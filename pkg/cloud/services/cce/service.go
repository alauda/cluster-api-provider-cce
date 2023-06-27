package cce

import (
	"context"
	"fmt"
	eipmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eip/v2/model"
	"k8s.io/utils/pointer"
	"net/url"
	"strconv"
	"time"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
	"github.com/alauda/cluster-api-provider-cce/pkg/cloud/scope"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/sdkerr"
	cce "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cce/v3"
	ccemodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cce/v3/model"
	cceregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cce/v3/region"
	eip "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eip/v2"
	eipregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eip/v2/region"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/record"
)

type Service struct {
	scope     *scope.ManagedControlPlaneScope
	CCEClient *cce.CceClient
	EIPClient *eip.EipClient
}

type ServiceOpts func(s *Service)

func NewService(controlPlaneScope *scope.ManagedControlPlaneScope, opts ...ServiceOpts) (*Service, error) {
	secret := corev1.Secret{}
	err := controlPlaneScope.Client.Get(context.TODO(), types.NamespacedName{}, &secret)
	if err != nil {
		return nil, err
	}

	auth := basic.NewCredentialsBuilder().
		WithAk(string(secret.Data["ak"])).
		WithSk(string(secret.Data["sk"])).
		Build()

	cceClient := cce.NewCceClient(
		cce.CceClientBuilder().
			WithRegion(cceregion.ValueOf(controlPlaneScope.ControlPlane.Spec.Region)).
			WithCredential(auth).
			Build())

	eipClient := eip.NewEipClient(
		eip.EipClientBuilder().
			WithRegion(eipregion.ValueOf(controlPlaneScope.ControlPlane.Spec.Region)).
			WithCredential(auth).
			Build())

	s := &Service{
		scope:     controlPlaneScope,
		CCEClient: cceClient,
		EIPClient: eipClient,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s, nil
}

// ReconcileControlPlane reconciles a CCE control plane.
func (s *Service) ReconcileControlPlane(ctx context.Context) error {
	s.scope.Debug("Reconciling CCE control plane", "cluster", klog.KRef(s.scope.Cluster.Namespace, s.scope.Cluster.Name))

	// CCE Cluster
	if err := s.reconcileCluster(ctx); err != nil {
		conditions.MarkFalse(s.scope.ControlPlane, infrastructurev1beta1.CCEControlPlaneReadyCondition, infrastructurev1beta1.CCEControlPlaneReconciliationFailedReason, clusterv1.ConditionSeverityError, err.Error())
		return err
	}
	conditions.MarkTrue(s.scope.ControlPlane, infrastructurev1beta1.CCEControlPlaneReadyCondition)

	s.scope.Debug("Reconcile CCE control plane completed successfully")
	return nil
}

func (s *Service) reconcileCluster(ctx context.Context) error {
	s.scope.Debug("Reconciling CCE cluster")

	var clusterStatus *ccemodel.ClusterStatus

	cluster, err := s.showCCECluster(s.scope.InfraClusterID())
	if err != nil {
		return errors.Wrap(err, "failed to show cce clusters")
	}
	if cluster == nil {
		created, err := s.createCluster()
		if err != nil {
			return errors.Wrap(err, "failed to create cluster")
		}
		clusterStatus = created.Status
	} else {
		clusterStatus = cluster.Status
		s.scope.Debug("Found owned CEE cluster", "cluster", klog.KRef("", s.scope.InfraClusterName()))
	}

	if err := s.setStatus(clusterStatus); err != nil {
		return errors.Wrap(err, "failed to set status")
	}

	// Wait for our cluster to be ready if necessary
	switch *clusterStatus.Phase {
	case "Creating", "Upgrading":
		cluster, err = s.waitForClusterActive()
	default:
		break
	}
	if err != nil {
		return errors.Wrap(err, "failed to wait for cluster to be active")
	}

	if !s.scope.ControlPlane.Status.Ready {
		return nil
	}

	s.scope.Debug("EKS Control Plane active", "endpoint", *cluster.Status.Endpoints)

	// s.scope.ControlPlane.Spec.ControlPlaneEndpoint = clusterv1.APIEndpoint{
	// 	Host: *cluster.Endpoint,
	// 	Port: 443,
	// }

	if err := s.reconcileEIP(ctx); err != nil {
		return errors.Wrap(err, "failed reconciling EIP")
	}

	cluster, err = s.showCCECluster(s.scope.InfraClusterID())
	if err != nil {
		return errors.Wrap(err, "failed to show cce clusters")
	}

	if cluster.Status.Endpoints != nil {
		for _, ep := range *cluster.Status.Endpoints {
			if *ep.Type == "Internal" {
				u, err := url.Parse(*ep.Url)
				if err != nil {
					return errors.Wrap(err, "failed to pase url")
				}
				port, err := strconv.Atoi(u.Port())
				if err != nil {
					return errors.Wrap(err, "String to int conversion error")
				}
				s.scope.ControlPlane.Spec.ControlPlaneEndpoint = clusterv1.APIEndpoint{
					Host: u.Hostname(),
					Port: int32(port),
				}
			}

			if *ep.Type == "External" {
				u, err := url.Parse(*ep.Url)
				if err != nil {
					return errors.Wrap(err, "failed to pase url")
				}
				port, err := strconv.Atoi(u.Port())
				if err != nil {
					return errors.Wrap(err, "String to int conversion error")
				}
				s.scope.ControlPlane.Spec.ControlPlaneEndpoint = clusterv1.APIEndpoint{
					Host: u.Hostname(),
					Port: int32(port),
				}
			}
		}
	}

	// if err := s.reconcileKubeconfig(ctx, cluster); err != nil {
	// 	return errors.Wrap(err, "failed reconciling kubeconfig")
	// }

	return nil
}

func (s *Service) reconcileEIP(ctx context.Context) error {
	s.scope.Info("Reconciling CCE  bind EIP", "cluster-name", pointer.StringDeref(pointer.String(s.scope.InfraClusterName()), ""))
	eipReq := &eipmodel.CreatePublicipRequest{}
	publicipbody := &eipmodel.CreatePublicipOption{
		Type: "5_bgp",
	}
	chargeMode := eipmodel.GetCreatePublicipBandwidthOptionChargeModeEnum().TRAFFIC
	bandwidthbody := &eipmodel.CreatePublicipBandwidthOption{
		ChargeMode: &chargeMode,
		Name:       pointer.String(s.scope.InfraClusterName()),
		ShareType:  eipmodel.GetCreatePublicipBandwidthOptionShareTypeEnum().PER,
		Size:       pointer.Int32(1),
	}
	eipReq.Body = &eipmodel.CreatePublicipRequestBody{
		Publicip:  publicipbody,
		Bandwidth: bandwidthbody,
	}
	eipRes, err := s.EIPClient.CreatePublicip(eipReq)
	if err != nil {
		return fmt.Errorf("failed to create EIP: %w", err)
	}

	// bind EIP
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

func (s *Service) showCCECluster(infraClusterID string) (*ccemodel.ShowClusterResponse, error) {
	request := &ccemodel.ShowClusterRequest{
		ClusterId: infraClusterID,
	}
	response, err := s.CCEClient.ShowCluster(request)
	if err != nil {
		if e, ok := err.(sdkerr.ServiceResponseError); ok {
			if e.StatusCode == 404 {
				return nil, nil
			}
			return nil, errors.Wrap(err, "failed to show cluster")
		}
		return nil, errors.Wrap(err, "failed to show cluster")
	}

	return response, nil
}

func (s *Service) waitForClusterActive() (*ccemodel.ShowClusterResponse, error) {
	err := waitUntil(func() (bool, error) {
		cluster, err := s.showCCECluster(s.scope.InfraClusterID())
		if err != nil {
			return false, err
		}
		if *cluster.Status.Phase != "Available" {
			return false, nil
		}
		return true, nil
	}, 30*time.Second, 40)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to wait for cce control plane %q", s.scope.InfraClusterName())
	}

	s.scope.Info("CCE control plane is now active", "cluster", klog.KRef("", s.scope.InfraClusterName()))

	cluster, err := s.showCCECluster(s.scope.InfraClusterID())
	if err != nil {
		return nil, errors.Wrap(err, "failed to show cce cluster")
	}

	if err := s.setStatus(cluster.Status); err != nil {
		return nil, errors.Wrap(err, "failed to set status")
	}

	return cluster, nil
}

func (s *Service) createCluster() (*ccemodel.CreateClusterResponse, error) {
	request := &ccemodel.CreateClusterRequest{}
	containerNetworkSpec := &ccemodel.ContainerNetwork{
		Mode: ccemodel.GetContainerNetworkModeEnum().VPC_ROUTER,
	}
	hostNetworkSpec := &ccemodel.HostNetwork{
		Vpc:    "0f902bd6-393a-4113-85d7-dc0e21ec3873",
		Subnet: "888fb6ce-02ee-4f3f-885b-94cedf540f0a",
	}
	specbody := &ccemodel.ClusterSpec{
		Flavor:           "cce.s2.small:",
		HostNetwork:      hostNetworkSpec,
		ContainerNetwork: containerNetworkSpec,
	}
	metadatabody := &ccemodel.ClusterMetadata{
		Name: "cluster-name1",
	}
	request.Body = &ccemodel.Cluster{
		Spec:       specbody,
		Metadata:   metadatabody,
		ApiVersion: "v3",
		Kind:       "cluster",
	}
	response, err := s.CCEClient.CreateCluster(request)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create CCE cluster")
	}
	s.scope.SetInfraClusterID(*response.Metadata.Uid)

	conditions.MarkTrue(s.scope.ControlPlane, infrastructurev1beta1.CCEControlPlaneCreatingCondition)

	s.scope.Info("Created CCE cluster in Huaweicloud", "cluster", klog.KRef("", *s.scope.ControlPlane.Spec.ClusterName))
	return response, nil
}

