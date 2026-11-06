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

package metrics

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
)

const (
	DefaultGroup = "default"
)

// GroupErrorReason indicates why a metrics group fetch failed.
type GroupErrorReason string

const (
	ReasonMetricsUnavailable        GroupErrorReason = "MetricsUnavailable"
	ReasonMetricsServerNotReachable GroupErrorReason = "MetricsServerNotReachable"
)

// MetricsProvider fetches volume metrics from a backend (Prometheus, K8s Metrics API, etc.).
type MetricsProvider interface {
	FetchAllMetrics(ctx context.Context) (*MetricsResult, error)
}

// MetricsResult holds fetched metrics grouped by query source (default or provisioner specific override).
type MetricsResult struct {
	// DefaultStats holds per-PVC metrics fetched using default kubelet_volume_stats_* queries.
	DefaultStats map[types.NamespacedName]VolumeStats

	// ProvisionerStats holds per-PVC metrics fetched using custom provisioner queries.
	ProvisionerStats map[string]map[types.NamespacedName]VolumeStats

	// GroupErrors records per-group fetch failures.
	GroupErrors []GroupError
}

// VolumeStats holds the volume metrics for a single PVC.
type VolumeStats struct {
	AvailableBytes float64
	CapacityBytes  float64
	InodesFree     *float64 // nil if inode metrics unavailable
	Inodes         *float64 // nil if inode metrics unavailable
}

// GroupError records a fetch failure for a single metrics group.
type GroupError struct {
	Provisioner string
	Reason      GroupErrorReason
	Err         error
}
