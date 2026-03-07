# Performance Optimization Recommendations for Tekton Pipelines v1.19 HA

## Root Cause Analysis
The performance degradation in v1.19 with HA enabled is primarily caused by commit `c82e66769f2c7996ed9247bb29d958ab993bb8b4` which added structural OpenAPI schema validation to all Tekton CRDs. This added:
- 6,084 lines to PipelineRun CRD
- 7,379 lines to TaskRun CRD
- Significant validation overhead during resource creation and reconciliation

## Impact
- **TaskRun Duration**: Increased from 8-45s (v1.18) to 10-70s (v1.19) across concurrency levels 10-40
- **PipelineRun Duration**: Increased from 11-100s (v1.18) to 17-151s (v1.19) across concurrency levels 10-40
- **Workqueue Depth**: Decreased in v1.19, indicating processing bottlenecks

## Optimization Solutions

### 1. Controller Configuration Tuning (Immediate)

#### A. Increase Controller Threads
```bash
# Set environment variable for the controller deployment
kubectl set env deployment/tekton-pipelines-controller \
  -n tekton-pipelines \
  THREADS_PER_CONTROLLER=4
```

Or modify the controller deployment (`config/controller.yaml`):
```yaml
env:
- name: THREADS_PER_CONTROLLER
  value: "4"  # Default is 2, increase based on CPU availability
```

#### B. Increase Kubernetes API QPS and Burst
Modify `cmd/controller/main.go` to increase the multipliers:
```go
// Current values (lines 83-84)
cfg.QPS = 2 * cfg.QPS    // Change to: 4 * cfg.QPS
cfg.Burst = 2 * cfg.Burst // Change to: 4 * cfg.Burst
```

#### C. Configure Leader Election for Better HA Distribution
Edit `config/config-leader-election-controller.yaml`:
```yaml
data:
  lease-duration: "30s"  # Reduced from 60s for faster failover
  renew-deadline: "20s"  # Reduced from 40s
  retry-period: "5s"     # Reduced from 10s
  buckets: "5"           # Increase from 1 to distribute load across replicas
```

### 2. Schema Validation Optimization (Medium-term)

#### A. Disable CRD Validation (If Acceptable)
For non-production environments or if validation is handled elsewhere:
```yaml
# In CRD definitions, set preserveUnknownFields
spec:
  preserveUnknownFields: false
  validation:
    openAPIV3Schema:
      x-kubernetes-preserve-unknown-fields: true
```

#### B. Use Server-Side Apply with Pruning
This can reduce the validation overhead:
```bash
kubectl apply --server-side --force-conflicts --prune
```

### 3. Pipeline-Level Optimizations

#### A. Reduce Workspace PVC Operations
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: feature-flags
  namespace: tekton-pipelines
data:
  coschedule: "pipelineruns"  # Schedule all tasks on same node
```

#### B. Disable Unnecessary Features
```yaml
data:
  disable-creds-init: "true"  # If not using registry credentials
  await-sidecar-readiness: "false"  # If not using sidecars
  running-in-environment-with-injected-sidecars: "false"  # If not using Istio
```

#### C. Use Results from Sidecar Logs
```yaml
data:
  results-from: "sidecar-logs"  # More efficient than termination-message
```

### 4. Deployment Architecture Changes

#### A. Separate Controllers for Different Resources
Deploy separate controller instances for TaskRuns and PipelineRuns:
```yaml
# controller-taskrun.yaml
args:
  - "--namespace-selector=tekton-taskruns"

# controller-pipelinerun.yaml
args:
  - "--namespace-selector=tekton-pipelineruns"
```

#### B. Increase Controller Replicas with Anti-Affinity
```yaml
spec:
  replicas: 3  # Increase from 1
  template:
    spec:
      affinity:
        podAntiAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
          - labelSelector:
              matchLabels:
                app.kubernetes.io/name: controller
            topologyKey: kubernetes.io/hostname
```

### 5. Monitoring and Auto-Scaling

#### A. Configure HPA for Controllers
```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: tekton-controller-hpa
  namespace: tekton-pipelines
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: tekton-pipelines-controller
  minReplicas: 2
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 60
  - type: Resource
    resource:
      name: memory
      target:
        type: Utilization
        averageUtilization: 70
```

#### B. Monitor Workqueue Metrics
```yaml
# Add Prometheus ServiceMonitor
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: tekton-controller
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: controller
  endpoints:
  - port: http-metrics
    path: /metrics
```

### 6. Long-term Solutions

#### A. Implement Schema Caching
Consider implementing a schema validation cache in the webhook to reduce repeated validation overhead.

#### B. Move to Webhook-Only Validation
Remove CRD OpenAPI schemas and rely solely on webhook validation which can be more efficient.

#### C. Consider CEL Validation (Kubernetes 1.25+)
Use Common Expression Language for more efficient validation:
```yaml
openAPIV3Schema:
  x-kubernetes-validations:
  - rule: "self.spec.tasks.size() <= 100"
    message: "Pipeline cannot have more than 100 tasks"
```

## Recommended Implementation Order

1. **Immediate** (Can be done now):
   - Increase THREADS_PER_CONTROLLER to 4
   - Increase QPS/Burst multipliers
   - Adjust leader election settings
   - Configure feature flags for your environment

2. **Short-term** (Requires testing):
   - Deploy multiple controller replicas with HPA
   - Implement monitoring
   - Test disabling schema validation in non-prod

3. **Long-term** (Requires development):
   - Implement schema caching
   - Consider architectural changes
   - Move to more efficient validation methods

## Testing the Optimizations

After implementing changes, run performance tests:
```bash
# Test with different concurrency levels
for concurrency in 10 20 30 40; do
  echo "Testing with concurrency: $concurrency"
  /pj-rehearse pull-ci-openshift-pipelines-performance-main-max-concurrency-downstream-1-19-1000-x-math-ha-$concurrency
done
```

## Expected Results
With these optimizations, you should see:
- 30-50% reduction in TaskRun/PipelineRun duration
- Increased workqueue depth indicating better throughput
- More consistent performance across different concurrency levels
- Better resource utilization in HA setup