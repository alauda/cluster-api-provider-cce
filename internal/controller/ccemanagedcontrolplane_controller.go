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
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
	"github.com/alauda/cluster-api-provider-cce/pkg/cloud/scope"
	"github.com/alauda/cluster-api-provider-cce/pkg/cloud/services/cce"
	"github.com/alauda/cluster-api-provider-cce/pkg/logger"
	capiannotations "sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/predicates"
)

const (
	// deleteRequeueAfter is how long to wait before checking again to see if the control plane still
	// has dependencies during deletion.
	deleteRequeueAfter = 20 * time.Second

	cceManagedControlPlaneKind = "CCEManagedControlPlane"
)

// CCEManagedControlPlaneReconciler reconciles a CCEManagedControlPlane object
type CCEManagedControlPlaneReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	WatchFilterValue string
	WaitInfraPeriod  time.Duration
}

//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ccemanagedcontrolplanes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ccemanagedcontrolplanes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ccemanagedcontrolplanes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CCEManagedControlPlane object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.4/pkg/reconcile
func (r *CCEManagedControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, reterr error) {
	log := logger.FromContext(ctx)

	// Get the control plane instance
	cceControlPlane := &infrastructurev1beta1.CCEManagedControlPlane{}
	if err := r.Client.Get(ctx, req.NamespacedName, cceControlPlane); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Get the cluster
	cluster, err := util.GetOwnerCluster(ctx, r.Client, cceControlPlane.ObjectMeta)
	if err != nil {
		log.Error(err, "Failed to retrieve owner Cluster from the API Server")
		return ctrl.Result{}, err
	}
	if cluster == nil {
		log.Info("Cluster Controller has not yet set OwnerRef")
		return ctrl.Result{}, nil
	}

	if capiannotations.IsPaused(cluster, cceControlPlane) {
		log.Info("Reconciliation is paused for this object")
		return ctrl.Result{}, nil
	}

	managedScope, err := scope.NewManagedControlPlaneScope(scope.ManagedControlPlaneScopeParams{
		Client:         r.Client,
		Cluster:        cluster,
		ControlPlane:   cceControlPlane,
		ControllerName: strings.ToLower(cceManagedControlPlaneKind),
	})
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to create scope: %w", err)
	}

	// Always close the scope
	defer func() {
		if err := managedScope.Close(); err != nil && reterr == nil {
			reterr = err
		}
	}()

	if !cceControlPlane.ObjectMeta.DeletionTimestamp.IsZero() {
		// Handle deletion reconciliation loop.
		return r.reconcileDelete(ctx, managedScope)
	}

	// Handle normal reconciliation loop.
	return r.reconcileNormal(ctx, managedScope)
}

// SetupWithManager sets up the controller with the Manager.
func (r *CCEManagedControlPlaneReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, options controller.Options) error {
	log := logger.FromContext(ctx)

	cceManagedControlPlane := &infrastructurev1beta1.CCEManagedControlPlane{}
	c, err := ctrl.NewControllerManagedBy(mgr).
		For(cceManagedControlPlane).
		WithOptions(options).
		WithEventFilter(predicates.ResourceNotPausedAndHasFilterLabel(log.GetLogger(), r.WatchFilterValue)).
		Build(r)

	if err != nil {
		return fmt.Errorf("failed setting up the CCEManagedControlPlane controller manager: %w", err)
	}

	if err = c.Watch(
		&source.Kind{Type: &clusterv1.Cluster{}},
		handler.EnqueueRequestsFromMapFunc(util.ClusterToInfrastructureMapFunc(ctx, cceManagedControlPlane.GroupVersionKind(), mgr.GetClient(), &infrastructurev1beta1.CCEManagedControlPlane{})),
		predicates.ClusterUnpausedAndInfrastructureReady(log.GetLogger()),
	); err != nil {
		return fmt.Errorf("failed adding a watch for ready clusters: %w", err)
	}

	if err = c.Watch(
		&source.Kind{Type: &infrastructurev1beta1.CCEManagedCluster{}},
		handler.EnqueueRequestsFromMapFunc(r.managedClusterToManagedControlPlane(ctx, log)),
	); err != nil {
		return fmt.Errorf("failed adding a watch for AWSManagedCluster")
	}

	return nil
}

func (r *CCEManagedControlPlaneReconciler) managedClusterToManagedControlPlane(ctx context.Context, log *logger.Logger) handler.MapFunc {
	return func(o client.Object) []ctrl.Request {
		cceManagedCluster, ok := o.(*infrastructurev1beta1.CCEManagedCluster)
		if !ok {
			log.Error(fmt.Errorf("expected a CCEManagedCluster but got a %T", o), "Expected AWSManagedCluster")
			return nil
		}

		if !cceManagedCluster.ObjectMeta.DeletionTimestamp.IsZero() {
			log.Debug("CCEManagedCluster has a deletion timestamp, skipping mapping")
			return nil
		}

		cluster, err := util.GetOwnerCluster(ctx, r.Client, cceManagedCluster.ObjectMeta)
		if err != nil {
			log.Error(err, "failed to get owning cluster")
			return nil
		}
		if cluster == nil {
			log.Debug("Owning cluster not set on CCEManagedCluster, skipping mapping")
			return nil
		}

		controlPlaneRef := cluster.Spec.ControlPlaneRef
		if controlPlaneRef == nil || controlPlaneRef.Kind != cceManagedControlPlaneKind {
			log.Debug("ControlPlaneRef is nil or not CCEManagedControlPlane, skipping mapping")
			return nil
		}

		return []ctrl.Request{
			{
				NamespacedName: types.NamespacedName{
					Name:      controlPlaneRef.Name,
					Namespace: controlPlaneRef.Namespace,
				},
			},
		}
	}
}

func (r *CCEManagedControlPlaneReconciler) reconcileDelete(ctx context.Context, managedScope *scope.ManagedControlPlaneScope) (_ ctrl.Result, reterr error) {
	managedScope.Info("Reconciling CCEManagedControlPlane delete")

	return reconcile.Result{}, nil
}

func (r *CCEManagedControlPlaneReconciler) reconcileNormal(ctx context.Context, managedScope *scope.ManagedControlPlaneScope) (res ctrl.Result, reterr error) {
	managedScope.Info("Reconciling CCEManagedControlPlane")

	if managedScope.Cluster.Spec.InfrastructureRef.Kind != cceManagedControlPlaneKind {
		// Wait for the cluster infrastructure to be ready before creating machines
		if !managedScope.Cluster.Status.InfrastructureReady {
			managedScope.Info("Cluster infrastructure is not ready yet")
			return ctrl.Result{RequeueAfter: r.WaitInfraPeriod}, nil
		}
	}

	cceManagedControlPlane := managedScope.ControlPlane

	if controllerutil.AddFinalizer(managedScope.ControlPlane, infrastructurev1beta1.ManagedControlPlaneFinalizer) {
		if err := managedScope.PatchObject(); err != nil {
			return ctrl.Result{}, err
		}
	}

	// service init
	ccesvc, err := cce.NewService(managedScope)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to reconcile control plane for CCEManagedControlPlane %s/%s: %w", cceManagedControlPlane.Namespace, cceManagedControlPlane.Name, err)
	}
	if err := ccesvc.ReconcileControlPlane(ctx); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to reconcile control plane for CCEManagedControlPlane %s/%s: %w", cceManagedControlPlane.Namespace, cceManagedControlPlane.Name, err)
	}

	return reconcile.Result{}, nil
}
