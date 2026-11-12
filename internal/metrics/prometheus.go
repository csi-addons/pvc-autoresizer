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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"k8s.io/apimachinery/pkg/types"

	"github.com/csi-addons/pvc-autoresizer/internal/utils"
)

var _ MetricsProvider = (*PrometheusProvider)(nil)

// PrometheusProvider fetches volume metrics from a Prometheus server.
type PrometheusProvider struct {
	api    promv1.API
	config *utils.Config
}

// NewPrometheusProvider creates a PrometheusProvider with the given configuration.
func NewPrometheusProvider(cfg *utils.Config) (*PrometheusProvider, error) {
	if cfg.MetricsSource.PrometheusURL == "" {
		return nil, fmt.Errorf("prometheus URL is not configured")
	}

	transport, err := buildTransport(cfg.MetricsSource.PrometheusCABundlePath)
	if err != nil {
		return nil, fmt.Errorf("failed to build HTTP transport: %w", err)
	}

	var rt http.RoundTripper = transport
	if cfg.MetricsSource.PrometheusBearerTokenPath != "" {
		rt, err = newBearerTokenRoundTripper(cfg.MetricsSource.PrometheusBearerTokenPath, transport)
		if err != nil {
			return nil, fmt.Errorf("failed to configure bearer token auth: %w", err)
		}
	}

	client, err := api.NewClient(api.Config{
		Address:      cfg.MetricsSource.PrometheusURL,
		RoundTripper: rt,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Prometheus API client: %w", err)
	}

	return &PrometheusProvider{
		api:    promv1.NewAPI(client),
		config: cfg,
	}, nil
}

func buildTransport(caBundlePath string) (*http.Transport, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true

	if caBundlePath != "" {
		caCert, err := os.ReadFile(caBundlePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA bundle from %s: %w", caBundlePath, err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificates from %s", caBundlePath)
		}

		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.RootCAs = caCertPool
	}

	return transport, nil
}

// bearerTokenRoundTripper adds a Bearer token from a file to each request.
type bearerTokenRoundTripper struct {
	tokenPath string
	next      http.RoundTripper
}

func newBearerTokenRoundTripper(tokenPath string, next http.RoundTripper) (*bearerTokenRoundTripper, error) {
	if _, err := os.Stat(tokenPath); err != nil {
		return nil, fmt.Errorf("bearer token file %s not accessible: %w", tokenPath, err)
	}
	return &bearerTokenRoundTripper{tokenPath: tokenPath, next: next}, nil
}

func (rt *bearerTokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Token is re-read on every request so rotated tokens are picked up automatically.
	token, err := os.ReadFile(rt.tokenPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read bearer token from %s: %w", rt.tokenPath, err)
	}

	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+string(token))
	return rt.next.RoundTrip(req)
}

