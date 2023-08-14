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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	expclusterv1 "sigs.k8s.io/cluster-api/exp/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
	"github.com/alauda/cluster-api-provider-cce/pkg/cloud/scope"
	"github.com/alauda/cluster-api-provider-cce/pkg/cloud/services/cce"
	"github.com/alauda/cluster-api-provider-cce/pkg/logger"
)

// CCEManagedMachinePoolReconciler reconciles a CCEManagedMachinePool object
type CCEManagedMachinePoolReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	WatchFilterValue string
	WaitInfraPeriod  time.Duration
}

//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ccemanagedmachinepools,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ccemanagedmachinepools/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=ccemanagedmachinepools/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CCEManagedMachinePool object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.4/pkg/reconcile
func (r *CCEManagedMachinePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	log := logger.FromContext(ctx)

	ccePool := &infrastructurev1beta1.CCEManagedMachinePool{}
	if err := r.Get(ctx, req.NamespacedName, ccePool); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{Requeue: true}, nil
	}

	machinePool, err := getOwnerMachinePool(ctx, r.Client, ccePool.ObjectMeta)
	if err != nil {
		log.Error(err, "Failed to retrieve owner MachinePool from the API Server")
		return ctrl.Result{}, err
	}
	if machinePool == nil {
		log.Info("MachinePool Controller has not yet set OwnerRef")
		return ctrl.Result{}, nil
	}

	log = log.WithValues("machinePool", klog.KObj(machinePool))

	// Fetch the Cluster.
	cluster, err := util.GetClusterFromMetadata(ctx, r.Client, machinePool.ObjectMeta)
	if err != nil {
		log.Info("Failed to retrieve Cluster from MachinePool")
		return reconcile.Result{}, nil
	}

	if annotations.IsPaused(cluster, ccePool) {
		log.Info("Reconciliation is paused for this object")
		return ctrl.Result{}, nil
	}

	log = log.WithValues("cluster", klog.KObj(cluster))

	controlPlaneKey := client.ObjectKey{
		Namespace: ccePool.Namespace,
		Name:      cluster.Spec.ControlPlaneRef.Name,
	}
	controlPlane := &infrastructurev1beta1.CCEManagedControlPlane{}
	if err := r.Client.Get(ctx, controlPlaneKey, controlPlane); err != nil {
		log.Info("Failed to retrieve ControlPlane from MachinePool")
		return reconcile.Result{}, nil
	}

	if !controlPlane.Status.Ready {
		log.Info("Control plane is not ready yet")
		conditions.MarkFalse(ccePool, infrastructurev1beta1.CCENodepoolReadyCondition, infrastructurev1beta1.WaitingForCCEControlPlaneReason, clusterv1.ConditionSeverityInfo, "")
		return ctrl.Result{}, nil
	}

	machinePoolScope, err := scope.NewManagedMachinePoolScope(scope.ManagedMachinePoolScopeParams{
		Client:             r.Client,
		ControllerName:     "ccemanagedmachinepool",
		Cluster:            cluster,
		ControlPlane:       controlPlane,
		MachinePool:        machinePool,
		ManagedMachinePool: ccePool,
	})
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to create scope")
	}

	defer func() {
		applicableConditions := []clusterv1.ConditionType{
			infrastructurev1beta1.CCENodepoolReadyCondition,
		}

		conditions.SetSummary(machinePoolScope.ManagedMachinePool, conditions.WithConditions(applicableConditions...), conditions.WithStepCounter())

		if err := machinePoolScope.Close(); err != nil && reterr == nil {
			reterr = err
		}
	}()

	if !ccePool.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, machinePoolScope)
	}

	return r.reconcileNormal(ctx, machinePoolScope)
}

// SetupWithManager sets up the controller with the Manager.
func (r *CCEManagedMachinePoolReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, options controller.Options) error {
	log := logger.FromContext(ctx)

	gvk, err := apiutil.GVKForObject(new(infrastructurev1beta1.CCEManagedMachinePool), mgr.GetScheme())
	if err != nil {
		return errors.Wrapf(err, "failed to find GVK for CCEManagedMachinePool")
	}

	managedControlPlaneToManagedMachinePoolMap := managedControlPlaneToManagedMachinePoolMapFunc(r.Client, gvk, log)
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrastructurev1beta1.CCEManagedMachinePool{}).
		WithOptions(options).
		WithEventFilter(predicates.ResourceNotPausedAndHasFilterLabel(log.GetLogger(), r.WatchFilterValue)).
		Watches(
			&source.Kind{Type: &expclusterv1.MachinePool{}},
			handler.EnqueueRequestsFromMapFunc(machinePoolToInfrastructureMapFunc(gvk)),
		).
		Watches(
			&source.Kind{Type: &infrastructurev1beta1.CCEManagedControlPlane{}},
			handler.EnqueueRequestsFromMapFunc(managedControlPlaneToManagedMachinePoolMap),
		).
		Complete(r)
}

func (r *CCEManagedMachinePoolReconciler) reconcileDelete(_ context.Context, machinePoolScope *scope.ManagedMachinePoolScope) (ctrl.Result, error) {
	machinePoolScope.Info("Reconciling deletion of CCEManagedMachinePool")

	ccesvc, err := cce.NewNodepoolService(machinePoolScope)
	if err != nil {
		time.Sleep(5 * time.Second)
		return reconcile.Result{}, fmt.Errorf("failed to init cce service for CCEManagedMachinePool %s/%s: %w", machinePoolScope.Namespace(), machinePoolScope.Name(), err)
	}

	if err := ccesvc.ReconcilePoolDelete(); err != nil {
		time.Sleep(r.WaitInfraPeriod)
		return reconcile.Result{}, errors.Wrapf(err, "failed to reconcile machine pool deletion for CCEManagedMachinePool %s/%s", machinePoolScope.ManagedMachinePool.Namespace, machinePoolScope.ManagedMachinePool.Name)
	}

	controllerutil.RemoveFinalizer(machinePoolScope.ManagedMachinePool, infrastructurev1beta1.ManagedMachinePoolFinalizer)
	return ctrl.Result{}, nil
}

