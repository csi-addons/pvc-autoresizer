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

package utils

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	// ConfigMapName is the name of the ConfigMap containing autoresizer configuration.
	ConfigMapName = "pvc-autoresizer-config"
	configMapKey  = "config.yaml"

	// Default metric queries using kubelet_volume_stats_* metrics.
	DefaultAvailableBytesQuery = `kubelet_volume_stats_available_bytes`
	DefaultCapacityBytesQuery  = `kubelet_volume_stats_capacity_bytes`
	DefaultInodesFreeQuery     = `kubelet_volume_stats_inodes_free`
	DefaultInodesQuery         = `kubelet_volume_stats_inodes`
)

// Config represents the configuration for the PVC autoresizer.
type Config struct {
	MetricsSource MetricsSourceConfig `json:"metrics-source"`
}

// MetricsSourceConfig configures the metrics backend and queries.
type MetricsSourceConfig struct {
	// Required if UseK8sMetricsAPI is false.
	// PrometheusURL is the URL of the Prometheus server.
	PrometheusURL string `json:"prometheus-url"`

	// Optional.
	// PrometheusCABundlePath is the path to the CA certificate bundle for Prometheus.
	// Only needed for https URLs with self-signed or internal CAs.
	PrometheusCABundlePath string `json:"prometheus-ca-bundle-path"`

	// Optional.
	// PrometheusBearerTokenPath is the path to a file containing a bearer token
	// for authenticating with Prometheus (e.g. a ServiceAccount token).
	PrometheusBearerTokenPath string `json:"prometheus-bearer-token-path"`

	// UseK8sMetricsAPI switches to using the Kubernetes Metrics API instead of Prometheus.
	UseK8sMetricsAPI bool `json:"use-k8s-metrics-api"`

	// Optional.
	// Provisioners allows overriding default kubelet_volume_stats_* metrics
	// with custom queries for specific provisioners.
	Provisioners map[string]ProvisionerMetrics `json:"provisioners"`
}

// ProvisionerMetrics defines custom metric queries for a specific provisioner.
type ProvisionerMetrics struct {
	MetricsAvailableBytesQuery string `json:"metrics-available-bytes-query"`
	MetricsCapacityBytesQuery  string `json:"metrics-capacity-bytes-query"`
	MetricsInodesFreeQuery     string `json:"metrics-inodes-free-query"`
	MetricsInodesQuery         string `json:"metrics-inodes-query"`
}

// GetDefaultConfig returns the default configuration.
func GetDefaultConfig() *Config {
	return &Config{
		MetricsSource: MetricsSourceConfig{
			PrometheusURL:             "",
			PrometheusCABundlePath:    "",
			PrometheusBearerTokenPath: "",
			UseK8sMetricsAPI:          false,
			Provisioners:              make(map[string]ProvisionerMetrics),
		},
	}
}

// LoadConfig loads the configuration from the ConfigMap.
func LoadConfig(ctx context.Context, c client.Client, namespace string) (*Config, error, bool) {
	cm := &corev1.ConfigMap{}
	err := c.Get(ctx, types.NamespacedName{
		Name:      ConfigMapName,
		Namespace: namespace,
	}, cm)

	if err != nil {
		if apierrors.IsNotFound(err) {
			return GetDefaultConfig(),
				fmt.Errorf("ConfigMap %s/%s not found, using default configuration", namespace, ConfigMapName),
				false
		}

		return GetDefaultConfig(),
			fmt.Errorf("failed to get ConfigMap %s/%s: %w", namespace, ConfigMapName, err),
			true
	}

	configYAML, ok := cm.Data[configMapKey]
	if !ok || configYAML == "" {
		return GetDefaultConfig(),
			fmt.Errorf("ConfigMap %s/%s data key %q not found or empty, using default configuration", namespace, ConfigMapName, configMapKey),
			false
	}

	return parseConfig(configYAML, namespace)
}

// parseConfig parses the YAML configuration.
func parseConfig(configYAML, namespace string) (*Config, error, bool) {
	cfg := GetDefaultConfig()
	if err := yaml.Unmarshal([]byte(configYAML), cfg); err != nil {
		return GetDefaultConfig(),
			fmt.Errorf("failed to parse ConfigMap %s/%s YAML: %v, using default configuration", namespace, ConfigMapName, err),
			false
	}

	return cfg, nil, false
}

// GetMetricsQueriesForProvisioner returns custom metrics for the provisioner, or nil if it uses defaults.
func (c *Config) GetMetricsQueriesForProvisioner(provisioner string) *ProvisionerMetrics {
	customMetrics, ok := c.MetricsSource.Provisioners[provisioner]
	if !ok {
		return nil
	}

	// Has custom config, but fill in defaults for empty/missing fields (if any).
	if customMetrics.MetricsAvailableBytesQuery == "" {
		customMetrics.MetricsAvailableBytesQuery = DefaultAvailableBytesQuery
	}
	if customMetrics.MetricsCapacityBytesQuery == "" {
		customMetrics.MetricsCapacityBytesQuery = DefaultCapacityBytesQuery
	}
	if customMetrics.MetricsInodesFreeQuery == "" {
		customMetrics.MetricsInodesFreeQuery = DefaultInodesFreeQuery
	}
	if customMetrics.MetricsInodesQuery == "" {
		customMetrics.MetricsInodesQuery = DefaultInodesQuery
	}

	return &customMetrics
}
