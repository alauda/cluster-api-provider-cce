package cce

import (
	"fmt"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/sdkerr"
	vpcmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/vpc/v2/model"
	"github.com/pkg/errors"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
	infrautilstrings "github.com/alauda/cluster-api-provider-cce/pkg/util/strings"
)

const (
	defaultSubnetCidr = "192.168.0.0/24"
	defaultSubnetIP   = "192.168.0.1"
	primaryDns        = "100.125.1.250"
	secondaryDns      = "100.125.64.250"
)

func (s *Service) reconcileSubnets() error {
	s.scope.Info("Reconciling subnets")

	if s.scope.ControlPlane.Spec.NetworkSpec.Subnet.ID != "" {
		// TODO check exist
		return nil
	}

	if !conditions.Has(s.scope.ControlPlane, infrastructurev1beta1.VpcReadyCondition) {
		conditions.MarkFalse(s.scope.ControlPlane, infrastructurev1beta1.SubnetsReadyCondition, infrastructurev1beta1.SubnetsReconciliationFailedReason, clusterv1.ConditionSeverityInfo, "")
		if err := s.scope.PatchObject(); err != nil {
			return errors.Wrap(err, "failed to patch conditions")
		}
	}

	subnet, err := s.createSubnet()
	if err != nil {
		return errors.Wrap(err, "failed to create new subnet")
	}
	s.scope.ControlPlane.Spec.NetworkSpec.Subnet.ID = subnet.ID
	s.scope.ControlPlane.Status.Network.Subnet.ID = subnet.ID
	return nil
}

func (s *Service) createSubnet() (*infrastructurev1beta1.Subnet, error) {
	request := &vpcmodel.CreateSubnetRequest{}
	nameSubnet := infrautilstrings.GenerateName(fmt.Sprintf("subnet-%s-", s.scope.Name()))
	var listTagsSubnet = []string{
		"self-built*true",
	}
	vpcId := s.scope.ControlPlane.Spec.NetworkSpec.VPC.ID
	dnsList := []string{
		primaryDns,
		secondaryDns,
	}
	extraDhcpOpts := []vpcmodel.ExtraDhcpOption{
		{
			OptName: vpcmodel.GetExtraDhcpOptionOptNameEnum().ADDRESSTIME,
		},
		{
			OptName: vpcmodel.GetExtraDhcpOptionOptNameEnum().NTP,
		},
	}
	subnetbody := &vpcmodel.CreateSubnetOption{
		Name:          nameSubnet,
		Cidr:          defaultSubnetCidr,
		VpcId:         vpcId,
		GatewayIp:     defaultSubnetIP,
		Tags:          &listTagsSubnet,
		DhcpEnable:    pointer.Bool(true),
		PrimaryDns:    pointer.String(primaryDns),
		SecondaryDns:  pointer.String(secondaryDns),
		DnsList:       &dnsList,
		ExtraDhcpOpts: &extraDhcpOpts,
	}
	request.Body = &vpcmodel.CreateSubnetRequestBody{
		Subnet: subnetbody,
	}
	response, err := s.VPCClient.CreateSubnet(request)
	if err != nil {
		if e, ok := err.(*sdkerr.ServiceResponseError); ok {
			// The subnet has already existed in the VPC, or has been in conflict with the VPC subnet.
			if e.ErrorCode == "VPC.0204" {
				// query list subnets
				resp, err := s.listSubnets(vpcId)
				if err != nil {
					return nil, err
				}
				subnets := *resp.Subnets
				found := subnets[0]

				s.scope.Debug("Find exist Subnet with cidr", "subnet-id", found.Id, "cidr-block", found.Cidr)

				return &infrastructurev1beta1.Subnet{ID: found.Id}, nil
			}
		}
		return nil, errors.Wrap(err, "failed to create subnet")
	}

	s.scope.Debug("Created new Subnet with cidr", "subnet-id", response.Subnet.Id, "cidr-block", response.Subnet.Cidr)

	return &infrastructurev1beta1.Subnet{ID: response.Subnet.Id}, nil
}

func (s *Service) listSubnets(vpcId string) (*vpcmodel.ListSubnetsResponse, error) {
	request := &vpcmodel.ListSubnetsRequest{}
	vpcIdRequest := vpcId
	request.VpcId = &vpcIdRequest
	response, err := s.VPCClient.ListSubnets(request)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list subnets")
	}
	return response, nil
}

func (s *Service) deleteSubnet(vpcid, subnetId string) error {
	request := &vpcmodel.DeleteSubnetRequest{
		VpcId:    vpcid,
		SubnetId: subnetId,
	}
	_, err := s.VPCClient.DeleteSubnet(request)
	if err != nil {
		return errors.Wrap(err, "failed to delete subnet")
	}
	return nil
}
