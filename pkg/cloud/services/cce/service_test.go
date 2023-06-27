package cce

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
	"github.com/alauda/cluster-api-provider-cce/pkg/cloud/scope"
)

func TestReconcileCluster(t *testing.T) {
	cceControlPlane := &infrastructurev1beta1.CCEManagedControlPlane{
		Spec: infrastructurev1beta1.CCEManagedControlPlaneSpec{
			IdentityRef: &corev1.ObjectReference{
				Kind:      "Secret",
				Namespace: "cpaas-system",
				Name:      "abc",
			},
		},
	}
	cluster := &clusterv1.Cluster{}

	managedScope, err := scope.NewManagedControlPlaneScope(scope.ManagedControlPlaneScopeParams{
		Client:         k8sClient,
		Cluster:        cluster,
		ControlPlane:   cceControlPlane,
		ControllerName: strings.ToLower("CCEManagedControlPlane"),
	})
	if err != nil {
		t.Fatalf("NewManagedControlPlaneScope err:%v", err)
	}

	_, err = NewService(managedScope)
	if err != nil {
		t.Fatalf("NewService err:%v", err)
	}

}
