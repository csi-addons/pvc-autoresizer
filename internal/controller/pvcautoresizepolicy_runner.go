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
	"context"
	"fmt"
	"time"

	autoresizev1alpha1 "github.com/csi-addons/pvc-autoresizer/api/v1alpha1"
	"github.com/csi-addons/pvc-autoresizer/internal/metrics"
	"github.com/csi-addons/pvc-autoresizer/internal/utils"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// PVCAutoResizePolicy create/update signals "trigger" so a tick can run earlier than "interval".
type Runner struct {
	client.Client
	interval                time.Duration
	hasClusterResourceQuota bool
	operatorNamespace       string
	trigger                 chan struct{}
}

func NewRunner(c client.Client, interval time.Duration, hasClusterResourceQuota bool, operatorNamespace string) *Runner {
	return &Runner{
		Client:                  c,
		interval:                interval,
		hasClusterResourceQuota: hasClusterResourceQuota,
		operatorNamespace:       operatorNamespace,
		trigger:                 make(chan struct{}, 1),
	}
}

// Trigger requests an early "runTick".
// Non-blocking, if a trigger is already pending (channel is full).
// Additional calls are dropped intentionally,
// the already pending tick will handle the auto resize for all the policies.
func (r *Runner) Trigger() {
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

// policyResult tracks per-policy state accumulated across phases.
type policyResult struct {
	phase            string
	matchedPVCsCount int32
	conditions       []metav1.Condition

	duplicatePVCsCount          int32
	maxCapacityReachedPVCsCount int32 //nolint:unused // used in phase 5-7
	errorPVCsCount              int32 //nolint:unused // used in phase 5-7
}

// +kubebuilder:rbac:groups=autoresize.csiaddons.openshift.io,resources=pvcautoresizepolicies,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=autoresize.csiaddons.openshift.io,resources=pvcautoresizepolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=resourcequotas,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=quota.openshift.io,resources=clusterresourcequotas,verbs=get;list;watch

func (r *Runner) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("runner")
	logger.Info("Starting PVC autoresize runner", "interval", r.interval)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Stopping PVC autoresize runner")
			return nil
		case <-ticker.C:
			r.runTick(ctx)
		case <-r.trigger:
			r.runTick(ctx)
		}
	}
}

// runTick periodically evaluates PVCAutoResizePolicy objects and resizes matching PVCs.
func (r *Runner) runTick(ctx context.Context) {
	logger := log.FromContext(ctx).WithName("runner")
	logger.Info("Running PVC autoresize tick")

	// Early exit, if no policies are found.
	var policyList autoresizev1alpha1.PVCAutoResizePolicyList
	if err := r.List(ctx, &policyList); err != nil {
		logger.Error(err, "Failed to list PVCAutoResizePolicies, aborting tick")
		return
	}
	if len(policyList.Items) == 0 {
		logger.Info("No PVCAutoResizePolicies found, skipping tick")
		return
	}

	// Config load from ConfigMap.
	cfg, err, shouldAbort := utils.LoadConfig(ctx, r.Client, r.operatorNamespace)
	if err != nil {
		if shouldAbort {
			logger.Error(err, "Failed to load configuration, aborting tick")
			return
		}
		logger.Info(err.Error())
	}
	logger.Info("Configuration loaded")

	var provider metrics.MetricsProvider
	// ToDo: implement Kubernetes Metrics API.
	if cfg.MetricsSource.UseK8sMetricsAPI {
		logger.Error(nil, "Kubernetes Metrics API is not yet implemented, aborting tick")
		return
	} else {
		provider, err = metrics.NewPrometheusProvider(cfg)
		if err != nil {
			logger.Error(err, "Failed to create Prometheus provider, aborting tick")
			return
		}
	}
	logger.Info("Metrics provider created")

	// Fetch metrics and resolve PVC ownership.
	metricsResult, policyResults, owned, err := r.fetchMetricsAndResolveOwnership(ctx, provider, policyList.Items)
	if err != nil {
		// Log error, but continue with the flow (no abort).
		// Results still carry error states that must be reflected in status updates.
		logger.Error(err, "Failed to fetch metrics and resolve ownership")
	} else {
		logger.Info("Completed fetching metrics and resolving ownership")
	}

	// ToDo: Phase 5-7.
	_ = metricsResult
	_ = policyResults
	_ = owned
}

