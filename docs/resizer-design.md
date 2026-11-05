## Problem
Stateful applications, especially database engines like PostgreSQL, MySQL, or MongoDB are exceptionally vulnerable to volume saturation. Running out of disk space can lead to abrupt crashes, transaction log lockups, or unrecoverable database corruption and resolving volume growth requires manual monitoring and intervention.

**Why is this important?**
Operational Overhead Reduction -  Automates routine storage capacity management, allowing users to focus on root-cause analysis rather than emergency disk resizing.
Prevention of Database Corruption - Guarantees that stateful databases maintain sufficient write buffer space during unexpected ingest spikes, keeping services available.

## Goal/Scope
- Automated expansion triggers based on configurable thresholds.
- Audit logging and metric events indicating when an automated expansion sequence initiates, completes, or hits a configured maximum capacity limit.
- Policies (or configuration settings) that can be applied on a single or group of PVCs.

**Out of Scope:**
- Workloads that require an automatic restart (e.g., StatefulSets, needs manual restart).
- Volumes managed by any custom controller(s) or automation.

## Existing Projects + Shortcomings
We evaluated several existing open-source projects in the same domain and identified certain limitations/shortcomings in how well they addressed our specific use case. This led us to pursue an independent implementation better aligned with our requirements.

#### [topolvm/pvc-autoresizer](https://github.com/topolvm/pvc-autoresizer)
- As of writing this doc, limited audit logging and metric events scenarios.
- No StorageQuota awareness or detection.
- No priority-based resize ordering under contention (something which we want to expand upon in future).
- As of writing this doc, limited metrics, poor fit for raising condition based Alerts such as "resize stuck" etc.
- No PVC grouping, annotations must be applied to each PVC individually, making changes difficult without centralized control.
- No concurrency for resize patches.

#### [redhat-cop/volume-expander-operator](https://github.com/redhat-cop/volume-expander-operator)
- No longer actively developed.
- Limited audit logging and metric events scenarios.
- No StorageQuota awareness or detection.
- No priority-based resize ordering under contention (something which we want to expand upon in future).
- No custom metrics exported at all.
- No PVC grouping, annotations must be applied to each PVC individually, making changes difficult without centralized control.
- Only supports Prometheus as the metrics source (no api-server/kubelet support).
- No checks for inodes utilisation.
- Use of hardcoded `InsecureSkipVerify: true` for metrics poll (TLS cert validation disabled unconditionally).
- Per PVC query/fetch to/from Prometheus server (instead of entire list in a single query).

#### [gardener/pvc-autoscaler](https://github.com/gardener/pvc-autoscaler)
- As of writing this doc, limited audit logging and metric events scenarios.
- No StorageQuota awareness or detection.
- No priority-based resize ordering under contention (something which we want to expand upon in future).
- As of writing this doc, limited metrics, poor fit for raising condition based Alerts such as "resize stuck" etc.
- No PVC grouping, annotations must be applied to each PVC individually, making changes difficult without centralized control.
- As of writing this doc, only supports Prometheus as the metrics source (no api-server/kubelet support).

