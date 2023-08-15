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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/errors"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

const (
	// ManagedMachinePoolFinalizer allows the controller to clean up resources on delete.
	ManagedMachinePoolFinalizer = "ccemanagedmachinepools.infrastructure.cluster.x-k8s.io"

	// InfraMachinePoolIDLabel is the label set on machineool linked to a nodepool
	InfraMachinePoolIDLabel = "cluster.x-k8s.io/infra-machinepool-id"
)

const (
	// CCENodepoolReadyCondition condition reports on the successful reconciliation of cce control plane.
	CCENodepoolReadyCondition clusterv1.ConditionType = "CCENodepoolReady"
	// CCENodepoolReconciliationFailedReason used to report failures while reconciling CCE control plane.
	CCENodepoolReconciliationFailedReason = "CCENodepoolReconciliationFailed"
	// WaitingForCCEControlPlaneReason used when the machine pool is waiting for
	// CCE control plane infrastructure to be ready before proceeding.
	WaitingForCCEControlPlaneReason = "WaitingForCCEControlPlane"
)

// 如下字段不可使用：  - node.kubernetes.io/memory-pressure - node.kubernetes.io/disk-pressure - node.kubernetes.io/out-of-disk - node.kubernetes.io/unschedulable - node.kubernetes.io/network-unavailable
type Taint struct {
	// 键
	Key string `json:"key"`

	// 值
	Value *string `json:"value,omitempty"`

	// 作用效果 NoSchedule,PreferNoSchedule,NoExecute
	Effect string `json:"effect"`
}

type Volume struct {

	// 磁盘大小，单位为GB  - 系统盘取值范围：40~1024 - 数据盘取值范围：100~32768
	Size int32 `json:"size"`

	// 磁盘类型，取值请参见创建云服务器 中“root_volume字段数据结构说明”。  - SAS：高IO，是指由SAS存储提供资源的磁盘类型。 - SSD：超高IO，是指由SSD存储提供资源的磁盘类型。 - SATA：普通IO，是指由SATA存储提供资源的磁盘类型。EVS已下线SATA磁盘，仅存量节点有此类型的磁盘。
	Volumetype string `json:"volumetype"`
}

// CCEManagedMachinePoolSpec defines the desired state of CCEManagedMachinePool
type CCEManagedMachinePoolSpec struct {
	// 操作系统
	Os *string `json:"os,omitempty"`

	// 节点池节点个数
	Replicas *int32 `json:"replicas,omitempty"`

	// 节点的规格
	Flavor *string `json:"flavor"`

	// 容器运行时
	Runtime *string `json:"runtime,omitempty"`

	// 登录密钥
	SSHKeyName *string `json:"sshKeyName,omitempty"`

	// 系统盘
	RootVolume *Volume `json:"rootVolume"`

	// 数据盘
	DataVolumes []Volume `json:"dataVolumes"`

	// Labels specifies labels for the Kubernetes node objects
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// 污点
	Taints *[]Taint `json:"taints,omitempty"`

	// 节点子网 ID
	Subnet *Subnet `json:"subnet,omitempty"`

	// ProviderIDList are the provider IDs of instances in the
	// autoscaling group corresponding to the nodegroup represented by this
	// machine pool
	// +optional
	ProviderIDList []string `json:"providerIDList,omitempty"`
}

// CCEManagedMachinePoolStatus defines the observed state of CCEManagedMachinePool
type CCEManagedMachinePoolStatus struct {
	// Ready denotes that the CCEManagedMachinePool nodegroup has joined
	// the cluster
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// Replicas is the most recently observed number of replicas.
	// +optional
	Replicas int32 `json:"replicas"`

	// FailureReason will be set in the event that there is a terminal problem
	// reconciling the MachinePool and will contain a succinct value suitable
	// for machine interpretation.
	//
	// This field should not be set for transitive errors that a controller
	// faces that are expected to be fixed automatically over
	// time (like service outages), but instead indicate that something is
	// fundamentally wrong with the Machine's spec or the configuration of
	// the controller, and that manual intervention is required. Examples
	// of terminal errors would be invalid combinations of settings in the
	// spec, values that are unsupported by the controller, or the
	// responsible controller itself being critically misconfigured.
	//
	// Any transient errors that occur during the reconciliation of MachinePools
	// can be added as events to the MachinePool object and/or logged in the
	// controller's output.
	// +optional
	FailureReason *errors.MachineStatusError `json:"failureReason,omitempty"`

	// FailureMessage will be set in the event that there is a terminal problem
	// reconciling the MachinePool and will contain a more verbose string suitable
	// for logging and human consumption.
	//
	// This field should not be set for transitive errors that a controller
	// faces that are expected to be fixed automatically over
	// time (like service outages), but instead indicate that something is
	// fundamentally wrong with the MachinePool's spec or the configuration of
	// the controller, and that manual intervention is required. Examples
	// of terminal errors would be invalid combinations of settings in the
	// spec, values that are unsupported by the controller, or the
	// responsible controller itself being critically misconfigured.
	//
	// Any transient errors that occur during the reconciliation of MachinePools
	// can be added as events to the MachinePool object and/or logged in the
	// controller's output.
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`

	// Conditions defines current service state of the managed machine pool
	// +optional
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:path=ccemanagedmachinepools,scope=Namespaced,categories=cluster-api,shortName=ccemmp
//+kubebuilder:storageversion
//+kubebuilder:subresource:status

// CCEManagedMachinePool is the Schema for the ccemanagedmachinepools API
type CCEManagedMachinePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CCEManagedMachinePoolSpec   `json:"spec,omitempty"`
	Status CCEManagedMachinePoolStatus `json:"status,omitempty"`
}

// GetConditions returns the observations of the operational state of the CCEManagedMachinePool resource.
func (r *CCEManagedMachinePool) GetConditions() clusterv1.Conditions {
	return r.Status.Conditions
}

// SetConditions sets the underlying service state of the CCEManagedMachinePool to the predescribed clusterv1.Conditions.
func (r *CCEManagedMachinePool) SetConditions(conditions clusterv1.Conditions) {
	r.Status.Conditions = conditions
}

func (r *CCEManagedMachinePool) InfraMachinePoolID() string {
	if id, ok := r.GetLabels()[InfraMachinePoolIDLabel]; ok {
		return id
	}
	return ""
}

//+kubebuilder:object:root=true

// CCEManagedMachinePoolList contains a list of CCEManagedMachinePool
type CCEManagedMachinePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CCEManagedMachinePool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CCEManagedMachinePool{}, &CCEManagedMachinePoolList{})
}
