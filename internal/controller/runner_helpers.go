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

package controller

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"time"

	autoresizev1alpha1 "github.com/csi-addons/pvc-autoresizer/api/v1alpha1"
	"github.com/csi-addons/pvc-autoresizer/internal/metrics"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	ConditionTypeCapacityUnknown = "CapacityUnknown"
	ConditionTypeDuplicatePolicy = "DuplicatePolicy"
	ConditionTypeResizeFailed    = "ResizeFailed"
	ConditionTypeInvalidSelector = "InvalidSelector"

	PhaseReady     = "Ready"
	PhaseSuspended = "Suspended"
	PhaseDuplicate = "Duplicate"
	PhaseError     = "Error"
)

// compiledSelector holds pre-compiled label selector and name regex for efficient matching.
type compiledSelector struct {
	labelSelector labels.Selector
	nameRegex     *regexp.Regexp
}

func (cs *compiledSelector) matches(name string, lbls labels.Set) bool {
	if cs == nil {
		return true
	}
	if cs.labelSelector != nil && !cs.labelSelector.Matches(lbls) {
		return false
	}
	if cs.nameRegex != nil && !cs.nameRegex.MatchString(name) {
		return false
	}
	return true
}

// buildCapacityUnknownResults creates policyResults for all policies when metrics
// are completely unavailable.
func buildCapacityUnknownResults(
	policies []autoresizev1alpha1.PVCAutoResizePolicy,
	reason, message string,
) map[string]*policyResult {
	results := make(map[string]*policyResult, len(policies))
	for i := range policies {
		results[policies[i].Name] = &policyResult{
			phase: determinePolicyPhase(policies[i].Spec.Suspend, true, false),
			conditions: []metav1.Condition{
				newCapacityUnknownCondition(reason, message),
			},
		}
	}
	return results
}

func newCapacityUnknownCondition(reason, message string) metav1.Condition {
	return metav1.Condition{
		Type:               ConditionTypeCapacityUnknown,
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}
}

func newInvalidSelectorCondition(selectorType string, err error) metav1.Condition {
	return metav1.Condition{
		Type:               ConditionTypeInvalidSelector,
		Status:             metav1.ConditionTrue,
		Reason:             "InvalidSelector",
		Message:            fmt.Sprintf("%s selector is invalid: %v", selectorType, err),
		LastTransitionTime: metav1.NewTime(time.Now()),
	}
}

func newDuplicatePolicyCondition(duplicateCount, selectorMatchedCount int32) metav1.Condition {
	return metav1.Condition{
		Type:               ConditionTypeDuplicatePolicy,
		Status:             metav1.ConditionTrue,
		Reason:             "OverlappingPVCs",
		Message:            fmt.Sprintf("%d of %d selector-matched PVCs are already owned by another policy", duplicateCount, selectorMatchedCount),
		LastTransitionTime: metav1.NewTime(time.Now()),
	}
}

// determinePolicyPhase returns the policy phase.
func determinePolicyPhase(suspended, hasError, hasDuplicates bool) string {
	if suspended {
		return PhaseSuspended
	}
	if hasError {
		return PhaseError
	}
	if hasDuplicates {
		return PhaseDuplicate
	}
	return PhaseReady
}

// sortPolicies sorts policies by ownership priority:
// non-suspended first, then creationTimestamp, then name as a fallback.
func sortPolicies(policies []autoresizev1alpha1.PVCAutoResizePolicy) {
	slices.SortFunc(policies, func(a, b autoresizev1alpha1.PVCAutoResizePolicy) int {
		if a.Spec.Suspend != b.Spec.Suspend {
			if !a.Spec.Suspend {
				return -1
			}
			return 1
		}
		if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
			if a.CreationTimestamp.Before(&b.CreationTimestamp) {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})
}

// compileTargetSelector pre-compiles a TargetSelector's label selector and name regex.
func compileTargetSelector(ts autoresizev1alpha1.TargetSelector) (*compiledSelector, error) {
	cs := &compiledSelector{}

	if ts.LabelSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(ts.LabelSelector)
		if err != nil {
			return nil, fmt.Errorf("invalid label selector: %w", err)
		}
		cs.labelSelector = selector
	}

	if ts.NameRegexSelector != nil && *ts.NameRegexSelector != "" {
		regex, err := regexp.Compile("^(?:" + *ts.NameRegexSelector + ")$")
		if err != nil {
			return nil, fmt.Errorf("invalid name regex %q: %w", *ts.NameRegexSelector, err)
		}
		cs.nameRegex = regex
	}

	return cs, nil
}

func findGroupError(groupErrors []metrics.GroupError, provisioner string) *metrics.GroupError {
	for i := range groupErrors {
		if groupErrors[i].Provisioner == provisioner {
			return &groupErrors[i]
		}
	}
	return nil
}
