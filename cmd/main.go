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
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	quotav1 "github.com/openshift/api/quota/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	autoresizev1alpha1 "github.com/csi-addons/pvc-autoresizer/api/v1alpha1"
	"github.com/csi-addons/pvc-autoresizer/internal/controller"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
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
	var resizerInterval time.Duration
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.DurationVar(&resizerInterval, "resizer-interval", 10*time.Minute,
		"How often the PVC autoresize runner evaluates policies and expands matching PVCs.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	cfg := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		// ToDo: implement metrics exporter.
		// Metrics are disabled for now.
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "91e00fff.csiaddons.openshift.io",
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

	resizer := controller.NewRunner(mgr.GetClient(), resizerInterval, hasCRQ)
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

// setupInformers checks for the ClusterResourceQuota API availability and registers cache informers early.
// ClusterResourceQuota is usually only available on OpenShift clusters (not a generic K8s resource).
func setupInformers(ctx context.Context, mgr ctrl.Manager, cfg *rest.Config) (bool, error) {
	cache := mgr.GetCache()
	objects := []client.Object{
		&corev1.PersistentVolumeClaim{},
		&autoresizev1alpha1.PVCAutoResizePolicy{},
		&corev1.Namespace{},
		&corev1.ResourceQuota{},
		&storagev1.StorageClass{},
	}
	for _, obj := range objects {
		if _, err := cache.GetInformer(ctx, obj); err != nil {
			return false, err
		}
	}

	available, err := isClusterResourceQuotaAvailable(cfg)
	if err != nil {
		return false, err
	}
	if available {
		setupLog.Info("ClusterResourceQuota API available, registering informer")
		if _, err := cache.GetInformer(ctx, &quotav1.ClusterResourceQuota{}); err != nil {
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
