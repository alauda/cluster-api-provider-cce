package cce

import (
	"context"
	"fmt"
	"time"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/sdkerr"
	ccemodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cce/v3/model"
	"github.com/pkg/errors"
	"github.com/thoas/go-funk"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/record"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
)

const (
	defaultContainerCidr = "10.0.0.0/16"
	defautlServiceCidr   = "10.247.0.0/16"
)

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

	var (
		cluster       *ccemodel.ShowClusterResponse
		clusterStatus *ccemodel.ClusterStatus
		err           error
	)
	if s.scope.InfraClusterID() != "" {
		cluster, err = s.showCCECluster(s.scope.InfraClusterID())
		if err != nil {
			return errors.Wrap(err, "failed to show cce clusters")
		}
	}
	if cluster == nil {
		created, err := s.createCluster()
		if err != nil {
			return errors.Wrap(err, "failed to create cluster")
		}
		clusterStatus = created.Status
	} else {
		s.scope.Debug("Found owned CEE cluster", "cluster", klog.KRef("", s.scope.InfraClusterName()))
		s.scope.Debug("Found owned CEE cluster", "cluster", *cluster.Spec)

		updated, err := s.containerNetwork(cluster)
		if err != nil {
			return errors.Wrap(err, "failed to update cce containerNetwork")
		}
		if updated {
			cluster, err = s.showCCECluster(s.scope.InfraClusterID())
			if err != nil {
				return errors.Wrap(err, "failed to show cce clusters")
			}
		}

		clusterStatus = cluster.Status
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

	// bind EIP
	if err := s.reconcileEIP(ctx); err != nil {
		return errors.Wrap(err, "failed reconciling EIP")
	}

	cluster, err = s.showCCECluster(s.scope.InfraClusterID())
	if err != nil {
		return errors.Wrap(err, "failed to show cce clusters")
	}

	// set ControlPlaneEndpoint
	if cluster.Status.Endpoints != nil {
		for _, ep := range *cluster.Status.Endpoints {
			if *ep.Type == "Internal" {
				host, port, err := parseURL(*ep.Url)
				if err != nil {
					return err
				}
				s.scope.ControlPlane.Spec.ControlPlaneEndpoint = clusterv1.APIEndpoint{
					Host: host,
					Port: int32(port),
				}
			}
		}
		for _, ep := range *cluster.Status.Endpoints {
			if *ep.Type == "External" {
				host, port, err := parseURL(*ep.Url)
				if err != nil {
					return err
				}
				s.scope.ControlPlane.Spec.ControlPlaneEndpoint = clusterv1.APIEndpoint{
					Host: host,
					Port: int32(port),
				}
			}
		}
	}

	// set SecurityGroup
	if cluster.Spec.HostNetwork != nil {
		s.scope.ControlPlane.Spec.NetworkSpec.SecurityGroup.ID = pointer.StringDeref(cluster.Spec.HostNetwork.SecurityGroup, "")
	}

	if err := s.reconcileKubeconfig(ctx, cluster); err != nil {
		return errors.Wrap(err, "failed reconciling kubeconfig")
	}

	return nil
}