func (s *Service) setStatus(status *ccemodel.ClusterStatus) error {
	switch *status.Phase {
	case "Deleting":
		s.scope.ControlPlane.Status.Ready = false
	case "Unavailable":
		s.scope.ControlPlane.Status.Ready = false
		failureMsg := fmt.Sprintf("CCE cluster in unexpected %s reason. %s", *status.Reason, *status.Message)
		s.scope.ControlPlane.Status.FailureMessage = &failureMsg
	case "Available":
		s.scope.ControlPlane.Status.Ready = true
		s.scope.ControlPlane.Status.FailureMessage = nil
		if conditions.IsTrue(s.scope.ControlPlane, infrastructurev1beta1.CCEControlPlaneCreatingCondition) {
			record.Eventf(s.scope.ControlPlane, "SuccessfulCreateCCEControlPlane", "Created new CCE control plane %s", s.scope.InfraClusterName())
			conditions.MarkFalse(s.scope.ControlPlane, infrastructurev1beta1.CCEControlPlaneCreatingCondition, "created", clusterv1.ConditionSeverityInfo, "")
		}
		if conditions.IsTrue(s.scope.ControlPlane, infrastructurev1beta1.CCEControlPlaneUpdatingCondition) {
			conditions.MarkFalse(s.scope.ControlPlane, infrastructurev1beta1.CCEControlPlaneUpdatingCondition, "updated", clusterv1.ConditionSeverityInfo, "")
			record.Eventf(s.scope.ControlPlane, "SuccessfulUpdateCCEControlPlane", "Updated CCE control plane %s", s.scope.InfraClusterName())
		}
	case "Creating":
		s.scope.ControlPlane.Status.Ready = false
	case "Upgrading":
		s.scope.ControlPlane.Status.Ready = true
	default:
		return errors.Errorf("unexpected CCE cluster status %s", *status.Phase)
	}
	if err := s.scope.PatchObject(); err != nil {
		return errors.Wrap(err, "failed to update control plane")
	}
	return nil
}

func waitUntil(condition func() (bool, error), interval time.Duration, maxRetries int) error {
	for i := 0; i < maxRetries; i++ {
		ok, err := condition()
		if err != nil {
			return err
		}

		if ok {
			return nil
		}

		time.Sleep(interval)
	}

	return fmt.Errorf("failed to wait")
}
