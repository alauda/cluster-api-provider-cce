package cce

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/sdkerr"
	ccemodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cce/v3/model"
	"github.com/pkg/errors"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
)

const (
	defaultOSNode     = "Huawei Cloud EulerOS 2.0"
	defaultVolumetype = "GPSSD"
	defaultRuntime    = "containerd"
)

func (s *NodepoolService) ReconcilePool(ctx context.Context) error {
	s.scope.Debug("Reconciling CCE nodepool")

	if err := s.reconcileNodepool(ctx); err != nil {
		conditions.MarkFalse(s.scope.ManagedMachinePool, infrastructurev1beta1.CCENodepoolReadyCondition, infrastructurev1beta1.CCENodepoolReconciliationFailedReason, clusterv1.ConditionSeverityError, err.Error())
		return err
	}
	conditions.MarkTrue(s.scope.ManagedMachinePool, infrastructurev1beta1.CCENodepoolReadyCondition)

	return nil
}

func (s *NodepoolService) ReconcilePoolDelete() error {
	s.scope.Debug("Reconciling deletion of CCE nodepool")

	// cceNodepoolName := s.scope.Name()

	np, err := s.showNodepool()
	if err != nil {
		return errors.Wrap(err, "failed to show CCE nodepool")
	}

	if np == nil {
		return nil
	}

	if err := s.deleteNodepoolAndWait(); err != nil {
		return errors.Wrap(err, "failed to delete nodepool")
	}

	s.scope.Debug("Delete nodepool completed successfully")

	return nil
}

func (s *NodepoolService) deleteNodepoolAndWait() (reterr error) {
	nodepoolName := s.scope.Name()
	if err := s.scope.NodepoolReadyFalse(clusterv1.DeletingReason, ""); err != nil {
		return err
	}
	defer func() {
		if reterr != nil {
			if err := s.scope.NodepoolReadyFalse("DeletingFailed", reterr.Error()); err != nil {
				reterr = err
			}
		} else if err := s.scope.NodepoolReadyFalse(clusterv1.DeletedReason, ""); err != nil {
			reterr = err
		}
	}()

	request := &ccemodel.DeleteNodePoolRequest{}
	request.ClusterId = s.scope.ControlPlane.InfraClusterID()
	request.NodepoolId = s.scope.InfraMachinePoolID()
	_, err := s.CCEClient.DeleteNodePool(request)
	if err != nil {
		if e, ok := err.(*sdkerr.ServiceResponseError); ok {
			if e.StatusCode == 404 {
				return nil
			}
			return errors.Wrap(err, "failed to delete nodepool")
		}
		return errors.Wrap(err, "failed to delete nodepool")
	}
	// wait until deleted
	err = s.waitForNodepoolDeleted()
	if err != nil {
		return errors.Wrapf(err, "failed waiting for CCE nodepool %s to delete", nodepoolName)
	}
	return nil
}

func (s *NodepoolService) reconcileNodepool(ctx context.Context) error {
	var (
		np  *ccemodel.NodePool
		err error
	)

	if s.scope.InfraMachinePoolID() != "" {
		np, err = s.showNodepool()
		if err != nil {
			return errors.Wrap(err, "failed to show nodepool")
		}

		// update nodepool
		update, err := s.updateNodepool(np)
		if err != nil {
			return errors.Wrap(err, "failed to update nodepool")
		}

		time.Sleep(2 * time.Second)
		if update {
			np, err = s.showNodepool()
			if err != nil {
				return errors.Wrap(err, "failed to show nodepool")
			}
		}
	}

	if np == nil {
		clusterName, nodepoolName := s.scope.ControlPlane.GetName(), s.scope.ManagedMachinePool.GetName()
		np, err = s.createNodepool()
		if err != nil {
			return errors.Wrap(err, "failed to create nodepool")
		}
		s.scope.Info("Created CCE nodepool in HuaweiCloud", "cluster-name", clusterName, "nodepool-name", nodepoolName)
	}

	if err := s.setStatus(np); err != nil {
		return errors.Wrap(err, "failed to set status")
	}

	switch *np.Status.Phase {
	case ccemodel.GetNodePoolStatusPhaseEnum().SYNCHRONIZING, ccemodel.GetNodePoolStatusPhaseEnum().SYNCHRONIZED:
		_, err = s.waitForNodepoolActive()
	default:
		break
	}

	// set machinepool replicas to ccemanaged
	if *s.scope.MachinePool.Spec.Replicas != *np.Spec.InitialNodeCount {
		s.scope.Info("Setting MachinePool replicas to InitialNodeCount", "local", *s.scope.MachinePool.Spec.Replicas, "external", *np.Spec.InitialNodeCount)
		s.scope.MachinePool.Spec.Replicas = np.Spec.InitialNodeCount
		if err := s.scope.PatchCAPIMachinePoolObject(ctx); err != nil {
			return err
		}
	}

	if err != nil {
		return errors.Wrap(err, "failed to wait for nodepool to be active")
	}

	return nil
}

