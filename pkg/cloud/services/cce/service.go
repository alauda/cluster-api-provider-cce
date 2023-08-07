package cce

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	cce "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cce/v3"
	cceregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cce/v3/region"
	eip "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eip/v2"
	eipregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eip/v2/region"
	nat "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/nat/v2"
	natregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/nat/v2/region"
	vpc "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/vpc/v2"
	vpcregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/vpc/v2/region"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/alauda/cluster-api-provider-cce/pkg/cloud/scope"
)

type Service struct {
	scope     *scope.ManagedControlPlaneScope
	CCEClient *cce.CceClient
	EIPClient *eip.EipClient
	VPCClient *vpc.VpcClient
	NatClient *nat.NatClient
}

type ServiceOpts func(s *Service)

func NewService(controlPlaneScope *scope.ManagedControlPlaneScope, opts ...ServiceOpts) (*Service, error) {
	sec := &corev1.Secret{}
	err := controlPlaneScope.Client.Get(context.TODO(), types.NamespacedName{
		Name:      controlPlaneScope.ControlPlane.Spec.IdentityRef.Name,
		Namespace: controlPlaneScope.ControlPlane.Namespace,
	}, sec)
	if err != nil {
		return nil, err
	}

	controlPlaneScope.Debug("secret", "accessKey", string(sec.Data["accessKey"]), "secretKey", string(sec.Data["secretKey"]), "region", controlPlaneScope.ControlPlane.Spec.Region)

	auth := basic.NewCredentialsBuilder().
		WithAk(string(sec.Data["accessKey"])).
		WithSk(string(sec.Data["secretKey"])).
		Build()

	cceClient := cce.NewCceClient(
		cce.CceClientBuilder().
			WithRegion(cceregion.ValueOf(controlPlaneScope.Region())).
			WithCredential(auth).
			Build())

	eipClient := eip.NewEipClient(
		eip.EipClientBuilder().
			WithRegion(eipregion.ValueOf(controlPlaneScope.Region())).
			WithCredential(auth).
			Build())

	vpcClient := vpc.NewVpcClient(
		vpc.VpcClientBuilder().
			WithRegion(vpcregion.ValueOf(controlPlaneScope.Region())).
			WithCredential(auth).
			Build())

	natClient := nat.NewNatClient(
		nat.NatClientBuilder().
			WithRegion(natregion.ValueOf(controlPlaneScope.Region())).
			WithCredential(auth).
			Build())

	s := &Service{
		scope:     controlPlaneScope,
		CCEClient: cceClient,
		EIPClient: eipClient,
		VPCClient: vpcClient,
		NatClient: natClient,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s, nil
}

type NodepoolService struct {
	scope     *scope.ManagedMachinePoolScope
	CCEClient *cce.CceClient
}

func NewNodepoolService(machinePoolScope *scope.ManagedMachinePoolScope) (*NodepoolService, error) {
	sec := &corev1.Secret{}
	err := machinePoolScope.Client.Get(context.TODO(), types.NamespacedName{
		Name:      machinePoolScope.ControlPlane.Spec.IdentityRef.Name,
		Namespace: machinePoolScope.ControlPlane.Namespace,
	}, sec)
	if err != nil {
		return nil, err
	}

	machinePoolScope.Debug("secret", "accessKey", string(sec.Data["accessKey"]), "secretKey", string(sec.Data["secretKey"]), "region", machinePoolScope.ControlPlane.Spec.Region)

	auth := basic.NewCredentialsBuilder().
		WithAk(string(sec.Data["accessKey"])).
		WithSk(string(sec.Data["secretKey"])).
		Build()
	cceClient := cce.NewCceClient(
		cce.CceClientBuilder().
			WithRegion(cceregion.ValueOf(machinePoolScope.ControlPlane.Spec.Region)).
			WithCredential(auth).
			Build())
	return &NodepoolService{
		scope:     machinePoolScope,
		CCEClient: cceClient,
	}, nil
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

func parseURL(address string) (string, int, error) {
	u, err := url.Parse(address)
	if err != nil {
		return "", 0, errors.Wrap(err, "failed to pase url")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return "", 0, errors.Wrap(err, "String to int conversion error")
	}
	return u.Hostname(), port, nil
}