func (s *Service) showCCECluster(infraClusterID string) (*ccemodel.ShowClusterResponse, error) {
	request := &ccemodel.ShowClusterRequest{
		ClusterId: infraClusterID,
	}
	response, err := s.CCEClient.ShowCluster(request)
	if err != nil {
		if e, ok := err.(*sdkerr.ServiceResponseError); ok {
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
	var mode ccemodel.ContainerNetworkMode

	// Container network mode
	if ccemodel.GetContainerNetworkModeEnum().VPC_ROUTER.Value() == pointer.StringDeref(s.scope.ControlPlane.Spec.NetworkSpec.Mode, "vpc-router") {
		mode = ccemodel.GetContainerNetworkModeEnum().VPC_ROUTER
	} else {
		mode = ccemodel.GetContainerNetworkModeEnum().OVERLAY_L2
	}

	// Container Cidr
	var listCidrsContainerNetwork []ccemodel.ContainerCidr
	if s.scope.Cluster.Spec.ClusterNetwork.Pods != nil {
		for _, cidr := range s.scope.Cluster.Spec.ClusterNetwork.Pods.CIDRBlocks {
			listCidrsContainerNetwork = append(listCidrsContainerNetwork, ccemodel.ContainerCidr{
				Cidr: cidr,
			})
		}
	}
	if len(listCidrsContainerNetwork) == 0 {
		listCidrsContainerNetwork = append(listCidrsContainerNetwork, ccemodel.ContainerCidr{
			Cidr: defaultContainerCidr,
		})
	}

	// Service Cidr
	kubernetesSvcIpRangeSpec := defautlServiceCidr
	if s.scope.Cluster.Spec.ClusterNetwork.Services != nil {
		kubernetesSvcIpRangeSpec = s.scope.Cluster.Spec.ClusterNetwork.Services.String()
	}

	// extendParam
	enterpriseProjectIdExtendParam := s.scope.Project()
	extendParamSpec := &ccemodel.ClusterExtendParam{
		EnterpriseProjectId: &enterpriseProjectIdExtendParam,
	}
	if mode.Value() == ccemodel.GetContainerNetworkModeEnum().VPC_ROUTER.Value() {
		alphaCceFixPoolMaskExtendParam := "25"
		extendParamSpec.AlphaCceFixPoolMask = &alphaCceFixPoolMaskExtendParam
	}

	specbody := &ccemodel.ClusterSpec{
		Version: s.scope.ControlPlane.Spec.Version,
		Flavor:  pointer.StringDeref(s.scope.ControlPlane.Spec.Flavor, "cce.s2.small"),
		HostNetwork: &ccemodel.HostNetwork{
			Vpc:    s.scope.ControlPlane.Spec.NetworkSpec.VPC.ID,
			Subnet: s.scope.ControlPlane.Spec.NetworkSpec.Subnet.ID,
		},
		ContainerNetwork: &ccemodel.ContainerNetwork{
			Mode:  mode,
			Cidrs: &listCidrsContainerNetwork,
		},
		KubernetesSvcIpRange: &kubernetesSvcIpRangeSpec,
		ExtendParam:          extendParamSpec,
	}
	metadatabody := &ccemodel.ClusterMetadata{
		Name: s.scope.ControlPlane.Name,
	}

	request := &ccemodel.CreateClusterRequest{}
	request.Body = &ccemodel.Cluster{
		Spec:       specbody,
		Metadata:   metadatabody,
		ApiVersion: "v3",
		Kind:       "Cluster",
	}
	response, err := s.CCEClient.CreateCluster(request)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create CCE cluster")
	}
	s.scope.SetInfraClusterID(*response.Metadata.Uid)

	conditions.MarkTrue(s.scope.ControlPlane, infrastructurev1beta1.CCEControlPlaneCreatingCondition)

	s.scope.Info("Created CCE cluster in Huaweicloud", "cluster", klog.KRef("", s.scope.ControlPlane.Name))
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

// DeleteControlPlane deletes the CCE control plane.
func (s *Service) DeleteControlPlane() (err error) {
	s.scope.Debug("Deleting CCE control plane")

	// CCE Cluster
	if err := s.deleteCluster(); err != nil {
		return err
	}

	s.scope.Debug("Delete CCE control plane completed successfully")
	return nil
}

// deleteCluster deletes an CCE cluster.
func (s *Service) deleteCluster() error {
	infraClusterID := s.scope.InfraClusterID()

	if infraClusterID == "" {
		s.scope.Debug("no CCE cluster id, skipping CCE cluster deletion")
		return nil
	}

	cluster, err := s.showCCECluster(infraClusterID)
	if err != nil {
		return errors.Wrap(err, "unable to show CCE cluster")
	}
	if cluster == nil {
		return nil
	}

	err = s.deleteClusterAndWait(infraClusterID)
	if err != nil {
		return errors.Wrap(err, "unable to delete CCE cluster")
	}

	return nil
}

func (s *Service) deleteClusterAndWait(infraClusterID string) error {
	cluserName := s.scope.Name()
	s.scope.Info("Deleting CCE cluster", "cluster", klog.KRef("", cluserName))

	request := &ccemodel.DeleteClusterRequest{}
	request.ClusterId = infraClusterID
	_, err := s.CCEClient.DeleteCluster(request)
	if err != nil {
		if e, ok := err.(*sdkerr.ServiceResponseError); ok {
			if e.StatusCode == 404 {
				return nil
			}
			return errors.Wrap(err, "failed to delete cce cluster")
		}
		return errors.Wrap(err, "failed to delete cce cluster")
	}
	err = s.waitForClusterDeleted(infraClusterID)
	if err != nil {
		return errors.Wrapf(err, "failed waiting for cce cluster %s to delete", cluserName)
	}

	return nil
}

func (s *Service) waitForClusterDeleted(infraClusterID string) error {
	err := waitUntil(func() (bool, error) {
		c, err := s.showCCECluster(infraClusterID)
		if err != nil {
			return false, err
		}
		if c == nil {
			return true, nil
		}
		if *c.Status.Phase == ccemodel.GetNodePoolStatusPhaseEnum().DELETING.Value() {
			return false, nil
		}
		return true, nil
	}, 30*time.Second, 40)
	if err != nil {
		return err
	}
	return nil
}

func (s *Service) containerNetwork(cluster *ccemodel.ShowClusterResponse) (bool, error) {
	s.scope.Debug("Reconciling containerNetwork")

	existscount := 1
	existscidrs := []string{}
	if cluster.Spec.ContainerNetwork.Cidrs != nil {
		cidrs := *cluster.Spec.ContainerNetwork.Cidrs
		existscount = len(cidrs)

		for _, c := range cidrs {
			existscidrs = append(existscidrs, c.Cidr)
		}
	}
	if !(len(s.scope.Cluster.Spec.ClusterNetwork.Pods.CIDRBlocks) > existscount) {
		return false, nil
	}

	request := &ccemodel.UpdateClusterRequest{}
	request.ClusterId = s.scope.InfraClusterID()

	var listCidrsContainerNetwork []ccemodel.ContainerCidr
	if s.scope.Cluster.Spec.ClusterNetwork.Pods != nil {
		for _, cidr := range s.scope.Cluster.Spec.ClusterNetwork.Pods.CIDRBlocks {
			if funk.ContainsString(existscidrs, cidr) {
				continue
			}
			listCidrsContainerNetwork = append(listCidrsContainerNetwork, ccemodel.ContainerCidr{
				Cidr: cidr,
			})
		}
	}

	containerNetworkSpec := &ccemodel.ContainerNetworkUpdate{
		Cidrs: &listCidrsContainerNetwork,
	}
	specbody := &ccemodel.ClusterInformationSpec{
		ContainerNetwork: containerNetworkSpec,
	}
	request.Body = &ccemodel.ClusterInformation{
		Spec: specbody,
	}
	_, err := s.CCEClient.UpdateCluster(request)
	if err != nil {
		return false, errors.Wrapf(err, "failed to update containerNetwork for cce cluster %s", s.scope.InfraClusterName())
	}
	return true, nil
}