func (s *NodepoolService) showNodepool() (*ccemodel.NodePool, error) {
	request := &ccemodel.ShowNodePoolRequest{}
	request.ClusterId = s.scope.ControlPlane.InfraClusterID()
	request.NodepoolId = s.scope.InfraMachinePoolID()
	response, err := s.CCEClient.ShowNodePool(request)
	if err != nil {
		if e, ok := err.(*sdkerr.ServiceResponseError); ok {
			if e.StatusCode == 404 {
				return nil, nil
			}
			return nil, errors.Wrap(err, "failed to show nodepool")
		}
		return nil, errors.Wrap(err, "failed to show nodepool")
	}
	return &ccemodel.NodePool{
		Kind:       *response.Kind,
		ApiVersion: *response.ApiVersion,
		Metadata:   response.Metadata,
		Spec:       response.Spec,
		Status:     response.Status,
	}, nil
}

func (s *NodepoolService) createNodepool() (*ccemodel.NodePool, error) {
	request := &ccemodel.CreateNodePoolRequest{}
	request.ClusterId = s.scope.ControlPlane.InfraClusterID()

	nameRuntime := toRuntimeName(pointer.StringDeref(s.scope.ManagedMachinePool.Spec.Runtime, defaultRuntime))

	var listTaintsNodeTemplate []ccemodel.Taint
	if s.scope.ManagedMachinePool.Spec.Taints != nil {
		taints := *s.scope.ManagedMachinePool.Spec.Taints
		for _, t := range taints {
			listTaintsNodeTemplate = append(listTaintsNodeTemplate, ccemodel.Taint{
				Key:    t.Key,
				Value:  t.Value,
				Effect: toTaintEffect(t.Effect),
			})
		}
	}

	listK8sTagsNodeTemplate := s.scope.ManagedMachinePool.Spec.Labels
	var listDataVolumesNodeTemplate []ccemodel.Volume
	for _, d := range s.scope.ManagedMachinePool.Spec.DataVolumes {
		listDataVolumesNodeTemplate = append(listDataVolumesNodeTemplate, ccemodel.Volume{
			Size:       d.Size,
			Volumetype: defaultVolumetype,
		})
	}
	// 使用集群子网
	if s.scope.ManagedMachinePool.Spec.Subnet.ID == "" {
		s.scope.ManagedMachinePool.Spec.Subnet.ID = s.scope.ControlPlane.Spec.NetworkSpec.Subnet.ID
	}

	nodeTemplateSpec := &ccemodel.NodeSpec{
		Flavor: pointer.StringDeref(s.scope.ManagedMachinePool.Spec.Flavor, "c6s.4xlarge.2"),
		Az:     "random",
		Os:     pointer.String(pointer.StringDeref(s.scope.ManagedMachinePool.Spec.Os, defaultOSNode)),
		Login: &ccemodel.Login{
			SshKey: s.scope.ManagedMachinePool.Spec.SSHKeyName,
		},
		RootVolume: &ccemodel.Volume{
			Size:       s.scope.ManagedMachinePool.Spec.RootVolume.Size,
			Volumetype: defaultVolumetype,
		},
		DataVolumes: listDataVolumesNodeTemplate,
		NodeNicSpec: &ccemodel.NodeNicSpec{
			PrimaryNic: &ccemodel.NicSpec{
				SubnetId: pointer.String(s.scope.ManagedMachinePool.Spec.Subnet.ID),
			},
		},
		Taints:  &listTaintsNodeTemplate,
		K8sTags: listK8sTagsNodeTemplate,
		Runtime: &ccemodel.Runtime{
			Name: &nameRuntime,
		},
	}
	typeSpec := ccemodel.GetNodePoolSpecTypeEnum().VM
	metadatabody := &ccemodel.NodePoolMetadata{
		Name: s.scope.Name(),
	}
	specbody := &ccemodel.NodePoolSpec{
		Type:             &typeSpec,
		NodeTemplate:     nodeTemplateSpec,
		InitialNodeCount: s.scope.ManagedMachinePool.Spec.Replicas,
		Autoscaling: &ccemodel.NodePoolNodeAutoscaling{
			Enable: pointer.Bool(false),
		},
	}
	if s.scope.ControlPlane.Spec.NetworkSpec.SecurityGroup.ID != "" {
		securityGroups := []string{
			s.scope.ControlPlane.Spec.NetworkSpec.SecurityGroup.ID,
		}
		specbody.CustomSecurityGroups = &securityGroups
	}
	request.Body = &ccemodel.NodePool{
		Metadata:   metadatabody,
		Spec:       specbody,
		ApiVersion: "v3",
		Kind:       "NodePool",
	}
	if j, err := json.Marshal(request); err == nil {
		s.scope.Logger.Debug("node request", "request", string(j))
	}
	response, err := s.CCEClient.CreateNodePool(request)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create CCE nodepool")
	}
	s.scope.SetInfraMachinePoolID(*response.Metadata.Uid)

	return &ccemodel.NodePool{
		Kind:       *response.Kind,
		ApiVersion: *response.ApiVersion,
		Metadata:   response.Metadata,
		Spec:       response.Spec,
		Status:     response.Status,
	}, nil
}