#### [DevOps-Nirvana/Kubernetes-Volume-Autoscaler](https://github.com/DevOps-Nirvana/Kubernetes-Volume-Autoscaler)
- No longer actively developed.
- Limited audit logging and metric events scenarios.
- No StorageQuota awareness or detection.
- No priority-based resize ordering under contention (something which we want to expand upon in future).
- Limited metrics, poor fit for raising condition based Alerts such as "resize stuck" etc.
- No PVC grouping, either globally selects all PVCs by default, or needs to opt-out each PVC individually, making changes difficult without centralized control.
- Python based controller rather than using a K8s native framework (e.g., Go's "controller-runtime"), future extension to additional resources and controller patterns less straightforward.
- Only supports Prometheus as the metrics source (no api-server/kubelet support).
- No caching at all.
- Exits on Prometheus unavailability at startup, can CrashLoopBackOff the entire pod.

## Proposed Solution
A periodic "ticker/runner" based controller + a reconciler controller which signals the the "runner" to evaluate policies early on create/update events (instead of waiting for the entire periodic tick duration).

To periodically monitor and automatically resize PersistentVolumeClaim(s), when the used capacity or inodes exceeds certain threshold (configurable), create a PVCAutoResizePolicy custom resource.

### Example

```yaml
apiVersion: autoresize.csiaddons.openshift.io/v1alpha1
kind: PVCAutoResizePolicy
metadata:
  name: example-policy
spec:
  # Target PVC(s) using this provisioner (CSI driver)
  provisioner: ebs.csi.aws.com
  
  # Suspend auto-expansion for all targeted PVC(s)
  suspend: false
  
  # Target specific Namespace(s) and PVC(s)
  selectors:
    namespace:
      labelSelector:
        matchLabels:
          environment: production
      nameRegexSelector: "^prod-.*"
    persistentVolumeClaim:
      labelSelector:
        matchLabels:
          auto-resize: "true"
      nameRegexSelector: "^data-.*"
  
  # Configure expansion thresholds and behavior
  expansionThresholds:
    # Trigger expansion when disk usage reaches 80%
    usagePercentage: 80
    
    # Trigger expansion when inode usage reaches 80%
    usageInodePercentage: 80
    
    # Increase size by 10% of current capacity
    increasePercentage: 10
    
    # Or use absolute increment (takes precedence over increasePercentage)
    # increaseAbsolute: 50Gi
    
    # Set maximum capacity limit
    maxCapacity: 2Ti
```

### Implementational Details
TBA

### Configuration ConfigMap + Flags (command-line)
Default metrics used (for resizing calculations)
- **kubelet_volume_stats_available_bytes**: available bytes on the volume
- **kubelet_volume_stats_capacity_bytes**: capacity of the volume
- **kubelet_volume_stats_inodes_free**: number of free inodes
- **kubelet_volume_stats_inodes**: number of inodes

We can override these metrics to custom ones (if required), example: VM based provisioners where default "kubelet_" metrics might not be relevant.

```
// static Flags (command-line) (needs Pod restart if changed)
# "ticker/runner" interval, default 10 mins
polling-interval: <INTERVAL>

# default "true"
metrics-exporter-enabled: <VALUE>  
     
# where "/metrics" endpoint will be exposed for Prometheus scraping, default ":8080"
metrics-exporter-bind-address: <ADDRESS>

---

// dynamic ConfigMap (can be fetched per tick/run, no Pod restart)
data:
  config.yaml: |
    metrics-source:
      # required only if "use-k8s-metrics-api" is set to "false"
      # URL from where metrics will to be fetched for resize calculations
      prometheus-url: <URL>

      # path to CA bundle used to sign the Prometheus server
      prometheus-ca-bundle-path: <PATH>

      # use K8s metrics API instead of Prometheus, default "false"
      use-k8s-metrics-api: <VALUE>

      # optional
      # override (with custom metrics)
      provisioners:
        <ebs.csi.aws.com>:
          metrics-available-bytes-query: <CUSTOM_METRIC>
          metrics-capacity-bytes-query: <CUSTOM_METRIC>

        <pd.csi.storage.gke.io>:
          metrics-available-bytes-query: <CUSTOM_METRIC>
          metrics-capacity-bytes-query: <CUSTOM_METRIC>
```

### Monitoring/Metrics
TBA

## Design Related Questions/Answers

**Q. Do we include inode threshold ?**
A. Yes.

**Q. Can we avoid fetching ResourceQuota and ClusterResourceQuota during reconciliation ?**
A. Resizer read these resources to prevent patch calls if quota already exceeded, to reduce the load on the API server.

**Q. Do we patch one PVC at a time, or patch them concurrently via grouping ?**
A. Batch and each batch will run as separate workers.

**Q. Do we have a webhook to prevent creating multiple policies that target the same PVC(s) ?**
A. Not at a moment, but policy reports the phase/conditions accordingly.

**Q. Do we block policy deletion in any case ?**
A. No, not needed. Expansion can be disabled either by deleting the policy or, by simply removing the label from the PVC. No new mechanism needs to be added for it.

**Q. Do we check based on StorageClass (allowVolumeExpansion = true) ?**
A. Yes. There are other checks as well, like based on quota, provisioner etc.

## Important Edge Cases
**Insufficient storage provider resources** - When a resize fails due to lack of resources on the storage provider side.
If needed, controller could theoritically leverage [Recovery From Volume Expansion Failure (GA)](https://kubernetes.io/blog/2025/09/19/kubernetes-v1-34-recover-expansion-failure/) K8s feature, though not implementing/testing this for now. Currently, it's out of scope.

**Capacity runs out due to "thin" provisioning** - Resizer can't predict this synchronously. Each storage vendor/provider needs to handle this, e.g., they can have Alerts on the storage level for such cases.

**PVCs (e.g., VMs) that do not report correct capacity via default "kubelet" metrics** - Resizer can fetch metrics from any endpoint URL (Prometheus/K8s), and the query itself is also configurable/overridable (via a ConfigMap).
Also, expansion is opt-in. So, we have "provisioner" spec as well as per-PVC level "selectors". 
Example: not creating a policy for such VM based provisioners or not labelling the PVCs etc.

**Multiple policies targeting same PVCs** - Policy will report phase/conditions accordingly. PVC resize will only be managed by a single active policy at a time.

**Metrics source down or stale metrics** - Resizer can report policy level conditions, Events and logs accordingly.
