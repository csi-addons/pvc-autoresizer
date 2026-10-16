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
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Runner periodically evaluates PVCAutoResizePolicy objects and resizes matching PVCs.
// PVCAutoResizePolicy create/update signals "trigger" so a tick can run earlier than "Interval".
type Runner struct {
	client.Client
	interval                time.Duration
	hasClusterResourceQuota bool
	trigger                 chan struct{}
}

func NewRunner(c client.Client, interval time.Duration, hasClusterResourceQuota bool) *Runner {
	return &Runner{
		Client:                  c,
		interval:                interval,
		hasClusterResourceQuota: hasClusterResourceQuota,
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

// +kubebuilder:rbac:groups=autoresize.csiaddons.openshift.io,resources=pvcautoresizepolicies,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=autoresize.csiaddons.openshift.io,resources=pvcautoresizepolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=resourcequotas,verbs=get;list;watch
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

func (r *Runner) runTick(ctx context.Context) {
	logger := log.FromContext(ctx).WithName("runner")
	logger.Info("Running PVC autoresize tick")

	// ToDo: implement periodic resize logic.
}
