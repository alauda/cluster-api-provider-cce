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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	// CCEManagedControlPlaneKind is the Kind of CCEManagedControlPlane.
	CCEManagedControlPlaneKind = "CCEManagedControlPlane"

	// ManagedControlPlaneFinalizer allows the controller to clean up resources on delete.
	ManagedControlPlaneFinalizer = "ccemanagedcontrolplane.infrastructure.cluster.x-k8s.io"

	// InfraClusterIDLabel is the label set on controlplane linked to a cluster
	InfraClusterIDLabel = "cluster.x-k8s.io/infra-cluster-id"
)

const (
	// CCEControlPlaneReadyCondition condition reports on the successful reconciliation of cce control plane.
	CCEControlPlaneReadyCondition clusterv1.ConditionType = "CCEControlPlaneReady"
	// CCEControlPlaneCreatingCondition condition reports on whether the cce
	// control plane is creating.
	CCEControlPlaneCreatingCondition clusterv1.ConditionType = "CCEControlPlaneCreating"
	// CCEControlPlaneUpdatingCondition condition reports on whether the cce
	// control plane is updating.
	CCEControlPlaneUpdatingCondition clusterv1.ConditionType = "CCEControlPlaneUpdating"
	// CCEControlPlaneReconciliationFailedReason used to report failures while reconciling CCE control plane.
	CCEControlPlaneReconciliationFailedReason = "CCEControlPlaneReconciliationFailed"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// 节点网络参数, 包含了虚拟私有云VPC和子网的ID信息。
type HostNetwork struct {

	// 用于创建控制节点的VPC的ID
	Vpc string `json:"vpc"`

	// 用于创建控制节点的subnet的网络ID
	Subnet string `json:"subnet"`
}

// 容器网络参数，包含了容器网络类型和容器网段的信息。
type ContainerNetwork struct {

	// 容器网络类型，overlay_l2 - 容器隧道网络，vpc-router - VPC网络，eni - 云原生网络2.0
	Mode string `json:"mode"`

	// 容器网络网段列表
	Cidrs *[]string `json:"cidrs,omitempty"`
}

type ClusterEndpoints struct {

	// 集群中 kube-apiserver 的访问地址
	Url *string `json:"url,omitempty"`

	// 集群访问地址的类型 - Internal：用户子网内访问的地址 - External：公网访问的地址
	Type *string `json:"type,omitempty"`
}

// 服务网段
type ServiceNetwork struct {
	IPv4CIDR string `json:"IPv4CIDR"`
}

// CCEManagedControlPlaneSpec defines the desired state of CCEManagedControlPlane
type CCEManagedControlPlaneSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// 身份引用
	IdentityRef *corev1.ObjectReference `json:"identityRef,omitempty"`

	// 项目ID
	Project string `json:"project,omitempty"`

	// 地域
	Region string `json:"region,omitempty"`

	// 集群名称
	ClusterName *string `json:"clusterName,omitempty"`

	// 集群规格
	// cce.s1.small: 小规模单控制节点CCE集群（最大50节点）
	// cce.s1.medium: 中等规模单控制节点CCE集群（最大200节点）
	// cce.s2.small: 小规模多控制节点CCE集群（最大50节点）
	// cce.s2.medium: 中等规模多控制节点CCE集群（最大200节点）
	// cce.s2.large: 大规模多控制节点CCE集群（最大1000节点）
	// cce.s2.xlarge: 超大规模多控制节点CCE集群（最大2000节点）
	Flavor *string `json:"flavor,omitempty"`

	// 节点网络参数
	HostNetwork *HostNetwork `json:"hostNetwork,omitempty"`

	// 容器网络参数
	ContainerNetwork *ContainerNetwork `json:"containerNetwork,omitempty"`

	// Kubernetes版本
	Version *string `json:"version,omitempty"`

	// 绑定公网
	BindPublicNetwork *bool `json:"bindPublicNetwork,omitempty"`

	// Service 网段
	ServiceNetwork *ServiceNetwork `json:"serviceNetwork,omitempty"`

	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint"`
}

// CCEManagedControlPlaneStatus defines the observed state of CCEManagedControlPlane
type CCEManagedControlPlaneStatus struct {
	// ExternalManagedControlPlane indicates to cluster-api that the control plane
	// is managed by an external service such as AKS, EKS, GKE, etc.
	// +kubebuilder:default=true
	ExternalManagedControlPlane *bool `json:"externalManagedControlPlane,omitempty"`
	// Initialized denotes whether or not the control plane has the
	// uploaded kubernetes config-map.
	// +optional
	Initialized bool `json:"initialized"`
	// Ready denotes that the CCEManagedControlPlane API Server is ready to
	// receive requests and that the VPC infra is ready.
	// +kubebuilder:default=false
	Ready bool `json:"ready"`
	// ErrorMessage indicates that there is a terminal problem reconciling the
	// state, and will be set to a descriptive error message.
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`
	// Conditions specifies the cpnditions for the managed control plane
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`

	// 集群中 kube-apiserver 的访问地址。
	Endpoints *[]ClusterEndpoints `json:"endpoints,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:path=ccemanagedcontrolplanes,scope=Namespaced,categories=cluster-api,shortName=ccemcp
//+kubebuilder:storageversion
//+kubebuilder:subresource:status

// CCEManagedControlPlane is the Schema for the ccemanagedcontrolplanes API
type CCEManagedControlPlane struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CCEManagedControlPlaneSpec   `json:"spec,omitempty"`
	Status CCEManagedControlPlaneStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// CCEManagedControlPlaneList contains a list of CCEManagedControlPlane
type CCEManagedControlPlaneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CCEManagedControlPlane `json:"items"`
}

// GetConditions returns the control planes conditions.
func (r *CCEManagedControlPlane) GetConditions() clusterv1.Conditions {
	return r.Status.Conditions
}

// SetConditions sets the status conditions for the CCEManagedControlPlane.
func (r *CCEManagedControlPlane) SetConditions(conditions clusterv1.Conditions) {
	r.Status.Conditions = conditions
}

func init() {
	SchemeBuilder.Register(&CCEManagedControlPlane{}, &CCEManagedControlPlaneList{})
}
