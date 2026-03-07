---
linkTitle: "Migrating from Tekton Hub to Artifact Hub"
weight: 4001
---

# Migrating from Tekton Hub to Artifact Hub

- [Overview](#overview)
- [Why Migrate](#why-migrate)
- [Impact Assessment](#impact-assessment)
- [Pre-Migration Checklist](#pre-migration-checklist)
- [Migration Steps](#migration-steps)
  - [Step 1: Identify Hub Resolver References](#step-1-identify-hub-resolver-references)
  - [Step 2: Update Type Parameter](#step-2-update-type-parameter)
  - [Step 3: Update Catalog Names](#step-3-update-catalog-names)
  - [Step 4: Verify Version Format](#step-4-verify-version-format)
  - [Step 5: Test and Validate](#step-5-test-and-validate)
- [Catalog Mapping Reference](#catalog-mapping-reference)
- [Migration Examples](#migration-examples)
  - [Task Reference Migration](#task-reference-migration)
  - [Pipeline Reference Migration](#pipeline-reference-migration)
  - [StepAction Reference Migration](#stepaction-reference-migration)
- [OpenShift Pipelines Considerations](#openshift-pipelines-considerations)
- [Private Artifact Hub Instances](#private-artifact-hub-instances)
- [Troubleshooting](#troubleshooting)
- [Backwards Compatibility](#backwards-compatibility)
- [Additional Resources](#additional-resources)

## Overview

This guide helps you migrate from the deprecated [Tekton Hub](https://hub.tekton.dev/) to [Artifact Hub](https://artifacthub.io/) for discovering and referencing Tekton resources (Tasks, Pipelines, and StepActions).

**Important**: The hub resolver now defaults to **Artifact Hub** (`type: artifact`). Tekton Hub is deprecated and requires additional configuration (`TEKTON_HUB_API` environment variable) to work. Users should migrate to Artifact Hub as soon as possible.

## Why Migrate

- **Tekton Hub is deprecated**: The Tekton Hub service (https://hub.tekton.dev/) is being phased out
- **Artifact Hub is now the default**: The hub resolver defaults to Artifact Hub (`type: artifact`)
- **Tekton Hub requires additional configuration**: Using Tekton Hub now requires setting the `TEKTON_HUB_API` environment variable in the hub resolver deployment, otherwise resolution will fail
- **Future removal**: Tekton Hub support will be removed in a future Tekton Pipelines release
- **Better ecosystem integration**: Artifact Hub supports multiple projects beyond Tekton
- **Improved discovery**: Better search, filtering, and package management capabilities

See the [deprecation notice](https://github.com/tektoncd/hub/issues/667) for more details.

## Impact Assessment

Migration from Tekton Hub to Artifact Hub affects:

- **PipelineRuns** that reference Tasks or Pipelines using the hub resolver
- **TaskRuns** that reference Tasks using the hub resolver
- **Pipelines** that reference Tasks using the hub resolver
- **Tasks** that reference StepActions using the hub resolver

The migration is **non-breaking** in the short term as both hubs are currently supported, but you should migrate proactively.

## Pre-Migration Assessment

Before starting your migration, run these commands to understand what needs to be changed:

```bash
# Find all files using hub resolver
echo "Files using hub resolver:"
find . -type f \( -name "*.yaml" -o -name "*.yml" \) -exec grep -l "resolver: hub" {} \;

# Identify files that need migration (using deprecated Tekton Hub)
echo -e "\nFiles needing migration (Tekton Hub):"
find . -type f \( -name "*.yaml" -o -name "*.yml" \) -exec grep -l "resolver: hub" {} \; | xargs grep -l "value: tekton\|value: Tekton"

# Count resources by type
echo -e "\nResources needing migration:"
find . -type f \( -name "*.yaml" -o -name "*.yml" \) -exec grep -l "value: tekton\|value: Tekton" {} \; | xargs grep "^kind:" | awk '{print $2}' | sort | uniq -c

# Check cluster hub resolver configuration (if cluster is accessible)
echo -e "\nHub resolver configuration:"
kubectl get configmap hubresolver-config -n tekton-pipelines -o jsonpath='{.data.default-type}' 2>/dev/null || \
kubectl get configmap hubresolver-config -n tekton-pipelines-resolvers -o jsonpath='{.data.default-type}' 2>/dev/null || \
echo "Hub resolver config not found (cluster may not be accessible)"
```

**Example output:**
```
Files using hub resolver:
./pipelines/build-taskrun.yaml
./pipelines/deploy-pipelinerun.yaml
./tasks/custom-task.yaml

Files needing migration (Tekton Hub):
./pipelines/build-taskrun.yaml
./pipelines/deploy-pipelinerun.yaml

Resources needing migration:
      2 kind: TaskRun
      1 kind: PipelineRun

Hub resolver configuration:
artifact
```

This tells you:
- **Total files** using hub resolver
- **Which specific files** need migration
- **Resource types** affected (TaskRun, PipelineRun, etc.)
- **Cluster configuration** status

## Quick Migration Summary

Since **Artifact Hub is now the default**, migration is extremely simple:

### Simplest Migration (If using default catalogs)

If you were using the default `catalog: Tekton`, just **remove the `type: tekton` parameter** - that's it!

```yaml
# Before (Tekton Hub)
taskRef:
  resolver: hub
  params:
    - name: type
      value: tekton      # ← Just remove this line!
    # catalog: Tekton was the default
    - name: kind
      value: task
    - name: name
      value: git-clone

# After (Artifact Hub) - Everything else is automatic!
taskRef:
  resolver: hub
  params:
    # type: artifact (automatic default)
    # catalog: tekton-catalog-tasks (automatic default for tasks)
    - name: kind
      value: task
    - name: name
      value: git-clone
```

**Why this works**: Both `type` and `catalog` have defaults that automatically change:
- `type` defaults to `artifact` (was previously `tekton` in old configs)
- `catalog` defaults based on `kind`:
  - `kind: task` → defaults to `tekton-catalog-tasks`
  - `kind: pipeline` → defaults to `tekton-catalog-pipelines`

### Manual Migration (If using custom catalogs)

If you explicitly specified `catalog: Tekton`, you need to update it:

```yaml
# Before (Tekton Hub)
params:
  - name: type
    value: tekton        # Remove this
  - name: catalog
    value: Tekton        # Change to tekton-catalog-tasks
  - name: name
    value: git-clone

# After (Artifact Hub)
params:
  # type: artifact is implicit
  - name: catalog
    value: tekton-catalog-tasks
  - name: name
    value: git-clone
```

## Migration Steps

### Step 1: Identify Hub Resolver References

Locate all resources using the hub resolver. Look for patterns like:

```yaml
taskRef:
  resolver: hub
  params:
    - name: type
      value: tekton  # This indicates Tekton Hub (deprecated)
```

### Step 2: Remove Type Parameter

**Important**: Artifact Hub is now the **default** hub type. You only need to **remove** the `type: tekton` parameter - you don't need to add `type: artifact`.

**Before (Tekton Hub - Deprecated):**
```yaml
params:
  - name: type
    value: tekton
  - name: catalog
    value: Tekton
  - name: name
    value: git-clone
```

**After (Artifact Hub - Recommended):**
```yaml
params:
  # type parameter removed - defaults to artifact
  - name: catalog
    value: tekton-catalog-tasks
  - name: name
    value: git-clone
```

**Note**: If you explicitly specify `type: artifact`, it will work, but it's redundant since `artifact` is the default.

### Step 3: Update Catalog Names

Update catalog names to match Artifact Hub conventions:

| Tekton Hub Catalog | Artifact Hub Catalog | Resource Type |
|--------------------|---------------------|---------------|
| `Tekton` | `tekton-catalog-tasks` | Tasks |
| `Tekton` | `tekton-catalog-pipelines` | Pipelines |
| Custom catalogs | Check Artifact Hub for equivalent | Varies |

### Step 4: Verify Version Format

Ensure version strings use full semantic versioning:

- **Tekton Hub**: Often uses shortened versions (e.g., `"0.8"`, `"1.0"`)
- **Artifact Hub**: Requires full semver (e.g., `"0.8.0"`, `"1.0.0"`)

The hub resolver handles version conversion automatically, but using full semver is recommended.

### Step 5: Test and Validate

Test your migrated resources in a development environment:

```bash
# Create a test TaskRun
kubectl create -f migrated-taskrun.yaml

# Verify resolution
kubectl describe taskrun test-run | grep -A10 "Resolution"

# Check for errors
kubectl get taskrun test-run -o yaml | grep -A10 "conditions"
```

## Automated Migration (Recommended)

### Option 1: Simplest Migration (If using default catalogs)

If your files rely on default catalog values, just remove the `type: tekton` parameter:

```bash
# Backup and remove type: tekton from all YAML files
find . -type f \( -name "*.yaml" -o -name "*.yml" \) -exec grep -l "type.*tekton" {} \; | while read file; do
  cp "$file" "$file.bak"
  sed -i.tmp '/- name: type/,/value: tekton/d' "$file"
  rm -f "$file.tmp"
  echo "Migrated: $file (backup: $file.bak)"
done
```

**What it does:**
- Finds all YAML files with `type: tekton`
- Creates `.bak` backup of each file
- Removes `type: tekton` parameter lines
- Catalog automatically defaults to correct value based on `kind`

### Option 2: Full Migration (If explicitly setting catalog: Tekton)

If your files explicitly specify `catalog: Tekton`, also update the catalog:

```bash
# Backup and migrate all YAML files in current directory
find . -type f \( -name "*.yaml" -o -name "*.yml" \) -exec grep -l "resolver: hub" {} \; | while read file; do
  cp "$file" "$file.bak"
  sed -i.tmp '/- name: type/,/value: tekton/d' "$file"
  sed -i.tmp 's/value: Tekton$/value: tekton-catalog-tasks/' "$file"
  rm -f "$file.tmp"
  echo "Migrated: $file (backup: $file.bak)"
done
```

**What it does:**
- Finds all YAML files with `resolver: hub`
- Creates `.bak` backup of each file
- Removes `type: tekton` parameter lines
- Changes explicit `catalog: Tekton` to `tekton-catalog-tasks`

**After running:**
```bash
# Review changes
git diff

# If using Option 2 and have pipelines/stepactions, update catalog names:
# tekton-catalog-tasks → tekton-catalog-pipelines (for pipelines)
# tekton-catalog-tasks → tekton-catalog-stepactions (for stepactions)

# Check for version format warnings and update if needed
grep -r "version:" --include="*.yaml" . | grep -v "0\.[0-9]\+\.[0-9]\+"

# Test and commit
kubectl apply -f ./path/to/files
git add .
git commit -m "Migrate from Tekton Hub to Artifact Hub"
```

**Note:** Option 1 is simpler and works for most cases. Use Option 2 only if you explicitly set `catalog: Tekton` in your YAML files.

## Catalog Mapping Reference

Complete catalog mapping for common Tekton resources:

| Resource Kind | Tekton Hub Catalog | Artifact Hub Catalog | Example Task |
|---------------|-------------------|---------------------|--------------|
| Task | `Tekton` | `tekton-catalog-tasks` | `git-clone`, `buildah`, `kaniko` |
| Pipeline | `Tekton` | `tekton-catalog-pipelines` | `buildpacks`, `docker-build` |
| StepAction | `Tekton` | `tekton-catalog-stepactions` | Various step actions |

## Migration Examples

### Task Reference Migration

**Before (Tekton Hub):**
```yaml
apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  name: git-clone-run
spec:
  taskRef:
    resolver: hub
    params:
      - name: type
        value: tekton
      - name: catalog
        value: Tekton
      - name: kind
        value: task
      - name: name
        value: git-clone
      - name: version
        value: "0.8"
  params:
    - name: url
      value: https://github.com/tektoncd/pipeline.git
    - name: revision
      value: main
  workspaces:
    - name: output
      emptyDir: {}
```

**After (Artifact Hub):**
```yaml
apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  name: git-clone-run
spec:
  taskRef:
    resolver: hub
    params:
      - name: catalog
        value: tekton-catalog-tasks
      - name: kind
        value: task
      - name: name
        value: git-clone
      - name: version
        value: "0.8.0"
  params:
    - name: url
      value: https://github.com/tektoncd/pipeline.git
    - name: revision
      value: main
  workspaces:
    - name: output
      emptyDir: {}
```

**Key Changes:**
- Removed `type: tekton` parameter (defaults to `artifact`)
- Changed `catalog` from `Tekton` to `tekton-catalog-tasks`
- Updated version from `"0.8"` to `"0.8.0"` (full semver)

### Pipeline Reference Migration

**Before (Tekton Hub):**
```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: buildpacks-run
spec:
  pipelineRef:
    resolver: hub
    params:
      - name: type
        value: tekton
      - name: catalog
        value: Tekton
      - name: kind
        value: pipeline
      - name: name
        value: buildpacks
      - name: version
        value: "0.1"
  params:
    - name: APP_IMAGE
      value: my-registry/my-app:latest
  workspaces:
    - name: source-workspace
      volumeClaimTemplate:
        spec:
          accessModes:
            - ReadWriteOnce
          resources:
            requests:
              storage: 1Gi
```

**After (Artifact Hub):**
```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: buildpacks-run
spec:
  pipelineRef:
    resolver: hub
    params:
      - name: catalog
        value: tekton-catalog-pipelines
      - name: kind
        value: pipeline
      - name: name
        value: buildpacks
      - name: version
        value: "0.1.0"
  params:
    - name: APP_IMAGE
      value: my-registry/my-app:latest
  workspaces:
    - name: source-workspace
      volumeClaimTemplate:
        spec:
          accessModes:
            - ReadWriteOnce
          resources:
            requests:
              storage: 1Gi
```

**Key Changes:**
- Removed `type: tekton` parameter
- Changed `catalog` from `Tekton` to `tekton-catalog-pipelines`
- Updated version to full semver format

### StepAction Reference Migration

**Before (Tekton Hub):**
```yaml
apiVersion: tekton.dev/v1
kind: Task
metadata:
  name: task-with-stepaction
spec:
  steps:
    - name: step-with-action
      ref:
        resolver: hub
        params:
          - name: type
            value: tekton
          - name: catalog
            value: Tekton
          - name: kind
            value: stepaction
          - name: name
            value: step-action-name
          - name: version
            value: "1.0"
```

**After (Artifact Hub):**
```yaml
apiVersion: tekton.dev/v1
kind: Task
metadata:
  name: task-with-stepaction
spec:
  steps:
    - name: step-with-action
      ref:
        resolver: hub
        params:
          - name: catalog
            value: tekton-catalog-stepactions
          - name: kind
            value: stepaction
          - name: name
            value: step-action-name
          - name: version
            value: "1.0.0"
```

**Key Changes:**
- Removed `type: tekton` parameter
- Changed `catalog` to `tekton-catalog-stepactions`
- Updated version to full semver format

## OpenShift Pipelines Considerations

[OpenShift Pipelines](https://docs.openshift.com/pipelines/latest/about/understanding-openshift-pipelines.html) is Red Hat's enterprise distribution of Tekton. The hub migration process is identical, but with these considerations:

### Installation Differences

OpenShift Pipelines is installed via the **Red Hat OpenShift Pipelines Operator**, not direct YAML:

1. **Hub resolver is included** in the standard installation
2. **ConfigMap location** is the same: `tekton-pipelines-resolvers` namespace
3. **Operator manages** the resolver deployment automatically

### Verifying Hub Resolver in OpenShift

```bash
# Check if hub resolver is enabled
oc get configmap hubresolver-config -n openshift-pipelines

# View hub resolver configuration
oc describe configmap hubresolver-config -n openshift-pipelines
```

### OpenShift Migration Example

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: openshift-build-deploy
  namespace: my-app
spec:
  pipelineRef:
    resolver: hub
    params:
      # Use Artifact Hub (new)
      - name: catalog
        value: tekton-catalog-tasks
      - name: name
        value: buildah
      - name: version
        value: "0.6.0"
  params:
    - name: IMAGE
      value: image-registry.openshift-image-registry.svc:5000/my-app/app:latest
  workspaces:
    - name: source
      persistentVolumeClaim:
        claimName: app-source-pvc
```

### OpenShift-Specific Features

- **Integrated UI**: View resolved resources in the OpenShift Console
- **Service Mesh**: Hub resolver works with OpenShift Service Mesh
- **Private registries**: Configure custom Artifact Hub instances for air-gapped environments
- **RBAC**: Same service account permissions apply

## Private Artifact Hub Instances

For air-gapped or private environments, you can configure a custom Artifact Hub instance:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: hubresolver-config
  namespace: tekton-pipelines-resolvers
data:
  # Default Artifact Hub URL
  default-artifact-hub-url: "https://artifacthub.io"

  # For private instances, update to your URL
  # default-artifact-hub-url: "https://hub.mycompany.internal"
```

**Important**: When using a private Artifact Hub:
1. Ensure network connectivity from resolver pods
2. Configure TLS certificates if using HTTPS
3. Set up authentication if required
4. Verify catalog names match your private hub's structure

## Troubleshooting

### Common Issues and Solutions

#### Issue: "Resource not found in Artifact Hub"

**Cause**: Task/Pipeline name or version doesn't exist in Artifact Hub catalog.

**Solution**:
1. Browse [Artifact Hub](https://artifacthub.io/) to verify resource exists
2. Check exact spelling of resource name
3. Verify version format (use full semver: `"0.8.0"` not `"0.8"`)
4. Ensure correct catalog name (`tekton-catalog-tasks` vs `tekton-catalog-pipelines`)

#### Issue: "Resolution failed with 404 error"

**Cause**: Incorrect catalog name or API endpoint.

**Solution**:
```bash
# Verify hub resolver configuration
kubectl get configmap hubresolver-config -n tekton-pipelines-resolvers -o yaml

# Check resolver logs
kubectl logs -n tekton-pipelines-resolvers -l app=hubresolver
```

#### Issue: "Version mismatch errors"

**Cause**: Version format differences between Tekton Hub and Artifact Hub.

**Solution**: Always use full semantic versioning:
```yaml
# Correct
- name: version
  value: "0.8.0"

# May cause issues
- name: version
  value: "0.8"
```

#### Issue: "Cannot connect to Artifact Hub"

**Cause**: Network connectivity or firewall issues.

**Solution**:
1. Verify resolver pods can reach `https://artifacthub.io`
2. Check proxy settings in your cluster
3. Ensure firewall rules allow HTTPS egress
4. Test connectivity:
   ```bash
   kubectl run test-curl --image=curlimages/curl -it --rm -- \
     curl -v https://artifacthub.io/api/v1/packages/tekton-task/tekton-catalog-tasks/git-clone/0.8.0
   ```

#### Issue: "Please configure TEKTON_HUB_API env variable to use tekton type"

**Cause**: Attempting to use Tekton Hub without the required `TEKTON_HUB_API` environment variable configured.

**Solution**:
Tekton Hub requires additional configuration. You have two options:

**Option 1: Migrate to Artifact Hub (Recommended)**
- Remove `type: tekton` from your YAML files
- Update catalog names to Artifact Hub conventions
- Artifact Hub works by default without additional configuration

**Option 2: Configure Tekton Hub API**
If you must continue using Tekton Hub, configure the hub resolver deployment.

**Recommended: Using kubectl patch (survives restarts)**
```bash
# Patch the deployment to set TEKTON_HUB_API
kubectl patch deployment tekton-pipelines-remote-resolvers \
  -n tekton-pipelines-resolvers \
  --type=json \
  -p='[{"op": "replace", "path": "/spec/template/spec/containers/0/env/7/value", "value": "https://api.hub.tekton.dev"}]'

# Verify the change took effect
kubectl get deployment tekton-pipelines-remote-resolvers \
  -n tekton-pipelines-resolvers \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="TEKTON_HUB_API")].value}'
```

**Alternative: Using kubectl set env (simpler syntax)**
```bash
# Set the environment variable
kubectl set env deployment/tekton-pipelines-remote-resolvers \
  -n tekton-pipelines-resolvers \
  TEKTON_HUB_API="https://api.hub.tekton.dev"

# Pods will automatically restart with the new configuration
```

**Not Recommended: Using kubectl edit (manual editing)**
```bash
# Edit the deployment interactively (harder to script/automate)
kubectl edit deployment tekton-pipelines-remote-resolvers -n tekton-pipelines-resolvers

# Find and update the TEKTON_HUB_API env var from "" to "https://api.hub.tekton.dev"
```

**Note**: Tekton Hub is deprecated and will be removed in a future release. Migrating to Artifact Hub is strongly recommended. These configuration changes may be overwritten during Tekton upgrades.

#### Issue: "Still using Tekton Hub after migration"

**Cause**: Cached resolver configuration or missed references.

**Solution**:
1. Restart hub resolver pods:
   ```bash
   kubectl rollout restart deployment hubresolver -n tekton-pipelines-resolvers
   ```
2. Double-check all YAML files for `type: tekton` references
3. Clear any local kubectl caches

### Validation Commands

```bash
# Verify TaskRun resolution
kubectl get taskrun <name> -o jsonpath='{.status.taskSpec.steps[0].name}'

# Check PipelineRun resolution
kubectl get pipelinerun <name> -o jsonpath='{.status.pipelineSpec.tasks[*].name}'

# View resolver events
kubectl get events -n tekton-pipelines-resolvers --sort-by='.lastTimestamp'

# Test hub resolver directly
kubectl run hub-test --image=curlimages/curl --rm -it -- \
  curl https://artifacthub.io/api/v1/packages/tekton-task/tekton-catalog-tasks/git-clone/0.8.0
```

## Backwards Compatibility

- **Current status**: Both Tekton Hub and Artifact Hub are supported
- **Deprecation**: Tekton Hub is officially deprecated
- **Removal timeline**: No specific date set, but users should migrate proactively
- **Beta feature**: Hub resolver is a beta feature subject to changes
- **Recommendation**: Migrate to Artifact Hub immediately to avoid disruption

### Checking Deprecation Status

```bash
# View current deprecations
curl -s https://raw.githubusercontent.com/tektoncd/pipeline/main/docs/deprecations.md | \
  grep -A10 "Tekton Hub"
```

## Additional Resources

- **Artifact Hub**: https://artifacthub.io/
- **Tekton Hub (deprecated)**: https://hub.tekton.dev/
- **Hub Resolver Documentation**: [hub-resolver.md](./hub-resolver.md)
- **Remote Resolution Guide**: [resolution.md](./resolution.md)
- **Bundle Resolver**: [bundle-resolver.md](./bundle-resolver.md)
- **Deprecation Discussion**: https://github.com/tektoncd/hub/issues/667
- **OpenShift Pipelines Documentation**: https://docs.openshift.com/pipelines/latest/

### Related Documentation

- [Migrating from v1beta1 to v1](./migrating-v1beta1-to-v1.md)
- [Resolution Getting Started Guide](./resolution-getting-started.md)
- [Install Guide](./install.md)

---

Except as otherwise noted, the content of this page is licensed under the [Creative Commons Attribution 4.0 License](https://creativecommons.org/licenses/by/4.0/).

Code samples are licensed under the [Apache 2.0 License](https://www.apache.org/licenses/LICENSE-2.0).