func (r *CCEManagedMachinePoolReconciler) reconcileNormal(ctx context.Context, machinePoolScope *scope.ManagedMachinePoolScope) (ctrl.Result, error) {
	machinePoolScope.Info("Reconciling CCEManagedMachinePool")

	if controllerutil.AddFinalizer(machinePoolScope.ManagedMachinePool, infrastructurev1beta1.ManagedMachinePoolFinalizer) {
		if err := machinePoolScope.PatchObject(); err != nil {
			return ctrl.Result{}, err
		}
	}

	// service init
	ccesvc, err := cce.NewNodepoolService(machinePoolScope)
	if err != nil {
		time.Sleep(5 * time.Second)
		return reconcile.Result{}, fmt.Errorf("failed to init cce service for CCEManagedMachinePool %s/%s: %w", machinePoolScope.Namespace(), machinePoolScope.Name(), err)
	}

	if err := ccesvc.ReconcilePool(ctx); err != nil {
		time.Sleep(r.WaitInfraPeriod)
		return reconcile.Result{}, errors.Wrapf(err, "failed to reconcile machine pool for CCEManagedMachinePool %s/%s", machinePoolScope.ManagedMachinePool.Namespace, machinePoolScope.ManagedMachinePool.Name)
	}

	return ctrl.Result{}, nil
}

func managedControlPlaneToManagedMachinePoolMapFunc(c client.Client, gvk schema.GroupVersionKind, log logger.Wrapper) handler.MapFunc {
	return func(o client.Object) []reconcile.Request {
		ctx := context.Background()
		cceControlPlane, ok := o.(*infrastructurev1beta1.CCEManagedControlPlane)
		if !ok {
			klog.Errorf("Expected a CCEManagedControlPlane but got a %T", o)
		}

		if !cceControlPlane.ObjectMeta.DeletionTimestamp.IsZero() {
			return nil
		}

		clusterKey, err := GetOwnerClusterKey(cceControlPlane.ObjectMeta)
		if err != nil {
			log.Error(err, "couldn't get CCE control plane owner ObjectKey")
			return nil
		}
		if clusterKey == nil {
			return nil
		}

		managedPoolForClusterList := expclusterv1.MachinePoolList{}
		if err := c.List(
			ctx, &managedPoolForClusterList, client.InNamespace(clusterKey.Namespace), client.MatchingLabels{clusterv1.ClusterNameLabel: clusterKey.Name},
		); err != nil {
			log.Error(err, "couldn't list pools for cluster")
			return nil
		}

		mapFunc := machinePoolToInfrastructureMapFunc(gvk)

		var results []ctrl.Request
		for i := range managedPoolForClusterList.Items {
			managedPool := mapFunc(&managedPoolForClusterList.Items[i])
			results = append(results, managedPool...)
		}

		return results
	}
}

// GetOwnerClusterKey returns only the Cluster name and namespace.
func GetOwnerClusterKey(obj metav1.ObjectMeta) (*client.ObjectKey, error) {
	for _, ref := range obj.OwnerReferences {
		if ref.Kind != "Cluster" {
			continue
		}
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil {
			return nil, errors.WithStack(err)
		}
		if gv.Group == clusterv1.GroupVersion.Group {
			return &client.ObjectKey{
				Namespace: obj.Namespace,
				Name:      ref.Name,
			}, nil
		}
	}
	return nil, nil
}

func machinePoolToInfrastructureMapFunc(gvk schema.GroupVersionKind) handler.MapFunc {
	return func(o client.Object) []reconcile.Request {
		m, ok := o.(*expclusterv1.MachinePool)
		if !ok {
			klog.Error("Expected a MachinePool but got a %T", o)
		}

		gk := gvk.GroupKind()
		// Return early if the GroupKind doesn't match what we expect
		infraGK := m.Spec.Template.Spec.InfrastructureRef.GroupVersionKind().GroupKind()
		if gk != infraGK {
			return nil
		}

		return []reconcile.Request{
			{
				NamespacedName: client.ObjectKey{
					Namespace: m.Namespace,
					Name:      m.Spec.Template.Spec.InfrastructureRef.Name,
				},
			},
		}
	}
}

// getOwnerMachinePool returns the MachinePool object owning the current resource.
func getOwnerMachinePool(ctx context.Context, c client.Client, obj metav1.ObjectMeta) (*expclusterv1.MachinePool, error) {
	for _, ref := range obj.OwnerReferences {
		if ref.Kind != "MachinePool" {
			continue
		}
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil {
			return nil, errors.WithStack(err)
		}
		if gv.Group == expclusterv1.GroupVersion.Group {
			return getMachinePoolByName(ctx, c, obj.Namespace, ref.Name)
		}
	}
	return nil, nil
}

// getMachinePoolByName finds and return a Machine object using the specified params.
func getMachinePoolByName(ctx context.Context, c client.Client, namespace, name string) (*expclusterv1.MachinePool, error) {
	m := &expclusterv1.MachinePool{}
	key := client.ObjectKey{Name: name, Namespace: namespace}
	if err := c.Get(ctx, key, m); err != nil {
		return nil, err
	}
	return m, nil
}
