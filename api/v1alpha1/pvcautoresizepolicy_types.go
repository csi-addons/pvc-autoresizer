/*
Copyright 2026.

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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// PVCAutoResizePolicySpec defines the desired state of PVCAutoResizePolicy
type PVCAutoResizePolicySpec struct {
	// Suspend is a policy-wide pause/suspend.
	// Auto-expansion for all targeted PVC(s) will be suspended.
	// +optional
	// +kubebuilder:default=false
	Suspend bool `json:"suspend,omitempty"`

	// Provisioner indicates the type of the provisioner (e.g. CSI driver name).
	// PVC(s) referencing this provisioner will be targeted for auto-expansion.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="provisioner is immutable"
	Provisioner string `json:"provisioner"`

	// Selectors specifies targeting criteria for Namespace(s) and PVC(s),
	// based on label selector and name regex expression.
	// If empty, means target all Namespaces and PVCs.
	// +optional
	// +kubebuilder:default={}
	Selectors Selectors `json:"selectors"`

	// ExpansionThresholds configures when and how storage expansion is triggered,
	// defines thresholds and increment rules for auto-expansion
	// +optional
	// +kubebuilder:default={}
	ExpansionThresholds ExpansionThresholds `json:"expansionThresholds"`
}

// Selectors specifies targeting criteria for Namespace(s) and PVC(s).
type Selectors struct {
	// Namespace selects Namespace(s) whose PVC(s) may be targeted.
	// If empty, means target all Namespaces.
	// +optional
	// +kubebuilder:default={}
	Namespace TargetSelector `json:"namespace"`

	// PersistentVolumeClaim selects PVC(s) within matched Namespace(s).
	// If empty, means target all PVCs.
	// +optional
	// +kubebuilder:default={}
	PersistentVolumeClaim TargetSelector `json:"persistentVolumeClaim"`
}

// TargetSelector selects objects by label selector and/or name regex.
type TargetSelector struct {
	// LabelSelector is a standard Kubernetes label selector.
	// +optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`

	// NameRegexSelector is a regular expression matched against object names.
	// +optional
	NameRegexSelector *string `json:"nameRegexSelector,omitempty"`
}

// ExpansionThresholds defines thresholds and increment rules for auto-expansion.
type ExpansionThresholds struct {
	// UsagePercentage is the disk space usage percentage triggering expansion.
	// +optional
	// +kubebuilder:default=80
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	UsagePercentage uint8 `json:"usagePercentage,omitempty"`

	// UsageInodePercentage is the inode usage percentage triggering expansion.
	// +optional
	// +kubebuilder:default=80
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	UsageInodePercentage uint8 `json:"usageInodePercentage,omitempty"`

	// IncreasePercentage defines the resize increment as a percentage of the current
	// volume size (e.g. 25 means add 25% of the current size).
	// Used when IncreaseAbsolute is not set.
	// +optional
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	IncreasePercentage uint8 `json:"increasePercentage,omitempty"`

	// IncreaseAbsolute defines the absolute quantity to add to the current storage (e.g. "50Gi").
	// When set, IncreaseAbsolute takes precedence over IncreasePercentage.
	// +optional
	IncreaseAbsolute *resource.Quantity `json:"increaseAbsolute,omitempty"`

	// MaxCapacity is the upper bound limit for storage expansion (e.g. "2Ti").
	// +optional
	MaxCapacity *resource.Quantity `json:"maxCapacity,omitempty"`
}

// PVCAutoResizePolicyStatus defines the observed state of PVCAutoResizePolicy.
type PVCAutoResizePolicyStatus struct {
	// MatchedPVCsCount represents total count of the PVC(s) targeted by the policy.
	// +optional
	MatchedPVCsCount int32 `json:"matchedPVCsCount,omitempty"`

	// Phase indicates the policy state (e.g. Ready, Suspended, Duplicate, Error).
	// +optional
	// +kubebuilder:validation:Enum=Ready;Suspended;Duplicate;Error
	Phase string `json:"phase,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// PVCAutoResizePolicy is the Schema for the pvcautoresizepolicies API
type PVCAutoResizePolicy struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of PVCAutoResizePolicy
	// +required
	Spec PVCAutoResizePolicySpec `json:"spec"`

	// status defines the observed state of PVCAutoResizePolicy
	// +optional
	Status PVCAutoResizePolicyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PVCAutoResizePolicyList contains a list of PVCAutoResizePolicy
type PVCAutoResizePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PVCAutoResizePolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &PVCAutoResizePolicy{}, &PVCAutoResizePolicyList{})
		return nil
	})
}
