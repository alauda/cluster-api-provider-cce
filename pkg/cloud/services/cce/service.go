package cce

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/sdkerr"
	cce "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cce/v3"
	ccemodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cce/v3/model"
	cceregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cce/v3/region"
	eip "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eip/v2"
	eipmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eip/v2/model"
	eipregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eip/v2/region"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/kubeconfig"
	"sigs.k8s.io/cluster-api/util/record"
	"sigs.k8s.io/cluster-api/util/secret"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
	"github.com/alauda/cluster-api-provider-cce/pkg/cloud/scope"
)

type Service struct {
	scope     *scope.ManagedControlPlaneScope
	CCEClient *cce.CceClient
	EIPClient *eip.EipClient
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

	controlPlaneScope.Debug("secret", "accessKey", string(sec.Data["accessKey"]), "secretKey", string(sec.Data["secretKey"]))

	auth := basic.NewCredentialsBuilder().
		WithAk(string(sec.Data["accessKey"])).
		WithSk(string(sec.Data["secretKey"])).
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

	if err := s.reconcileKubeconfig(ctx, cluster); err != nil {
		return errors.Wrap(err, "failed reconciling kubeconfig")
	}

	return nil
}

func (s *Service) reconcileKubeconfig(ctx context.Context, cluster *ccemodel.ShowClusterResponse) error {
	s.scope.Debug("Reconciling CCE kubeconfigs for cluster", "cluster-name", s.scope.InfraClusterName())

	clusterRef := types.NamespacedName{
		Name:      s.scope.Cluster.Name,
		Namespace: s.scope.Cluster.Namespace,
	}

	// Create the kubeconfig used by CAPI
	configSecret, err := secret.GetFromNamespacedName(ctx, s.scope.Client, clusterRef, secret.Kubeconfig)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return errors.Wrap(err, "failed to get kubeconfig secret")
		}

		if createErr := s.createCAPIKubeconfigSecret(
			ctx,
			cluster,
			&clusterRef,
		); createErr != nil {
			return fmt.Errorf("creating kubeconfig secret: %w", err)
		}
	} else if updateErr := s.updateCAPIKubeconfigSecret(ctx, configSecret, cluster); updateErr != nil {
		return fmt.Errorf("updating kubeconfig secret: %w", err)
	}

	// Set initialized to true to indicate the kubconfig has been created
	s.scope.ControlPlane.Status.Initialized = true

	return nil
}

