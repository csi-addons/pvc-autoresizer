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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	quotav1 "github.com/openshift/api/quota/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	autoresizev1alpha1 "github.com/csi-addons/pvc-autoresizer/api/v1alpha1"
	"github.com/csi-addons/pvc-autoresizer/internal/controller"
	"github.com/csi-addons/pvc-autoresizer/internal/utils"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

const (
	// minPollingInterval is the minimum allowed polling interval to prevent overloading the API server and Prometheus.
	minPollingInterval = 5 * time.Minute
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(autoresizev1alpha1.AddToScheme(scheme))
	utilruntime.Must(quotav1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var enableLeaderElection bool
	var probeAddr string
	var pollingInterval time.Duration
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.DurationVar(&pollingInterval, "polling-interval", 10*time.Minute,
		fmt.Sprintf("How often the PVC autoresize runner evaluates policies and expands matching PVCs. "+
			"Minimum: %v", minPollingInterval))
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Validate polling interval to prevent overloading the API server and Prometheus
	if pollingInterval < minPollingInterval {
		setupLog.Error(fmt.Errorf("polling interval %v is below minimum %v", pollingInterval, minPollingInterval),
			"Invalid polling interval")
		os.Exit(1)
	}

	operatorNamespace, err := utils.GetOperatorNamespace()
	if err != nil {
		setupLog.Error(err, "Failed to get operator namespace")
		os.Exit(1)
	}
	setupLog.Info("Operator namespace", "namespace", operatorNamespace)

	cfg := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		// ToDo: implement metrics exporter.
		// Metrics are disabled for now.
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "91e00fff.csiaddons.openshift.io",
		Cache:                  getCacheOptions(operatorNamespace),
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	ctx := context.Background()
	hasCRQ, err := setupInformers(ctx, mgr, cfg)
	if err != nil {
		setupLog.Error(err, "Failed to set up informers")
		os.Exit(1)
	}

	resizer := controller.NewRunner(mgr.GetClient(), pollingInterval, hasCRQ, operatorNamespace)
	if err := mgr.Add(resizer); err != nil {
		setupLog.Error(err, "Failed to add PVC autoresize runner")
		os.Exit(1)
	}

	if err := (&controller.PVCAutoResizePolicyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Runner: resizer,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "PVCAutoResizePolicy")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}

func getCacheOptions(operatorNamespace string) cache.Options {
	return cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			// Cache only a single required ConfigMap.
			&corev1.ConfigMap{}: {
				Namespaces: map[string]cache.Config{
					operatorNamespace: {},
				},
				Field: fields.SelectorFromSet(fields.Set{
					"metadata.name": utils.ConfigMapName,
				}),
			},
			// Cache only required PVC fields.
			&corev1.PersistentVolumeClaim{}: {
				Transform: func(obj any) (any, error) {
					pvc := obj.(*corev1.PersistentVolumeClaim)
					return &corev1.PersistentVolumeClaim{
						ObjectMeta: metav1.ObjectMeta{
							Name:            pvc.Name,
							Namespace:       pvc.Namespace,
							ResourceVersion: pvc.ResourceVersion,
							UID:             pvc.UID,
							Labels:          pvc.Labels,
							Annotations:     pvc.Annotations,
						},
						Spec: corev1.PersistentVolumeClaimSpec{
							StorageClassName: pvc.Spec.StorageClassName,
							Resources:        pvc.Spec.Resources,
							AccessModes:      pvc.Spec.AccessModes,
							VolumeName:       pvc.Spec.VolumeName,
						},
						Status: corev1.PersistentVolumeClaimStatus{
							Capacity:   pvc.Status.Capacity,
							Conditions: pvc.Status.Conditions,
							Phase:      pvc.Status.Phase,
						},
					}, nil
				},
			},
			// Cache only required Namespace fields.
			&corev1.Namespace{}: {
				Transform: func(obj any) (any, error) {
					ns := obj.(*corev1.Namespace)
					return &corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:            ns.Name,
							ResourceVersion: ns.ResourceVersion,
							UID:             ns.UID,
							Labels:          ns.Labels,
						},
					}, nil
				},
			},
		},
	}
}

// setupInformers checks for the ClusterResourceQuota API availability and registers cache informers early.
// ClusterResourceQuota is usually only available on OpenShift clusters (not a generic K8s resource).
func setupInformers(ctx context.Context, mgr ctrl.Manager, cfg *rest.Config) (bool, error) {
	mgrCache := mgr.GetCache()
	objects := []client.Object{
		// Only required PVC fields, using cache options.
		&corev1.PersistentVolumeClaim{},
		&autoresizev1alpha1.PVCAutoResizePolicy{},
		// Only required Namespace fields, using cache options.
		&corev1.Namespace{},
		&corev1.ResourceQuota{},
		&storagev1.StorageClass{},
		// Only a single operator config ConfigMap, using cache options.
		&corev1.ConfigMap{},
	}
	for _, obj := range objects {
		if _, err := mgrCache.GetInformer(ctx, obj); err != nil {
			return false, err
		}
	}

	available, err := isClusterResourceQuotaAvailable(cfg)
	if err != nil {
		return false, err
	}
	if available {
		setupLog.Info("ClusterResourceQuota API available, registering informer")
		if _, err := mgrCache.GetInformer(ctx, &quotav1.ClusterResourceQuota{}); err != nil {
			return false, err
		}
	} else {
		setupLog.Info("ClusterResourceQuota API not available, skipping informer")
	}
	return available, nil
}

func isClusterResourceQuotaAvailable(cfg *rest.Config) (bool, error) {
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return false, err
	}

	gvk, err := apiutil.GVKForObject(&quotav1.ClusterResourceQuota{}, scheme)
	if err != nil {
		return false, err
	}

	resourceList, err := discoveryClient.ServerResourcesForGroupVersion(gvk.GroupVersion().String())
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}

		if discovery.IsGroupDiscoveryFailedError(err) {
			return false, nil
		}
		return false, err
	}

	for _, resource := range resourceList.APIResources {
		if resource.Kind == gvk.Kind {
			return true, nil
		}
	}
	return false, nil
}
