package cce

import (
	"context"
	"fmt"

	ccemodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cce/v3/model"
	eipmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eip/v2/model"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
)

const (
	defaultEIPType = "5_bgp"
)

func (s *Service) reconcileEIP(ctx context.Context) error {
	s.scope.Info("Reconciling CCE  bind EIP", "cluster-name", pointer.StringDeref(pointer.String(s.scope.InfraClusterName()), ""))

	// show cluster endpoint
	epReq := &ccemodel.ShowClusterEndpointsRequest{}
	epReq.ClusterId = s.scope.InfraClusterID()
	epRes, err := s.CCEClient.ShowClusterEndpoints(epReq)
	if err != nil {
		return fmt.Errorf("failed to show cluster endpoint: %w", err)
	}
	if epRes.Status.PublicEndpoint != nil && *epRes.Status.PublicEndpoint != "" {
		return nil
	}

	// create EIP
	s.scope.Debug("ready to creating EIP", "cluster", klog.KRef("", s.scope.InfraClusterName()))
	eipReq := &eipmodel.CreatePublicipRequest{}
	publicipbody := &eipmodel.CreatePublicipOption{
		Type: defaultEIPType,
	}
	chargeMode := eipmodel.GetCreatePublicipBandwidthOptionChargeModeEnum().TRAFFIC
	bandwidthbody := &eipmodel.CreatePublicipBandwidthOption{
		ChargeMode: &chargeMode,
		Name:       pointer.String(s.scope.InfraClusterName()),
		ShareType:  eipmodel.GetCreatePublicipBandwidthOptionShareTypeEnum().PER,
		Size:       pointer.Int32(100),
	}
	enterpriseProjectId := s.scope.Project()
	eipReq.Body = &eipmodel.CreatePublicipRequestBody{
		Publicip:            publicipbody,
		Bandwidth:           bandwidthbody,
		EnterpriseProjectId: &enterpriseProjectId,
	}
	eipRes, err := s.EIPClient.CreatePublicip(eipReq)
	if err != nil {
		return fmt.Errorf("failed to create EIP: %w", err)
	}

	s.scope.ControlPlane.Status.Network = infrastructurev1beta1.NetworkStatus{
		EIP: infrastructurev1beta1.EIP{
			ID: pointer.StringDeref(eipRes.Publicip.Id, ""),
		},
	}

	// bind EIP
	s.scope.Debug("ready to binding EIP", "cluster", klog.KRef("", s.scope.InfraClusterName()))
	bindEIPReq := &ccemodel.UpdateClusterEipRequest{
		ClusterId: s.scope.InfraClusterID(),
	}
	specSpec := &ccemodel.MasterEipRequestSpecSpec{
		Id: eipRes.Publicip.Id,
	}
	actionSpec := ccemodel.GetMasterEipRequestSpecActionEnum().BIND
	specbody := &ccemodel.MasterEipRequestSpec{
		Action: &actionSpec,
		Spec:   specSpec,
	}
	bindEIPReq.Body = &ccemodel.MasterEipRequest{
		Spec: specbody,
	}
	_, err = s.CCEClient.UpdateClusterEip(bindEIPReq)
	if err != nil {
		return fmt.Errorf("failed to bind EIP: %w", err)
	}
	return nil
}