// fetchMetricsAndResolveOwnership fetches metrics, sorts policies by ownership priority,
// iterates PVC, matches selectors, checks eligibility, and resolves ownership.
func (r *Runner) fetchMetricsAndResolveOwnership(
	ctx context.Context,
	provider metrics.MetricsProvider,
	policies []autoresizev1alpha1.PVCAutoResizePolicy,
) (*metrics.MetricsResult, map[string]*policyResult, map[types.NamespacedName]string, error) {
	owned := make(map[types.NamespacedName]string)

	if provider == nil {
		return nil,
			buildCapacityUnknownResults(policies,
				string(metrics.ReasonMetricsServerNotReachable),
				"metrics provider is not available"),
			owned,
			fmt.Errorf("metrics provider is not available")
	}

	metricsResult, err := provider.FetchAllMetrics(ctx)
	if err != nil {
		return nil,
			buildCapacityUnknownResults(policies,
				string(metrics.ReasonMetricsServerNotReachable),
				err.Error()),
			owned,
			fmt.Errorf("failed to fetch metrics: %w", err)
	}

	sortPolicies(policies)

	policyResults := make(map[string]*policyResult, len(policies))

	// Iterate policies in sorted order, from highest to lowest priority.
	// This helps with the case where a PVC is targeted by multiple policies.
	for i := range policies {
		policy := &policies[i]
		pr := &policyResult{}

		if policy.Spec.Suspend {
			pr.phase = PhaseSuspended
			policyResults[policy.Name] = pr
			continue
		}

		var pvcStats map[types.NamespacedName]metrics.VolumeStats

		// Fetch metrics results for the policy's provisioner.
		// If the provisioner is not found (not exists), means no metrics overridden via ConfigMap,
		// use the default (kubelet) stats in that case.
		// If provisioner is found but stats are explicitly nil,
		// means metrics are overridden via ConfigMap, and fetch failed for the provisioner.
		if stats, exists := metricsResult.ProvisionerStats[policy.Spec.Provisioner]; exists {
			if stats == nil {
				if ge := findGroupError(metricsResult.GroupErrors, policy.Spec.Provisioner); ge != nil {
					pr.conditions = append(pr.conditions, newCapacityUnknownCondition(
						string(ge.Reason), ge.Err.Error()))
				}
				pr.phase = PhaseError
				policyResults[policy.Name] = pr
				continue
			}
			pvcStats = stats
		} else {
			if metricsResult.DefaultStats == nil {
				if ge := findGroupError(metricsResult.GroupErrors, metrics.DefaultGroup); ge != nil {
					pr.conditions = append(pr.conditions, newCapacityUnknownCondition(
						string(ge.Reason), ge.Err.Error()))
				}
				pr.phase = PhaseError
				policyResults[policy.Name] = pr
				continue
			}
			pvcStats = metricsResult.DefaultStats
		}

		// Compile selectors.
		nsSelector, err := compileTargetSelector(policy.Spec.Selectors.Namespace)
		if err != nil {
			pr.conditions = append(pr.conditions, newInvalidSelectorCondition("namespace", err))
			pr.phase = PhaseError
			policyResults[policy.Name] = pr
			continue
		}

		pvcSelector, err := compileTargetSelector(policy.Spec.Selectors.PersistentVolumeClaim)
		if err != nil {
			pr.conditions = append(pr.conditions, newInvalidSelectorCondition("persistentVolumeClaim", err))
			pr.phase = PhaseError
			policyResults[policy.Name] = pr
			continue
		}

		// Iterate PVC keys from the metrics group.
		for pvcKey := range pvcStats {
			ns := &corev1.Namespace{}
			if err := r.Get(ctx, types.NamespacedName{Name: pvcKey.Namespace}, ns); err != nil {
				continue
			}

			if !nsSelector.matches(ns.Name, labels.Set(ns.Labels)) {
				continue
			}

			pvc := &corev1.PersistentVolumeClaim{}
			if err := r.Get(ctx, pvcKey, pvc); err != nil {
				continue
			}

			if !pvcSelector.matches(pvc.Name, labels.Set(pvc.Labels)) {
				continue
			}

			if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName == "" {
				continue
			}
			sc := &storagev1.StorageClass{}
			if err := r.Get(ctx, types.NamespacedName{Name: *pvc.Spec.StorageClassName}, sc); err != nil {
				continue
			}

			if sc.AllowVolumeExpansion == nil || !*sc.AllowVolumeExpansion {
				continue
			}

			if sc.Provisioner != policy.Spec.Provisioner {
				continue
			}

			if _, alreadyOwned := owned[pvcKey]; alreadyOwned {
				pr.duplicatePVCsCount++
			} else {
				owned[pvcKey] = policy.Name
				pr.matchedPVCsCount++
			}
		}

		if pr.duplicatePVCsCount > 0 {
			pr.conditions = append(pr.conditions, newDuplicatePolicyCondition(
				pr.duplicatePVCsCount, pr.matchedPVCsCount+pr.duplicatePVCsCount))
		}

		pr.phase = determinePolicyPhase(false, false, pr.duplicatePVCsCount > 0)
		policyResults[policy.Name] = pr
	}

	return metricsResult, policyResults, owned, nil
}
