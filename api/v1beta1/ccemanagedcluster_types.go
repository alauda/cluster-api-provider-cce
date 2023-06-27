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
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// CCEManagedClusterSpec defines the desired state of CCEManagedCluster
type CCEManagedClusterSpec struct {
	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint"`
}

// CCEManagedClusterStatus defines the observed state of CCEManagedCluster
type CCEManagedClusterStatus struct {
	// Ready is when the CCEManagedControlPlane has a API server URL.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// FailureDomains specifies a list fo available availability zones that can be used
	// +optional
	FailureDomains clusterv1.FailureDomains `json:"failureDomains,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:path=ccemanagedclusters,scope=Namespaced,categories=cluster-api,shortName=ccemc
//+kubebuilder:storageversion
//+kubebuilder:subresource:status

// CCEManagedCluster is the Schema for the ccemanagedclusters API
type CCEManagedCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CCEManagedClusterSpec   `json:"spec,omitempty"`
	Status CCEManagedClusterStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// CCEManagedClusterList contains a list of CCEManagedCluster
type CCEManagedClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CCEManagedCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CCEManagedCluster{}, &CCEManagedClusterList{})
}
