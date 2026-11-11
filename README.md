# pvc-autoresizer
Automating PersistentVolumeClaim (PVC) expansion by periodically monitoring and resizing based on capacity or number of inodes usage.

## Description
Please refer [docs/resizer-design.md](./docs/resizer-design.md) for more details.

## Usage
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

### Configuration Fields

| Field | Description | Default Value | Required |
|-------|-------------|---------------|----------|
| `spec.provisioner` | CSI driver name (e.g., `ebs.csi.aws.com`, `pd.csi.storage.gke.io`). Targets PVCs using this provisioner. **Immutable after creation.** | - | Yes |
| `spec.suspend` | Pause auto-expansion for all targeted PVCs | `false` | No |
| `spec.selectors.namespace.labelSelector` | Kubernetes label selector for Namespaces | All Namespaces | No |
| `spec.selectors.namespace.nameRegexSelector` | Regular expression to match Namespace names | All Namespaces | No |
| `spec.selectors.persistentVolumeClaim.labelSelector` | Kubernetes label selector for PVCs | All PVCs | No |
| `spec.selectors.persistentVolumeClaim.nameRegexSelector` | Regular expression to match PVC names | All PVCs | No |
| `spec.expansionThresholds.usagePercentage` | Disk space usage percentage (1-100) that triggers expansion | `80` | No |
| `spec.expansionThresholds.usageInodePercentage` | Inode usage percentage (1-100) that triggers expansion | `80` | No |
| `spec.expansionThresholds.increasePercentage` | Percentage (1-100) to increase current size by | `10` | No |
| `spec.expansionThresholds.increaseAbsolute` | Absolute storage quantity to add (e.g., `50Gi`, `100Gi`). **Takes precedence over `increasePercentage` when set.** | Not set | No |
| `spec.expansionThresholds.maxCapacity` | Maximum allowed capacity limit (e.g., `2Ti`, `5Ti`) | No limit | No |

### Status Fields

| Field | Description | Values |
|-------|-------------|--------|
| `status.matchedPVCsCount` | Total number of PVCs currently targeted by this policy | Integer (count) |
| `status.phase` | Current state of the policy | `Ready` - Policy is active and monitoring PVCs<br>`Suspended` - Policy is paused (via `spec.suspend: true`)<br>`Duplicate` - Multiple policies target the same PVC(s) |

## Getting Started

### Prerequisites
- go version v1.24.6+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/pvc-autoresizer:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/pvc-autoresizer:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/pvc-autoresizer:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/pvc-autoresizer/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v2-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

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