func (s *NodepoolService) updateNodepool(found *ccemodel.NodePool) (bool, error) {
	s.scope.Debug("Reconciling update CCE nodepool")
	request := &ccemodel.UpdateNodePoolRequest{}
	request.ClusterId = s.scope.ControlPlane.InfraClusterID()
	request.NodepoolId = s.scope.InfraMachinePoolID()

	update := false

	// label
	listK8sTagsNodeTemplate := s.scope.ManagedMachinePool.Spec.Labels
	if len(found.Spec.NodeTemplate.K8sTags) > 0 {
		delete(found.Spec.NodeTemplate.K8sTags, "cce.cloud.com/cce-nodepool")
	}
	if !reflect.DeepEqual(listK8sTagsNodeTemplate, found.Spec.NodeTemplate.K8sTags) {
		update = true

		// Merge local & external cce config
		for k, v := range found.Spec.NodeTemplate.K8sTags {
			if _, ok := listK8sTagsNodeTemplate[k]; !ok {
				listK8sTagsNodeTemplate[k] = v
			}
		}
	}

	// taint
	var listTaintsNodeTemplate []ccemodel.Taint
	if s.scope.ManagedMachinePool.Spec.Taints != nil {
		taints := *s.scope.ManagedMachinePool.Spec.Taints
		for _, t := range taints {
			listTaintsNodeTemplate = append(listTaintsNodeTemplate, ccemodel.Taint{
				Key:    t.Key,
				Value:  t.Value,
				Effect: toTaintEffect(t.Effect),
			})
		}
		// diff
		if found.Spec.NodeTemplate.Taints != nil {
			foundTaints := *found.Spec.NodeTemplate.Taints
			if !reflect.DeepEqual(foundTaints, listTaintsNodeTemplate) {
				update = true

				// Merge local & external cce config
				for _, ft := range foundTaints {
					if findTaint(listTaintsNodeTemplate, ft) {
						listTaintsNodeTemplate = append(listTaintsNodeTemplate, ft)
					}
				}
			}
		}
	}
	if s.scope.ManagedMachinePool.Spec.Taints == nil && found.Spec.NodeTemplate.Taints != nil {
		update = true
	}

	// autoscaling
	enableAutoscaling := false
	autoscalingSpec := &ccemodel.NodePoolNodeAutoscaling{
		Enable: &enableAutoscaling,
	}
	if found.Spec.Autoscaling != nil && pointer.BoolDeref(found.Spec.Autoscaling.Enable, false) {
		autoscalingSpec = found.Spec.Autoscaling
	}

	// node count
	foundNodeCount := pointer.Int32Deref(found.Spec.InitialNodeCount, 0)
	initialNodeCount := pointer.Int32Deref(s.scope.ManagedMachinePool.Spec.Replicas, 1)
	if foundNodeCount != initialNodeCount {
		update = true

		if *autoscalingSpec.Enable {
			if *s.scope.ManagedMachinePool.Spec.Replicas < *autoscalingSpec.MinNodeCount {
				s.scope.ManagedMachinePool.Spec.Replicas = autoscalingSpec.MinNodeCount
			}
			if *s.scope.ManagedMachinePool.Spec.Replicas > *autoscalingSpec.MaxNodeCount {
				s.scope.ManagedMachinePool.Spec.Replicas = autoscalingSpec.MaxNodeCount
			}
			// Merge local & external cce config
			s.scope.ManagedMachinePool.Spec.Replicas = pointer.Int32(foundNodeCount)
		}
	}

	metadatabody := &ccemodel.NodePoolMetadataUpdate{
		Name: s.scope.ManagedMachinePool.Name,
	}
	nodeTemplateSpec := &ccemodel.NodeSpecUpdate{
		Taints:  listTaintsNodeTemplate,
		K8sTags: listK8sTagsNodeTemplate,
	}
	specbody := &ccemodel.NodePoolSpecUpdate{
		NodeTemplate:     nodeTemplateSpec,
		InitialNodeCount: pointer.Int32Deref(s.scope.ManagedMachinePool.Spec.Replicas, 1),
		Autoscaling:      autoscalingSpec,
	}
	request.Body = &ccemodel.NodePoolUpdate{
		Spec:     specbody,
		Metadata: metadatabody,
	}

	if update {
		_, err := s.CCEClient.UpdateNodePool(request)
		if err != nil {
			return false, err
		}
	}
	return update, nil
}