// FetchAllMetrics queries Prometheus for all metric groups (default or provisioner specific override) in parallel.
// One goroutine for the default group + one per custom provisioner override.
// Each goroutine runs 4 queries synchronously (avail, cap, inodesFree, inodes).
func (p *PrometheusProvider) FetchAllMetrics(ctx context.Context) (*MetricsResult, error) {
	result := &MetricsResult{
		DefaultStats:     make(map[types.NamespacedName]VolumeStats),
		ProvisionerStats: make(map[string]map[types.NamespacedName]VolumeStats),
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	// Default group
	wg.Go(func() {
		stats, groupErr := p.fetchGroup(ctx, DefaultGroup,
			utils.DefaultAvailableBytesQuery,
			utils.DefaultCapacityBytesQuery,
			utils.DefaultInodesFreeQuery,
			utils.DefaultInodesQuery,
		)
		mu.Lock()
		defer mu.Unlock()
		if groupErr != nil {
			result.GroupErrors = append(result.GroupErrors, *groupErr)
			result.DefaultStats = nil
		} else {
			result.DefaultStats = stats
		}
	})

	// Custom provisioner override groups
	for provName := range p.config.MetricsSource.Provisioners {
		queries := p.config.GetMetricsQueriesForProvisioner(provName)
		wg.Add(1)
		go func(name string, q *utils.ProvisionerMetrics) {
			defer wg.Done()
			stats, groupErr := p.fetchGroup(ctx, name,
				q.MetricsAvailableBytesQuery,
				q.MetricsCapacityBytesQuery,
				q.MetricsInodesFreeQuery,
				q.MetricsInodesQuery,
			)
			mu.Lock()
			defer mu.Unlock()
			if groupErr != nil {
				result.GroupErrors = append(result.GroupErrors, *groupErr)
				result.ProvisionerStats[name] = nil
			} else {
				result.ProvisionerStats[name] = stats
			}
		}(provName, queries)
	}

	wg.Wait()

	return result, nil
}

// fetchGroup runs 4 queries synchronously for a single group.
func (p *PrometheusProvider) fetchGroup(
	ctx context.Context,
	provisioner, availableQuery, capacityQuery, inodesFreeQuery, inodesQuery string,
) (map[types.NamespacedName]VolumeStats, *GroupError) {
	// Bytes queries (available, capacity) are required, on failure the whole group fails.
	// Both queries must succeed for capacity based evaluation
	available, err := p.queryMetric(ctx, availableQuery)
	if err != nil {
		return nil, &GroupError{
			Provisioner: provisioner,
			Reason:      classifyError(err),
			Err:         fmt.Errorf("query %q failed: %w", availableQuery, err),
		}
	}

	capacity, err := p.queryMetric(ctx, capacityQuery)
	if err != nil {
		return nil, &GroupError{
			Provisioner: provisioner,
			Reason:      classifyError(err),
			Err:         fmt.Errorf("query %q failed: %w", capacityQuery, err),
		}
	}

	// Inode queries are optional, on failure the group fallback to bytes queries only evaluation.
	// Both queries must succeed for inode based evaluation
	var inodesFree, inodes map[types.NamespacedName]float64
	inodesFree, err = p.queryMetric(ctx, inodesFreeQuery)
	if err != nil {
		inodesFree = nil
	}
	if inodesFree != nil {
		inodes, err = p.queryMetric(ctx, inodesQuery)
		if err != nil {
			inodesFree = nil
			inodes = nil
		}
	}

	stats := make(map[types.NamespacedName]VolumeStats, len(available))
	for pvcKey, availVal := range available {
		capVal, hasCapacity := capacity[pvcKey]
		if !hasCapacity {
			continue
		}

		vs := VolumeStats{
			AvailableBytes: availVal,
			CapacityBytes:  capVal,
		}

		if inodesFree != nil && inodes != nil {
			if ifVal, hasIF := inodesFree[pvcKey]; hasIF {
				if iVal, hasI := inodes[pvcKey]; hasI {
					vs.InodesFree = &ifVal
					vs.Inodes = &iVal
				}
			}
		}

		stats[pvcKey] = vs
	}

	return stats, nil
}

// queryMetric runs a single Prometheus instant query and extracts "NamespacedName" from result labels.
func (p *PrometheusProvider) queryMetric(ctx context.Context, query string) (map[types.NamespacedName]float64, error) {
	result, _, err := p.api.Query(ctx, query, time.Now())
	if err != nil {
		return nil, err
	}

	vector, ok := result.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("unexpected result type %T for query %q", result, query)
	}

	parsed := make(map[types.NamespacedName]float64, len(vector))
	for _, sample := range vector {
		ns := string(sample.Metric["namespace"])
		pvc := string(sample.Metric["persistentvolumeclaim"])
		if ns == "" || pvc == "" {
			continue
		}
		parsed[types.NamespacedName{Namespace: ns, Name: pvc}] = float64(sample.Value)
	}

	return parsed, nil
}

func classifyError(err error) GroupErrorReason {
	if _, ok := errors.AsType[net.Error](err); ok {
		return ReasonMetricsServerNotReachable
	}
	return ReasonMetricsUnavailable
}
