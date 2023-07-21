package cce

import (
	"k8s.io/klog/v2"
	"sigs.k8s.io/cluster-api/util/conditions"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
	infrautilconditions "github.com/alauda/cluster-api-provider-cce/pkg/util/conditions"
)

// ReconcileNetwork reconciles the network of the given cluster.
func (s *Service) ReconcileNetwork() (err error) {
	s.scope.Debug("Reconciling network for cluster", "cluster", klog.KRef(s.scope.Namespace(), s.scope.Name()))

	// VPC
	if err := s.reconcileVPC(); err != nil {
		conditions.MarkFalse(s.scope.ControlPlane, infrastructurev1beta1.VpcReadyCondition, infrastructurev1beta1.VpcReconciliationFailedReason, infrautilconditions.ErrorConditionAfterInit(s.scope.ControlPlane), err.Error())
		return err
	}
	conditions.MarkTrue(s.scope.ControlPlane, infrastructurev1beta1.VpcReadyCondition)

	// Subnets
	if err := s.reconcileSubnets(); err != nil {
		conditions.MarkFalse(s.scope.ControlPlane, infrastructurev1beta1.SubnetsReadyCondition, infrastructurev1beta1.SubnetsReconciliationFailedReason, infrautilconditions.ErrorConditionAfterInit(s.scope.ControlPlane), err.Error())
		return err
	}
	conditions.MarkTrue(s.scope.ControlPlane, infrastructurev1beta1.SubnetsReadyCondition)

	// EIP
	// TODO

	s.scope.Debug("Reconcile network completed successfully")
	return nil
}

// DeleteNetwork deletes the network of the given cluster.
func (s *Service) DeleteNetwork() (err error) {
	return nil
}
