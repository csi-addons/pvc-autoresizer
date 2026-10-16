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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	autoresizev1alpha1 "github.com/csi-addons/pvc-autoresizer/api/v1alpha1"
)

// PVCAutoResizePolicyReconciler watches PVCAutoResizePolicy objects and signals the
// pvcautoresizepolicy "runner" to evaluate policies early on create/update,
// instead of waiting for the entire periodic tick duration.
type PVCAutoResizePolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Runner *Runner
}

func (r *PVCAutoResizePolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("pvcautoresizepolicy")
	logger.Info("PVCAutoResizePolicy changed, triggering early runner tick", "name", req.Name)

	if r.Runner != nil {
		r.Runner.Trigger()
	}

	return ctrl.Result{}, nil
}

var createOrSpecUpdatePredicate = predicate.Funcs{
	CreateFunc: func(_ event.CreateEvent) bool { return true },
	UpdateFunc: func(e event.UpdateEvent) bool {
		return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration()
	},
	DeleteFunc:  func(_ event.DeleteEvent) bool { return false },
	GenericFunc: func(_ event.GenericEvent) bool { return false },
}

// SetupWithManager sets up the controller with the Manager.
func (r *PVCAutoResizePolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&autoresizev1alpha1.PVCAutoResizePolicy{}).
		WithEventFilter(createOrSpecUpdatePredicate).
		Named("pvcautoresizepolicy").
		Complete(r)
}
