package cce

import (
	"time"

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
	time.Sleep(3 * time.Second)

	// Subnets
	if err := s.reconcileSubnets(); err != nil {
		conditions.MarkFalse(s.scope.ControlPlane, infrastructurev1beta1.SubnetsReadyCondition, infrastructurev1beta1.SubnetsReconciliationFailedReason, infrautilconditions.ErrorConditionAfterInit(s.scope.ControlPlane), err.Error())
		return err
	}
	conditions.MarkTrue(s.scope.ControlPlane, infrastructurev1beta1.SubnetsReadyCondition)

	time.Sleep(3 * time.Second)
	// NAT
	if err := s.reconcileNAT(); err != nil {
		conditions.MarkFalse(s.scope.ControlPlane, infrastructurev1beta1.NatReadyCondition, infrastructurev1beta1.NatReconciliationFailedReason, infrautilconditions.ErrorConditionAfterInit(s.scope.ControlPlane), err.Error())
		return err
	}
	conditions.MarkTrue(s.scope.ControlPlane, infrastructurev1beta1.NatReadyCondition)

	s.scope.Debug("Reconcile network completed successfully")
	return nil
}

// DeleteNetwork deletes the network of the given cluster.
func (s *Service) DeleteNetwork() (err error) {
	s.scope.Debug("Deleting network")

	if s.scope.ControlPlane.Status.Network.Nat.RuleID != "" && s.scope.ControlPlane.Status.Network.Nat.GatewayID != "" {
		if err := s.deleteNatRule(s.scope.ControlPlane.Status.Network.Nat.GatewayID, s.scope.ControlPlane.Status.Network.Nat.RuleID); err != nil {
			s.scope.Error(err, "failed to delete network nat rule")
		}
		time.Sleep(3 * time.Second)
	}
	if s.scope.ControlPlane.Status.Network.Nat.GatewayID != "" {
		if err := s.deleteNatGateway(s.scope.ControlPlane.Status.Network.Nat.GatewayID); err != nil {
			s.scope.Error(err, "failed to delete network nat gateway")
		}
		time.Sleep(3 * time.Second)
	}
	if s.scope.ControlPlane.Status.Network.Nat.EIPID != "" {
		if err := s.deleteEIP(s.scope.ControlPlane.Status.Network.Nat.EIPID); err != nil {
			s.scope.Error(err, "failed to delete network nat eip")
		}
		time.Sleep(3 * time.Second)
	}

	if s.scope.ControlPlane.Status.Network.Subnet.ID != "" {
		if err := s.deleteSubnet(s.scope.ControlPlane.Status.Network.VPC.ID, s.scope.ControlPlane.Status.Network.Subnet.ID); err != nil {
			s.scope.Error(err, "failed to delete network subnet")
		}
		time.Sleep(3 * time.Second)
	}

	if s.scope.ControlPlane.Status.Network.VPC.ID != "" {
		if err := s.deleteVPC(s.scope.ControlPlane.Status.Network.VPC.ID); err != nil {
			s.scope.Error(err, "failed to delete network vpc")
		}
		time.Sleep(3 * time.Second)
	}

	if s.scope.ControlPlane.Status.Network.EIP.ID != "" {
		if err := s.deleteEIP(s.scope.ControlPlane.Status.Network.EIP.ID); err != nil {
			s.scope.Error(err, "failed to delete network eip")
		}
	}

	s.scope.Debug("Delete network completed successfully")
	return nil
}
