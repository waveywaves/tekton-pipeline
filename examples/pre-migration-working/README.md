# Pre-Migration Working Examples

This directory contains working examples that use **Tekton Hub** resolver. These are set up to run successfully in your cluster so you can practice the migration to Artifact Hub.

## Current Setup

- Hub resolver is configured to use Tekton Hub (`default-type: tekton`)
- All resources reference tasks from the Tekton Hub catalog
- Resources are ready to be migrated to Artifact Hub

## Files

1. **01-taskrun-git-clone.yaml** - TaskRun using git-clone task from Tekton Hub
2. **02-taskrun-wget.yaml** - TaskRun using wget task from Tekton Hub
3. **03-pipeline-with-hub-tasks.yaml** - Pipeline definition that references Hub tasks
4. **04-pipelinerun.yaml** - PipelineRun that executes the pipeline

## Deploy Resources

```bash
# Deploy the pipeline definition first
kubectl apply -f 03-pipeline-with-hub-tasks.yaml

# Run individual TaskRuns
kubectl create -f 01-taskrun-git-clone.yaml
kubectl create -f 02-taskrun-wget.yaml

# Run the full pipeline
kubectl create -f 04-pipelinerun.yaml
```

## Watch Progress

```bash
# Watch TaskRuns
kubectl get taskruns -w

# Watch PipelineRun
kubectl get pipelineruns -w

# View logs
kubectl logs -f <pod-name>
```

## Run Pre-Migration Assessment

From the repository root:

```bash
cd examples/pre-migration-working

# Find all files using hub resolver
echo "Files using hub resolver:"
find . -type f \( -name "*.yaml" -o -name "*.yml" \) -exec grep -l "resolver: hub" {} \;

# Identify files that need migration (using deprecated Tekton Hub)
echo -e "\nFiles needing migration (Tekton Hub):"
find . -type f \( -name "*.yaml" -o -name "*.yml" \) -exec grep -l "resolver: hub" {} \; | xargs grep -l "value: tekton\|value: Tekton" 2>/dev/null

# Count resources by type
echo -e "\nResources needing migration:"
find . -type f \( -name "*.yaml" -o -name "*.yml" \) -exec grep -l "value: Tekton" {} \; 2>/dev/null | xargs grep "^kind:" | awk '{print $2}' | sort | uniq -c

# Check cluster hub resolver configuration
echo -e "\nHub resolver configuration:"
kubectl get configmap hubresolver-config -n tekton-pipelines-resolvers -o jsonpath='{.data.default-type}'
```

## Migration Steps

Follow the steps in `docs/hub-migration.md` to migrate these resources to Artifact Hub:

1. Change `catalog: Tekton` to `catalog: tekton-catalog-tasks`
2. Update version format to full semver (e.g., `"0.9"` → `"0.9.0"`)
3. Optionally remove the `type` parameter (defaults to `artifact`)
4. Update the hub resolver config to `default-type: artifact`

## After Migration

Once migrated, your resources will use Artifact Hub instead of the deprecated Tekton Hub.