func (s *NodepoolService) setStatus(np *ccemodel.NodePool) error {
	managedPool := s.scope.ManagedMachinePool

	status := np.Status

	switch *status.Phase {
	case ccemodel.GetNodePoolStatusPhaseEnum().DELETING:
		managedPool.Status.Ready = false
	case ccemodel.GetNodePoolStatusPhaseEnum().ERROR, ccemodel.GetNodePoolStatusPhaseEnum().SOLD_OUT:
		managedPool.Status.Ready = false
		failureMsg := fmt.Sprintf("CCE nodeppol in failed %s status", *np.Status.Phase)
		managedPool.Status.FailureMessage = &failureMsg
	case ccemodel.GetNodePoolStatusPhaseEnum().SYNCHRONIZING, ccemodel.GetNodePoolStatusPhaseEnum().SYNCHRONIZED:
		managedPool.Status.Ready = false
	default:
		if np.Status.Phase.Value() == "" {
			// 空值: 可用（节点池当前节点数已达到预期，且无伸缩中的节点）
			managedPool.Status.Ready = true
			managedPool.Status.FailureMessage = nil
		}
	}
	if managedPool.Status.Ready && np.Status.CurrentNode != nil && *np.Status.CurrentNode > 0 {
		machinePoolID := managedPool.InfraMachinePoolID()
		request := &ccemodel.ListNodesRequest{}
		request.ClusterId = s.scope.ControlPlane.InfraClusterID()
		response, err := s.CCEClient.ListNodes(request)
		if err != nil {
			return errors.Wrap(err, "failed to list nodes for cluster")
		}
		var providerIDList []string
		for _, node := range *response.Items {
			if len(node.Metadata.Annotations) == 0 {
				continue
			}
			if id, ok := node.Metadata.Annotations["kubernetes.io/node-pool.id"]; !ok || id != machinePoolID {
				continue
			}
			providerIDList = append(providerIDList, pointer.StringDeref(node.Metadata.Uid, ""))
		}
		managedPool.Spec.ProviderIDList = providerIDList
		managedPool.Status.Replicas = *np.Status.CurrentNode
	}
	if err := s.scope.PatchObject(); err != nil {
		return errors.Wrap(err, "failed to update nodepool")
	}
	return nil
}

func (s *NodepoolService) waitForNodepoolActive() (*ccemodel.NodePool, error) {
	nodepoolName := s.scope.ManagedMachinePool.GetName()
	err := waitUntil(func() (bool, error) {
		np, err := s.showNodepool()
		if err != nil {
			return false, err
		}
		if np.Status.Phase.Value() != "" {
			return false, nil
		}
		return true, nil
	}, 30*time.Second, 40)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to wait for cce nodepool %q", nodepoolName)
	}

	s.scope.Info("CCE nodepool is now available", "nodeppol-name", nodepoolName)

	np, err := s.showNodepool()
	if err != nil {
		return nil, errors.Wrap(err, "failed to describe CCE nodepool")
	}

	if err := s.setStatus(np); err != nil {
		return nil, errors.Wrap(err, "failed to set status")
	}

	return np, nil
}

func (s *NodepoolService) waitForNodepoolDeleted() error {
	err := waitUntil(func() (bool, error) {
		np, err := s.showNodepool()
		if err != nil {
			return false, err
		}
		if np == nil {
			return true, nil
		}
		if np.Status.Phase.Value() == ccemodel.GetNodePoolStatusPhaseEnum().DELETING.Value() {
			return false, nil
		}
		return true, nil
	}, 30*time.Second, 40)
	if err != nil {
		return err
	}
	return nil
}

func toTaintEffect(effect string) ccemodel.TaintEffect {
	switch effect {
	case "NoSchedule":
		return ccemodel.GetTaintEffectEnum().NO_SCHEDULE
	case "PreferNoSchedule":
		return ccemodel.GetTaintEffectEnum().PREFER_NO_SCHEDULE
	case "NoExecute":
		return ccemodel.GetTaintEffectEnum().NO_EXECUTE
	}
	return ccemodel.GetTaintEffectEnum().NO_SCHEDULE
}

func toRuntimeName(runtime string) ccemodel.RuntimeName {
	if runtime == ccemodel.GetRuntimeNameEnum().DOCKER.Value() {
		return ccemodel.GetRuntimeNameEnum().DOCKER
	}
	return ccemodel.GetRuntimeNameEnum().CONTAINERD
}

func findTaint(taints []ccemodel.Taint, taint ccemodel.Taint) bool {
	for _, t := range taints {
		if t.Key == taint.Key {
			return true
		}
	}
	return false
}
