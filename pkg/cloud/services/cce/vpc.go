package cce

import (
	"fmt"

	vpcmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/vpc/v2/model"
	"github.com/pkg/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
	infrautilstrings "github.com/alauda/cluster-api-provider-cce/pkg/util/strings"
)

const (
	defaultVPCCidr = "192.168.0.0/16"
)

func (s *Service) reconcileVPC() error {
	s.scope.Debug("Reconciling VPC")

	if s.scope.ControlPlane.Spec.NetworkSpec.VPC.ID != "" {
		// TODO check exist
		return nil
	}

	if !conditions.Has(s.scope.ControlPlane, infrastructurev1beta1.VpcReadyCondition) {
		conditions.MarkFalse(s.scope.ControlPlane, infrastructurev1beta1.VpcReadyCondition, infrastructurev1beta1.VpcCreationStartedReason, clusterv1.ConditionSeverityInfo, "")
		if err := s.scope.PatchObject(); err != nil {
			return errors.Wrap(err, "failed to patch conditions")
		}
	}

	vpc, err := s.createVPC()
	if err != nil {
		return errors.Wrap(err, "failed to create new vpc")
	}
	s.scope.ControlPlane.Spec.NetworkSpec.VPC.ID = vpc.ID

	return nil
}

func (s *Service) createVPC() (*infrastructurev1beta1.VPC, error) {
	request := &vpcmodel.CreateVpcRequest{}
	cidrVpc := defaultVPCCidr
	nameVpc := infrautilstrings.GenerateName(fmt.Sprintf("vpc-%s-", s.scope.Name()))
	var listTagsVpc = []string{
		"self-built*true",
	}
	enterpriseProjectId := s.scope.Project()
	vpcbody := &vpcmodel.CreateVpcOption{
		Name:                &nameVpc,
		Cidr:                &cidrVpc,
		EnterpriseProjectId: &enterpriseProjectId,
		Tags:                &listTagsVpc,
	}
	request.Body = &vpcmodel.CreateVpcRequestBody{
		Vpc: vpcbody,
	}
	response, err := s.VPCClient.CreateVpc(request)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create vpc")
	}

	s.scope.Debug("Created new VPC with cidr", "vpc-id", response.Vpc.Id, "cidr-block", response.Vpc.Cidr)

	return &infrastructurev1beta1.VPC{ID: response.Vpc.Id}, nil
}
