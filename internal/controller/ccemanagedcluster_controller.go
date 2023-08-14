/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
	"github.com/alauda/cluster-api-provider-cce/pkg/logger"
)

// CCEManagedClusterReconciler reconciles a CCEManagedCluster object
type CCEManagedClusterReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	WatchFilterValue string
	WaitInfraPeriod  time.Duration
}

//+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;patch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;delete;patch
//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinedeployments,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinepools,verbs=get;list;watch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ccemanagedclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ccemanagedclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ccemanagedclusters/finalizers,verbs=update
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ccemanagedmachinepools;ccemanagedmachinepools/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ccemachinepools;ccemachinepools/status,verbs=get;list;watch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ccemanagedcontrolplanes,verbs=get;list;watch;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ccemanagedcontrolplanes/status,verbs=get;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CCEManagedCluster object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.4/pkg/reconcile
func (r *CCEManagedClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// Fetch the CCEManagedCluster instance
	cceManagedCluster := &infrastructurev1beta1.CCEManagedCluster{}
	err := r.Get(ctx, req.NamespacedName, cceManagedCluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// Fetch the Cluster.
	cluster, err := util.GetOwnerCluster(ctx, r.Client, cceManagedCluster.ObjectMeta)
	if err != nil {
		return reconcile.Result{}, err
	}
	if cluster == nil {
		log.Info("Cluster Controller has not yet set OwnerRef")
		return reconcile.Result{}, nil
	}

	if annotations.IsPaused(cluster, cceManagedCluster) {
		log.Info("CCEManagedCluster or linked Cluster is marked as paused. Won't reconcile")
		return reconcile.Result{}, nil
	}

	log = log.WithValues("cluster", cluster.Name)

	controlPlane := &infrastructurev1beta1.CCEManagedControlPlane{}
	controlPlaneRef := types.NamespacedName{
		Name:      cluster.Spec.ControlPlaneRef.Name,
		Namespace: cluster.Spec.ControlPlaneRef.Namespace,
	}

	if err := r.Get(ctx, controlPlaneRef, controlPlane); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get control plane ref: %w", err)
	}

	log = log.WithValues("controlPlane", controlPlaneRef.Name)

	patchHelper, err := patch.NewHelper(cceManagedCluster, r.Client)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to init patch helper: %w", err)
	}

	// Set the values from the managed control plane
	cceManagedCluster.Status.Ready = true
	cceManagedCluster.Spec.ControlPlaneEndpoint = controlPlane.Spec.ControlPlaneEndpoint
	//cceManagedCluster.Status.FailureDomains = controlPlane.Status.FailureDomains

	if err := patchHelper.Patch(ctx, cceManagedCluster); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to patch CCEManagedCluster: %w", err)
	}

	log.Info("Successfully reconciled CCEManagedCluster")

	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CCEManagedClusterReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, options controller.Options) error {
	log := logger.FromContext(ctx)

	cceManagedCluster := &infrastructurev1beta1.CCEManagedCluster{}

	controller, err := ctrl.NewControllerManagedBy(mgr).
		WithOptions(options).
		For(cceManagedCluster).
		WithEventFilter(predicates.ResourceNotPausedAndHasFilterLabel(ctrl.LoggerFrom(ctx), r.WatchFilterValue)).
		Build(r)
	if err != nil {
		return fmt.Errorf("error creating controller: %w", err)
	}

	// Add a watch for clusterv1.Cluster unpaise
	if err = controller.Watch(
		&source.Kind{Type: &clusterv1.Cluster{}},
		handler.EnqueueRequestsFromMapFunc(util.ClusterToInfrastructureMapFunc(ctx, infrastructurev1beta1.GroupVersion.WithKind("CCEManagedCluster"), mgr.GetClient(), &infrastructurev1beta1.CCEManagedCluster{})),
		predicates.ClusterUnpaused(log.GetLogger()),
	); err != nil {
		return fmt.Errorf("failed adding a watch for ready clusters: %w", err)
	}

	// Add a watch for CCEManagedControlPlane
	if err = controller.Watch(
		&source.Kind{Type: &infrastructurev1beta1.CCEManagedControlPlane{}},
		handler.EnqueueRequestsFromMapFunc(r.managedControlPlaneToManagedCluster(ctx, log)),
	); err != nil {
		return fmt.Errorf("failed adding watch on CCEManagedControlPlane: %w", err)
	}

	return nil
}

func (r *CCEManagedClusterReconciler) managedControlPlaneToManagedCluster(ctx context.Context, log *logger.Logger) handler.MapFunc {
	return func(o client.Object) []ctrl.Request {
		cceManagedControlPlane, ok := o.(*infrastructurev1beta1.CCEManagedControlPlane)
		if !ok {
			log.Error(errors.Errorf("expected an CCEManagedControlPlane, got %T instead", o), "failed to map CCEManagedControlPlane")
			return nil
		}

		log := log.WithValues("objectMapper", "ccemcpTomc", "ccemanagedcontrolplane", klog.KRef(cceManagedControlPlane.Namespace, cceManagedControlPlane.Name))

		if !cceManagedControlPlane.ObjectMeta.DeletionTimestamp.IsZero() {
			log.Info("CCEManagedControlPlane has a deletion timestamp, skipping mapping")
			return nil
		}

		if cceManagedControlPlane.Spec.ControlPlaneEndpoint.IsZero() {
			log.Debug("CCEManagedControlPlane has no control plane endpoint, skipping mapping")
			return nil
		}

		cluster, err := util.GetOwnerCluster(ctx, r.Client, cceManagedControlPlane.ObjectMeta)
		if err != nil {
			log.Error(err, "failed to get owning cluster")
			return nil
		}
		if cluster == nil {
			log.Info("no owning cluster, skipping mapping")
			return nil
		}

		managedClusterRef := cluster.Spec.InfrastructureRef
		if managedClusterRef == nil || managedClusterRef.Kind != "CCEManagedCluster" {
			log.Info("InfrastructureRef is nil or not CCEManagedCluster, skipping mapping")
			return nil
		}

		return []ctrl.Request{
			{
				NamespacedName: types.NamespacedName{
					Name:      managedClusterRef.Name,
					Namespace: managedClusterRef.Namespace,
				},
			},
		}
	}
}
