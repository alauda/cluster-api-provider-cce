package scope

import (
	"context"

	"github.com/pkg/errors"
	"k8s.io/klog/v2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
	"github.com/alauda/cluster-api-provider-cce/pkg/logger"
)

// ManagedControlPlaneScopeParams defines the input parameters used to create a new Scope.
type ManagedControlPlaneScopeParams struct {
	Client         client.Client
	Logger         *logger.Logger
	Cluster        *clusterv1.Cluster
	ControlPlane   *infrastructurev1beta1.CCEManagedControlPlane
	ControllerName string
}

// NewManagedControlPlaneScope creates a new Scope from the supplied parameters.
// This is meant to be called for each reconcile iteration.
func NewManagedControlPlaneScope(params ManagedControlPlaneScopeParams) (*ManagedControlPlaneScope, error) {
	if params.Cluster == nil {
		return nil, errors.New("failed to generate new scope from nil Cluster")
	}
	if params.ControlPlane == nil {
		return nil, errors.New("failed to generate new scope from nil CCEManagedControlPlane")
	}
	if params.Logger == nil {
		log := klog.Background()
		params.Logger = logger.NewLogger(log)
	}

	managedScope := &ManagedControlPlaneScope{
		Logger:         *params.Logger,
		Client:         params.Client,
		Cluster:        params.Cluster,
		ControlPlane:   params.ControlPlane,
		patchHelper:    nil,
		controllerName: params.ControllerName,
	}

	helper, err := patch.NewHelper(params.ControlPlane, params.Client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to init patch helper")
	}

	managedScope.patchHelper = helper
	return managedScope, nil
}

// ManagedControlPlaneScope defines the basic context for an actuator to operate upon.
type ManagedControlPlaneScope struct {
	logger.Logger
	Client      client.Client
	patchHelper *patch.Helper

	Cluster      *clusterv1.Cluster
	ControlPlane *infrastructurev1beta1.CCEManagedControlPlane

	controllerName string
}

// PatchObject persists the control plane configuration and status.
func (s *ManagedControlPlaneScope) PatchObject() error {
	return s.patchHelper.Patch(
		context.TODO(),
		s.ControlPlane,
		patch.WithOwnedConditions{Conditions: []clusterv1.ConditionType{
			infrastructurev1beta1.VpcReadyCondition,
			infrastructurev1beta1.SubnetsReadyCondition,
			infrastructurev1beta1.EIPReadyCondition,
			infrastructurev1beta1.CCEControlPlaneCreatingCondition,
			infrastructurev1beta1.CCEControlPlaneReadyCondition,
			infrastructurev1beta1.CCEControlPlaneUpdatingCondition,
		}})
}

// Close closes the current scope persisting the control plane configuration and status.
func (s *ManagedControlPlaneScope) Close() error {
	return s.PatchObject()
}

func (s *ManagedControlPlaneScope) InfraClusterID() string {
	if id, ok := s.ControlPlane.GetLabels()[infrastructurev1beta1.InfraClusterIDLabel]; ok {
		return id
	}
	return ""
}

func (s *ManagedControlPlaneScope) SetInfraClusterID(clusterID string) {
	lbs := s.ControlPlane.GetLabels()
	if lbs == nil {
		lbs = map[string]string{}
	}
	lbs[infrastructurev1beta1.InfraClusterIDLabel] = clusterID
	s.ControlPlane.SetLabels(lbs)
}

// Name returns the CAPI cluster name.
func (s *ManagedControlPlaneScope) Name() string {
	return s.Cluster.Name
}

// Namespace returns the cluster namespace.
func (s *ManagedControlPlaneScope) Namespace() string {
	return s.Cluster.Namespace
}

// InfraClusterName returns the CCE cluster name.
func (s *ManagedControlPlaneScope) InfraClusterName() string {
	return s.ControlPlane.Name
}

// Region returns the cluster region.
func (s *ManagedControlPlaneScope) Region() string {
	return s.ControlPlane.Spec.Region
}

// Region returns the cluster region.
func (s *ManagedControlPlaneScope) Project() string {
	if s.ControlPlane.Spec.Project == nil {
		return "0"
	}
	return *s.ControlPlane.Spec.Project
}