func (s *Service) reconcileEIP(ctx context.Context) error {
	s.scope.Info("Reconciling CCE  bind EIP", "cluster-name", pointer.StringDeref(pointer.String(s.scope.InfraClusterName()), ""))

	// create EIP
	eipReq := &eipmodel.CreatePublicipRequest{}
	publicipbody := &eipmodel.CreatePublicipOption{
		Type: "5_bgp",
	}
	chargeMode := eipmodel.GetCreatePublicipBandwidthOptionChargeModeEnum().TRAFFIC
	bandwidthbody := &eipmodel.CreatePublicipBandwidthOption{
		ChargeMode: &chargeMode,
		Name:       pointer.String(s.scope.InfraClusterName()),
		ShareType:  eipmodel.GetCreatePublicipBandwidthOptionShareTypeEnum().PER,
		Size:       pointer.Int32(100),
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
		Vpc:    s.scope.ControlPlane.Spec.NetworkSpec.VPC.ID,
		Subnet: s.scope.ControlPlane.Spec.NetworkSpec.Subnet.ID,
	}
	specbody := &ccemodel.ClusterSpec{
		Flavor:           *s.scope.ControlPlane.Spec.Flavor,
		HostNetwork:      hostNetworkSpec,
		ContainerNetwork: containerNetworkSpec,
	}
	metadatabody := &ccemodel.ClusterMetadata{
		Name: s.scope.ControlPlane.Name,
	}
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

	s.scope.Info("Created CCE cluster in Huaweicloud", "cluster", klog.KRef("", *&s.scope.ControlPlane.Name))
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

func (s *Service) createCAPIKubeconfigSecret(ctx context.Context, cluster *ccemodel.ShowClusterResponse, clusterRef *types.NamespacedName) error {
	controllerOwnerRef := *metav1.NewControllerRef(s.scope.ControlPlane, infrastructurev1beta1.GroupVersion.WithKind("CCEManagedControlPlane"))

	clusterName := s.scope.InfraClusterName()
	userName := s.getKubeConfigUserName(clusterName, false)

	cfg, err := s.createBaseKubeConfig(cluster, userName)
	if err != nil {
		return fmt.Errorf("creating base kubeconfig: %w", err)
	}

	//token, err := s.generateToken()
	//if err != nil {
	//	return fmt.Errorf("generating presigned token: %w", err)
	//}

	//cfg.AuthInfos = map[string]*api.AuthInfo{
	//	userName: {
	//		Token: token,
	//	},
	//}

	out, err := clientcmd.Write(*cfg)
	if err != nil {
		return errors.Wrap(err, "failed to serialize config to yaml")
	}

	kubeconfigSecret := kubeconfig.GenerateSecretWithOwner(*clusterRef, out, controllerOwnerRef)
	if err := s.scope.Client.Create(ctx, kubeconfigSecret); err != nil {
		return errors.Wrap(err, "failed to create kubeconfig secret")
	}

	record.Eventf(s.scope.ControlPlane, "SucessfulCreateKubeconfig", "Created kubeconfig for cluster %q", s.scope.InfraClusterName())
	return nil
}

func (s *Service) getKubeConfigUserName(clusterName string, isUser bool) string {
	if isUser {
		return fmt.Sprintf("%s-user", clusterName)
	}

	return fmt.Sprintf("%s-capi-admin", clusterName)
}

func (s *Service) createBaseKubeConfig(cluster *ccemodel.ShowClusterResponse, userName string) (*api.Config, error) {
	clusterName := s.scope.InfraClusterName()
	contextName := fmt.Sprintf("%s@%s", userName, clusterName)

	// create cce cert
	clientCertificateData, clientKeyData, err := s.generateClientData()
	if err != nil {
		return nil, err
	}

	cfg := &api.Config{
		APIVersion: api.SchemeGroupVersion.Version,
		Clusters: map[string]*api.Cluster{
			clusterName: {
				Server:                fmt.Sprintf("https://%s", s.scope.ControlPlane.Spec.ControlPlaneEndpoint.String()),
				InsecureSkipTLSVerify: true,
				// CertificateAuthorityData: certData,
			},
		},
		Contexts: map[string]*api.Context{
			contextName: {
				Cluster:  clusterName,
				AuthInfo: userName,
			},
		},
		CurrentContext: contextName,
		AuthInfos: map[string]*api.AuthInfo{
			userName: {
				ClientCertificateData: clientCertificateData,
				ClientKeyData:         clientKeyData,
			},
		},
	}

	return cfg, nil
}

func (s *Service) generateClientData() ([]byte, []byte, error) {
	certReq := &ccemodel.CreateKubernetesClusterCertRequest{}
	certReq.ClusterId = s.scope.InfraClusterID()
	certReq.Body = &ccemodel.CertDuration{
		Duration: int32(10 * 365),
	}
	certResp, err := s.CCEClient.CreateKubernetesClusterCert(certReq)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create CCE cluster cert: %w", err)
	}

	clientCertificateData := (*certResp.Users)[0].User.ClientCertificateData
	clientKeyData := (*certResp.Users)[0].User.ClientKeyData

	return []byte(*clientCertificateData), []byte(*clientKeyData), nil
}

func (s *Service) updateCAPIKubeconfigSecret(ctx context.Context, configSecret *corev1.Secret, cluster *ccemodel.ShowClusterResponse) error {
	s.scope.Debug("Updating CCE kubeconfigs for cluster", "cluster-name", s.scope.InfraClusterName())

	data, ok := configSecret.Data[secret.KubeconfigDataName]
	if !ok {
		return errors.Errorf("missing key %q in secret data", secret.KubeconfigDataName)
	}

	config, err := clientcmd.Load(data)
	if err != nil {
		return errors.Wrap(err, "failed to convert kubeconfig Secret into a clientcmdapi.Config")
	}

	clientCertificateData, clientKeyData, err := s.generateClientData()
	if err != nil {
		return err
	}

	userName := s.getKubeConfigUserName(s.scope.InfraClusterName(), false)
	config.AuthInfos[userName].ClientCertificateData = clientCertificateData
	config.AuthInfos[userName].ClientKeyData = clientKeyData

	out, err := clientcmd.Write(*config)
	if err != nil {
		return errors.Wrap(err, "failed to serialize config to yaml")
	}

	configSecret.Data[secret.KubeconfigDataName] = out

	err = s.scope.Client.Update(ctx, configSecret)
	if err != nil {
		return fmt.Errorf("updating kubeconfig secret: %w", err)
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
