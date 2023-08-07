package cce

import (
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/nat/v2/model"
	"github.com/pkg/errors"
	"k8s.io/utils/pointer"

	"github.com/alauda/cluster-api-provider-cce/pkg/util/strings"
)

func (s *Service) reconcileNAT() (err error) {
	s.scope.Debug("Reconciling NAT")
	if s.scope.ControlPlane.Status.Network.Subnet.ID == "" {
		return nil
	}

	gatwayId := s.scope.ControlPlane.Status.Network.Nat.GatewayID
	if gatwayId == "" {
		gatwayId, err = s.createNatGateWay()
		if err != nil {
			return errors.Wrap(err, "failed to create new nat")
		}
	}

	eipid := s.scope.ControlPlane.Status.Network.Nat.EIPID
	if eipid == "" {
		eip, err := s.createEIP()
		if err != nil {
			return errors.Wrap(err, "failed to create new nat")
		}
		eipid = eip.ID
	}

	ruleId := s.scope.ControlPlane.Status.Network.Nat.RuleID
	if ruleId == "" {
		ruleId, err = s.createNatRule(gatwayId, eipid)
		if err != nil {
			return errors.Wrap(err, "failed to create new nat")
		}
	}

	s.scope.ControlPlane.Status.Network.Nat.EIPID = eipid
	s.scope.ControlPlane.Status.Network.Nat.GatewayID = gatwayId
	s.scope.ControlPlane.Status.Network.Nat.RuleID = ruleId

	return nil
}

func (s *Service) createNatGateWay() (string, error) {
	request := &model.CreateNatGatewayRequest{}
	request.Body = &model.CreateNatGatewayRequestBody{
		NatGateway: &model.CreateNatGatewayOption{
			Name:                strings.GenerateName("nat-"),
			EnterpriseProjectId: pointer.String(s.scope.Project()),
			Spec:                model.GetCreateNatGatewayOptionSpecEnum().E_1,
			InternalNetworkId:   s.scope.ControlPlane.Status.Network.Subnet.ID,
			RouterId:            s.scope.ControlPlane.Status.Network.VPC.ID,
		},
	}
	response, err := s.NatClient.CreateNatGateway(request)
	if err != nil {
		return "", errors.Wrap(err, "failed to create nat gateway")
	}

	s.scope.Debug("Created new Nat gateway", "gateway-id", response.NatGateway.Id)

	return response.NatGateway.Id, nil
}

func (s *Service) createNatRule(gatewayId, eip string) (string, error) {
	request := &model.CreateNatGatewaySnatRuleRequest{}
	request.Body = &model.CreateNatGatewaySnatRuleRequestOption{
		SnatRule: &model.CreateNatGatewaySnatRuleOption{
			SourceType:   pointer.Int32(0),
			NatGatewayId: gatewayId,
			NetworkId:    pointer.String(s.scope.ControlPlane.Status.Network.Subnet.ID),
			FloatingIpId: eip,
		},
	}
	response, err := s.NatClient.CreateNatGatewaySnatRule(request)
	if err != nil {
		return "", errors.Wrap(err, "failed to create nat gateway")
	}
	s.scope.Debug("Created new Nat rule", "rule-id", response.SnatRule.Id)

	return response.SnatRule.Id, nil
}

func (s *Service) deleteNatRule(gatewayId, ruleId string) error {
	request := &model.DeleteNatGatewaySnatRuleRequest{
		NatGatewayId: gatewayId,
		SnatRuleId:   ruleId,
	}
	_, err := s.NatClient.DeleteNatGatewaySnatRule(request)
	if err != nil {
		return errors.Wrap(err, "failed to delete nat rule")
	}
	return nil
}

func (s *Service) deleteNatGateway(gatewayId string) error {
	request := &model.DeleteNatGatewayRequest{
		NatGatewayId: gatewayId,
	}
	_, err := s.NatClient.DeleteNatGateway(request)
	if err != nil {
		return errors.Wrap(err, "failed to delete nat gatway")
	}
	return nil
}
