package cce

import (
	"context"
	"fmt"

	ccemodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cce/v3/model"
	eipmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eip/v2/model"
	"github.com/pkg/errors"
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

	actionSpec := ccemodel.GetMasterEipRequestSpecActionEnum().BIND
	if pointer.BoolDeref(s.scope.ControlPlane.Spec.EndpointAccess.Public, true) {
		if epRes.Status.PublicEndpoint != nil && *epRes.Status.PublicEndpoint != "" {
			return nil
		}

		// create EIP
		eip, err := s.createEIP()
		if err != nil {
			return errors.Wrap(err, "failed to create new vpc")
		}
		// set status id
		s.scope.ControlPlane.Status.Network.EIP.ID = eip.ID
	} else {

		if epRes.Status.PublicEndpoint == nil ||
			(epRes.Status.PublicEndpoint != nil && *epRes.Status.PublicEndpoint == "") ||
			s.scope.ControlPlane.Status.Network.EIP.ID == "" {
			return nil
		}

		actionSpec = ccemodel.GetMasterEipRequestSpecActionEnum().UNBIND
	}

	// bind EIP
	s.scope.Debug("ready to binding EIP", "cluster", klog.KRef("", s.scope.InfraClusterName()))
	bindEIPReq := &ccemodel.UpdateClusterEipRequest{
		ClusterId: s.scope.InfraClusterID(),
	}
	specSpec := &ccemodel.MasterEipRequestSpecSpec{
		Id: pointer.String(s.scope.ControlPlane.Status.Network.EIP.ID),
	}
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

	// delete eip
	if !pointer.BoolDeref(s.scope.ControlPlane.Spec.EndpointAccess.Public, true) {
		if s.scope.ControlPlane.Status.Network.EIP.ID != "" {
			if err := s.deleteEIP(s.scope.ControlPlane.Status.Network.EIP.ID); err != nil {
				s.scope.Error(err, "failed to delete network eip")
			}
			s.scope.ControlPlane.Status.Network.EIP.ID = ""
		}
	}

	return nil
}

func (s *Service) createEIP() (*infrastructurev1beta1.EIP, error) {
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
		return nil, fmt.Errorf("failed to create EIP: %w", err)
	}
	return &infrastructurev1beta1.EIP{ID: *eipRes.Publicip.Id}, nil
}

func (s *Service) deleteEIP(id string) error {
	request := &eipmodel.DeletePublicipRequest{
		PublicipId: id,
	}
	_, err := s.EIPClient.DeletePublicip(request)
	if err != nil {
		return errors.Wrap(err, "failed to delete eip")
	}
	return nil
}
