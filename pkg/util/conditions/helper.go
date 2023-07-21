package conditions

import (
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
)

// ErrorConditionAfterInit returns severity error, if the control plane is initialized; otherwise, returns severity warning.
// Failures after control plane is initialized is likely to be non-transient,
// hence conditions severities should be set to Error.
func ErrorConditionAfterInit(getter conditions.Getter) clusterv1.ConditionSeverity {
	if conditions.IsTrue(getter, clusterv1.ControlPlaneInitializedCondition) {
		return clusterv1.ConditionSeverityError
	}
	return clusterv1.ConditionSeverityWarning
}
