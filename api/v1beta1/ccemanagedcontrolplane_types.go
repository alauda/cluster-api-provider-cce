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

const (
	// VpcReadyCondition reports on the successful reconciliation of a VPC.
	VpcReadyCondition clusterv1.ConditionType = "VpcReady"
	// VpcCreationStartedReason used when attempting to create a VPC for a managed cluster.
	// Will not be applied to unmanaged clusters.
	VpcCreationStartedReason = "VpcCreationStarted"
	// VpcReconciliationFailedReason used when errors occur during VPC reconciliation.
	VpcReconciliationFailedReason = "VpcReconciliationFailed"
)

const (
	// SubnetsReadyCondition reports on the successful reconciliation of subnets.
	SubnetsReadyCondition clusterv1.ConditionType = "SubnetsReady"
	// SubnetsReconciliationFailedReason used to report failures while reconciling subnets.
	SubnetsReconciliationFailedReason = "SubnetsReconciliationFailed"
)

const (
	// Only applicable to managed clusters.
	EIPReadyCondition clusterv1.ConditionType = "EIPReady"
	EIPFailedReason                           = "EIPFailed"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type VPC struct {
	ID string `json:"id,omitempty"`
}

type Subnet struct {
	ID string `json:"id,omitempty"`
}

type EIP struct {
	ID string `json:"id,omitempty"`
}

// NetworkSpec encapsulates all things related to CCE network.
type NetworkSpec struct {
	// 网络模型，overlay_l2 - 容器隧道网络，vpc-router - VPC网络，eni - 云原生网络2.0
	Mode *string `json:"mode"`

	// VPC configuration.
	VPC VPC `json:"vpc,omitempty"`

	// Subnet configuration.
	Subnet Subnet `json:"subnet,omitempty"`
}

// NetworkStatus encapsulates CCE networking resources.
type NetworkStatus struct {
	// EIP configuration
	EIP EIP `json:"eip,omitempty"`
}

// EndpointAccess specifies how control plane endpoints are accessible.
type EndpointAccess struct {
	// Public controls whether control plane endpoints are publicly accessible
	// +optional
	Public *bool `json:"public,omitempty"`
}

// CCEManagedControlPlaneSpec defines the desired state of CCEManagedControlPlane
type CCEManagedControlPlaneSpec struct {
	// 身份引用
	IdentityRef *corev1.ObjectReference `json:"identityRef,omitempty"`

	// 项目ID
	Project *string `json:"project,omitempty"`

	// 地域
	Region string `json:"region,omitempty"`

	// Kubernetes版本
	Version *string `json:"version,omitempty"`

	// 集群规格
	// cce.s1.small: 小规模单控制节点CCE集群（最大50节点）
	// cce.s1.medium: 中等规模单控制节点CCE集群（最大200节点）
	// cce.s2.small: 小规模多控制节点CCE集群（最大50节点）
	// cce.s2.medium: 中等规模多控制节点CCE集群（最大200节点）
	// cce.s2.large: 大规模多控制节点CCE集群（最大1000节点）
	// cce.s2.xlarge: 超大规模多控制节点CCE集群（最大2000节点）
	Flavor *string `json:"flavor,omitempty"`

	// 节点网络参数
	NetworkSpec NetworkSpec `json:"network,omitempty"`

	// Endpoints specifies access to this cluster's control plane endpoints
	// +optional
	EndpointAccess *EndpointAccess `json:"endpointAccess,omitempty"`

	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint"`
}

// CCEManagedControlPlaneStatus defines the observed state of CCEManagedControlPlane
type CCEManagedControlPlaneStatus struct {
	// Networks holds details about the CCE networking resources used by the control plane
	// +optional
	Network NetworkStatus `json:"networkStatus,omitempty"`
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

func (r *CCEManagedControlPlane) InfraClusterID() string {
	if id, ok := r.GetLabels()[InfraClusterIDLabel]; ok {
		return id
	}
	return ""
}

func init() {
	SchemeBuilder.Register(&CCEManagedControlPlane{}, &CCEManagedControlPlaneList{})
}
