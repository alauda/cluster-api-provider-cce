package scope

import (
	"context"

	"github.com/pkg/errors"
	"k8s.io/klog/v2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	expclusterv1 "sigs.k8s.io/cluster-api/exp/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
	"github.com/alauda/cluster-api-provider-cce/pkg/logger"
)

// ManagedMachinePoolScopeParams defines the input parameters used to create a new Scope.
type ManagedMachinePoolScopeParams struct {
	Client             client.Client
	Logger             *logger.Logger
	Cluster            *clusterv1.Cluster
	ControlPlane       *infrastructurev1beta1.CCEManagedControlPlane
	ManagedMachinePool *infrastructurev1beta1.CCEManagedMachinePool
	MachinePool        *expclusterv1.MachinePool
	ControllerName     string
}

// NewManagedMachinePoolScope creates a new Scope from the supplied parameters.
// This is meant to be called for each reconcile iteration.
func NewManagedMachinePoolScope(params ManagedMachinePoolScopeParams) (*ManagedMachinePoolScope, error) {
	if params.ControlPlane == nil {
		return nil, errors.New("failed to generate new scope from nil CCEManagedMachinePool")
	}
	if params.MachinePool == nil {
		return nil, errors.New("failed to generate new scope from nil MachinePool")
	}
	if params.ManagedMachinePool == nil {
		return nil, errors.New("failed to generate new scope from nil ManagedMachinePool")
	}
	if params.Logger == nil {
		log := klog.Background()
		params.Logger = logger.NewLogger(log)
	}

	ammpHelper, err := patch.NewHelper(params.ManagedMachinePool, params.Client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to init CCEManagedMachinePool patch helper")
	}
	mpHelper, err := patch.NewHelper(params.MachinePool, params.Client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to init MachinePool patch helper")
	}

	return &ManagedMachinePoolScope{
		Logger:                     *params.Logger,
		Client:                     params.Client,
		patchHelper:                ammpHelper,
		capiMachinePoolPatchHelper: mpHelper,
		Cluster:                    params.Cluster,
		ControlPlane:               params.ControlPlane,
		ManagedMachinePool:         params.ManagedMachinePool,
		MachinePool:                params.MachinePool,
		controllerName:             params.ControllerName,
	}, nil
}

// ManagedMachinePoolScope defines the basic context for an actuator to operate upon.
type ManagedMachinePoolScope struct {
	logger.Logger
	client.Client

	patchHelper                *patch.Helper
	capiMachinePoolPatchHelper *patch.Helper

	Cluster            *clusterv1.Cluster
	ControlPlane       *infrastructurev1beta1.CCEManagedControlPlane
	ManagedMachinePool *infrastructurev1beta1.CCEManagedMachinePool
	MachinePool        *expclusterv1.MachinePool

	controllerName string
}

// PatchObject persists the control plane configuration and status.
func (s *ManagedMachinePoolScope) PatchObject() error {
	return s.patchHelper.Patch(
		context.TODO(),
		s.ManagedMachinePool,
		patch.WithOwnedConditions{Conditions: []clusterv1.ConditionType{
			infrastructurev1beta1.CCENodepoolReadyCondition,
		}})
}

// Close closes the current scope persisting the control plane configuration and status.
func (s *ManagedMachinePoolScope) Close() error {
	return s.PatchObject()
}

func (s *ManagedMachinePoolScope) Name() string {
	return s.ManagedMachinePool.Name
}

func (s *ManagedMachinePoolScope) Namespace() string {
	return s.ManagedMachinePool.Namespace
}

func (s *ManagedMachinePoolScope) InfraMachinePoolID() string {
	if id, ok := s.ManagedMachinePool.GetLabels()[infrastructurev1beta1.InfraMachinePoolIDLabel]; ok {
		return id
	}
	return ""
}

func (s *ManagedMachinePoolScope) SetInfraMachinePoolID(nodepoolID string) {
	lbs := s.ManagedMachinePool.GetLabels()
	if lbs == nil {
		lbs = map[string]string{}
	}
	lbs[infrastructurev1beta1.InfraMachinePoolIDLabel] = nodepoolID
	s.ManagedMachinePool.SetLabels(lbs)
}

// NodepoolReadyFalse marks the ready condition false using warning if error isn't empty.
func (s *ManagedMachinePoolScope) NodepoolReadyFalse(reason string, err string) error {
	severity := clusterv1.ConditionSeverityWarning
	if err == "" {
		severity = clusterv1.ConditionSeverityInfo
	}
	conditions.MarkFalse(
		s.ManagedMachinePool,
		infrastructurev1beta1.CCENodepoolReadyCondition,
		reason,
		severity,
		err,
	)
	if err := s.PatchObject(); err != nil {
		return errors.Wrap(err, "failed to mark nodepool not ready")
	}
	return nil
}
