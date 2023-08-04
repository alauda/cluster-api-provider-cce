package cce

import (
	"context"
	"encoding/base64"
	"fmt"

	ccemodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/cce/v3/model"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/cluster-api/util/kubeconfig"
	"sigs.k8s.io/cluster-api/util/record"
	"sigs.k8s.io/cluster-api/util/secret"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
)

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
	caData, clientCertificateData, clientKeyData, err := s.generateClientData()
	if err != nil {
		return nil, err
	}

	cfg := &api.Config{
		APIVersion: api.SchemeGroupVersion.Version,
		Clusters: map[string]*api.Cluster{
			clusterName: {
				Server: fmt.Sprintf("https://%s", s.scope.ControlPlane.Spec.ControlPlaneEndpoint.String()),
				//InsecureSkipTLSVerify: true,
				CertificateAuthorityData: caData,
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

func (s *Service) generateClientData() ([]byte, []byte, []byte, error) {
	certReq := &ccemodel.CreateKubernetesClusterCertRequest{}
	certReq.ClusterId = s.scope.InfraClusterID()
	certReq.Body = &ccemodel.CertDuration{
		Duration: int32(10 * 365),
	}
	certResp, err := s.CCEClient.CreateKubernetesClusterCert(certReq)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create CCE cluster cert: %w", err)
	}
	//clusers :=
	var caData []byte
	for _, c := range *certResp.Clusters {
		clusername := "internalCluster"
		if *s.scope.ControlPlane.Spec.EndpointAccess.Public {
			clusername = "externalCluster"
		}
		if *c.Name == clusername {
			caData, err = base64.StdEncoding.DecodeString(*c.Cluster.CertificateAuthorityData)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("decoding cluster client cert: %w", err)
			}
		}

	}

	clientCertificateData := (*certResp.Users)[0].User.ClientCertificateData
	certData, err := base64.StdEncoding.DecodeString(*clientCertificateData)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("decoding cluster client cert: %w", err)
	}
	clientKeyData := (*certResp.Users)[0].User.ClientKeyData
	keyData, err := base64.StdEncoding.DecodeString(*clientKeyData)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("decoding cluster client key: %w", err)
	}
	return caData, certData, keyData, nil
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

	caData, clientCertificateData, clientKeyData, err := s.generateClientData()
	if err != nil {
		return err
	}
	clusterName := s.scope.InfraClusterName()
	config.Clusters[clusterName].Server = fmt.Sprintf("https://%s", s.scope.ControlPlane.Spec.ControlPlaneEndpoint.String())
	config.Clusters[clusterName].CertificateAuthorityData = caData

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
