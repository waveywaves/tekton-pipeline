# Tekton Runner: Running Tekton Without Kubernetes Workers

## Executive Summary

This document outlines the design for a Tekton execution service that leverages Tekton's API and workflow definitions while executing tasks outside of Kubernetes. Inspired by vCluster Standalone and Replicated Embedded Cluster, this approach uses a lightweight Kubernetes control plane solely as an API server and state store, while delegating actual task execution to external runtimes like Podman, Dagger, or other CI backends.

### Key Benefits

- **Simplified Infrastructure**: No Kubernetes worker nodes, kubelet, or container runtime in K8s
- **Flexible Execution**: Run tasks locally, in VMs, serverless functions, or any backend
- **Full API Compatibility**: Standard Tekton YAML, kubectl, and tkn CLI work unchanged
- **Lightweight**: Minimal resource footprint suitable for edge, developer laptops, or constrained environments
- **Easy Distribution**: Single binary or packaged installer

### Target Use Cases

- **Local Development**: Test Tekton pipelines on developer laptops without a full cluster
- **Edge Computing**: Deploy to IoT devices, Raspberry Pi, or resource-constrained environments
- **Cost Optimization**: Eliminate Kubernetes worker node infrastructure
- **Regulated Environments**: Execute in specific locations with compliance requirements
- **Legacy Integration**: Bridge Tekton workflows to existing execution infrastructure

---

## 1. Background: Tekton's Managed-By Feature

### What is `managedBy`?

Tekton's TaskRun and PipelineRun specs include an optional `managedBy` field that specifies which controller is responsible for reconciling the resource:

```yaml
apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  name: example-task
spec:
  managedBy: "tekton-runner"  # Custom controller
  taskSpec:
    steps:
    - name: hello
      image: ubuntu
      script: echo "Hello World"
```

### How It Works

The Tekton Pipeline controller includes filter functions that skip resources where `managedBy != "tekton.dev/pipeline"`:

```go
// From pkg/reconciler/taskrun/controller.go
var taskRunFilterManagedBy = func(obj interface{}) bool {
    tr, ok := obj.(*v1.TaskRun)
    if !ok {
        return true
    }
    // Skip TaskRuns managed by other controllers
    if tr.Spec.ManagedBy != nil && *tr.Spec.ManagedBy != pipeline.ManagedBy {
        return false
    }
    return true
}
```

When `managedBy` is set to a custom value, the Tekton controller completely ignores the resource, allowing an external controller to take full responsibility for execution and status updates.

### Existing Uses

**Kubernetes Kueue** (specifically MultiKueue) uses `managedBy` for multi-cluster workload management, implementing queue management, quota enforcement, and cross-cluster scheduling. For example, Kueue sets `spec.runPolicy.managedBy: kueue.x-k8s.io/multikueue` to delegate job management across clusters. Kueue also has experimental integration with Tekton via Plain Pod support, with built-in PipelineRun support under development (as of early 2025). Our use case is orthogonal: we're focused on executing tasks outside Kubernetes entirely, not scheduling within Kubernetes.

### What External Controllers Must Handle

When taking ownership via `managedBy`, the external controller becomes responsible for:

- Creating execution environments (Pods, containers, processes, etc.)
- Updating TaskRun/PipelineRun status through the Kubernetes API
- Handling timeouts, cancellations, and retries
- Managing workspaces and results
- Streaming or storing logs
- Processing step outputs and artifacts

---

## 2. Inspiration: Prior Art

### vCluster Standalone

vCluster Standalone (v0.29+) demonstrates a compelling pattern for packaging Kubernetes as a systemd service:

**Key Features:**
- Single binary installation via curl script
- SystemD service manages K8s control plane lifecycle
- Runs API Server + etcd + Controller Manager + Scheduler
- Can run on bare metal or VMs without a host cluster
- Supports both single-node and HA multi-node deployments

**Installation Example:**
```bash
export VCLUSTER_VERSION="v0.29.0"
curl -sfL https://github.com/loft-sh/vcluster/releases/download/${VCLUSTER_VERSION}/install-standalone.sh | sh -s -- --vcluster-name standalone
```

**What We Learn:**
- Kubernetes control plane can be packaged as a single-binary systemd service
- Users expect simple installation without Kubernetes expertise
- Control plane can run independently of workload execution

### Replicated Embedded Cluster

Replicated Embedded Cluster packages k0s + applications together as a single appliance:

**Key Features:**
- Based on k0s Kubernetes distribution
- Bundles Kubernetes cluster + application + dependencies
- Includes KOTS admin console for configuration
- Built-in air-gap support with embedded image registry
- Velero integration for backup/restore
- Handles updates for both cluster and application

**What We Learn:**
- Enterprise customers want single-installer experiences
- Air-gap support is critical for regulated industries
- Application + cluster lifecycle should be managed together
- Configuration UI (KOTS) simplifies deployment

### Key Takeaways for Tekton Runner

1. **SystemD packaging**: Control plane as a service is proven and familiar
2. **Single binary distribution**: Reduces deployment complexity
3. **Air-gap support**: Important for enterprise/edge scenarios
4. **Update management**: Bundle cluster + application updates
5. **No host cluster required**: Standalone operation is viable

---

## 3. Architecture

### Overview

```
┌──────────────────────────────────────────────────┐
│  SystemD Service: tekton-runner.service          │
│                                                   │
│  ┌────────────────────────────────────────────┐  │
│  │  Lightweight K8s Control Plane             │  │
│  │  (k3s/k0s/MicroShift)                      │  │
│  │  - API Server + etcd                       │  │
│  │  - NO worker nodes                         │  │
│  │  - NO kubelet                              │  │
│  │  - Tekton CRDs installed                   │  │
│  │  - Tekton controllers DISABLED             │  │
│  └────────────────────────────────────────────┘  │
│                                                   │
│  ┌────────────────────────────────────────────┐  │
│  │  Tekton Runner Controller                  │  │
│  │  - Watches TaskRuns/PipelineRuns           │  │
│  │  - Filters: managedBy="tekton-runner"      │  │
│  │  - Updates status via K8s API              │  │
│  └────────────────┬───────────────────────────┘  │
└───────────────────┼───────────────────────────────┘
                    │
                    ├─▶ Podman (rootless containers)
                    ├─▶ Dagger (portable CI/CD engine)
                    ├─▶ Docker
                    ├─▶ Shell/Process execution
                    ├─▶ AWS Lambda
                    └─▶ [Pluggable backends]
```

### Component Breakdown

#### 1. Lightweight Kubernetes Control Plane

The K8s control plane serves **only** as an API server and state store:

- **No worker nodes**: No kubelet, no container runtime in K8s
- **No Pod scheduling**: kube-scheduler can be disabled
- **Minimal resources**: ~100-150MB RAM for control plane only
- **Storage**: sqlite3 (k3s) or etcd (k0s/MicroShift)

**Purpose:**
- Store TaskRun/PipelineRun/Task/Pipeline CRDs
- Provide standard Kubernetes API (enables kubectl, tkn CLI)
- Handle authentication, RBAC, webhooks
- Maintain resource state and history

#### 2. Tekton Runner Controller

Custom controller that:

```go
// Watch TaskRuns with managedBy filter
informer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
    AddFunc: func(obj interface{}) {
        tr := obj.(*v1.TaskRun)
        if tr.Spec.ManagedBy != nil &&
           *tr.Spec.ManagedBy == "tekton-runner" {
            go executeTaskRun(tr, executor)
        }
    },
    UpdateFunc: func(oldObj, newObj interface{}) {
        // Handle cancellations, status updates
    },
})
```

**Responsibilities:**
- Watch for new TaskRuns/PipelineRuns
- Parse TaskSpec and extract steps, params, workspaces
- Route to appropriate execution backend
- Update TaskRun status after each step
- Handle timeouts, cancellations, errors

#### 3. Execution Backends

Pluggable executors implement this interface:

```go
type Executor interface {
    Execute(ctx context.Context, tr *v1.TaskRun) error
    Cancel(ctx context.Context, tr *v1.TaskRun) error
    GetLogs(ctx context.Context, tr *v1.TaskRun, step string) (io.Reader, error)
}
```

**Podman Executor:**
```go
type PodmanExecutor struct {
    socket string
}

func (p *PodmanExecutor) Execute(ctx context.Context, tr *v1.TaskRun) error {
    workspace := createWorkspace(tr.Name)

    for i, step := range tr.Spec.TaskSpec.Steps {
        cmd := exec.CommandContext(ctx, "podman", "run",
            "--rm",
            "-v", workspace+":/workspace:Z",
            "-w", "/workspace",
            step.Image,
            step.Command...,
        )

        output, err := cmd.CombinedOutput()
        updateStepStatus(tr, i, output, err)
    }

    return extractResults(tr, workspace)
}
```

**Dagger Executor:**
```go
type DaggerExecutor struct {
    client *dagger.Client
}

func (d *DaggerExecutor) Execute(ctx context.Context, tr *v1.TaskRun) error {
    for _, step := range tr.Spec.TaskSpec.Steps {
        container := d.client.Container().
            From(step.Image).
            WithWorkdir("/workspace").
            WithExec(step.Command)

        output, err := container.Stdout(ctx)
        // Update status...
    }
}
```

---

## 4. Control Plane Architecture: Pluggable Design

Tekton Runner uses a pluggable control plane architecture to support different deployment models and scale requirements. The control plane serves exclusively as an API server and state store - it provides the Kubernetes API interface for Tekton resources but does **not** execute workloads.

### Architecture Overview

```
┌─────────────────────────────────────────────────┐
│  Tekton Runner (pluggable control plane)        │
│                                                  │
│  ┌────────────────────────────────────────────┐ │
│  │  Control Plane Interface                   │ │
│  │                                             │ │
│  │  type ControlPlane interface {             │ │
│  │    Start(config) error                     │ │
│  │    GetKubeconfig() []byte                  │ │
│  │    GetClient() client.Client               │ │
│  │    Healthy() bool                          │ │
│  │    Stop() error                            │ │
│  │  }                                          │ │
│  └────────────────┬───────────────────────────┘ │
└───────────────────┼───────────────────────────────┘
                    │
      ┌─────────────┼─────────────┐
      │             │             │
      ▼             ▼             ▼
┌──────────┐  ┌──────────┐  ┌──────────┐
│   k3s    │  │   k0s    │  │   kcp    │
│ Phase 1  │  │ Phase 1  │  │ Phase 2+ │
└──────────┘  └──────────┘  └──────────┘
Single-tenant   Enterprise   Multi-tenant
```

### Implementation Strategy

**Phase 1 (MVP - Months 1-4):** Lightweight Kubernetes
- Start with k3s/k0s/MicroShift
- Single-tenant deployments
- Focus on local dev, edge, single-team scenarios

**Phase 2+ (Months 7+):** Multi-Tenant Control Plane
- Add kcp backend for multi-tenant scenarios
- Keep k3s/k0s for single-tenant use cases
- Seamless migration path between backends

---

## 4.1 Phase 1: Lightweight Kubernetes Options

For the initial implementation, Tekton Runner will support three lightweight Kubernetes distributions as control plane backends.

### Comparison

| Feature | k3s | k0s | MicroShift |
|---------|-----|-----|------------|
| **Provider** | Rancher/SUSE | Mirantis | Red Hat |
| **Binary Size** | <70MB | ~150MB | ~300MB+ |
| **Control-Plane Only** | Via --disable-agent | Native controller mode | Supported |
| **Default Storage** | sqlite3 | etcd | etcd |
| **Installation** | curl \| sh | Single binary | dnf install |
| **Maturity** | Very mature | Mature | Newer |
| **Community** | Large | Growing | Red Hat ecosystem |
| **Use Case** | General purpose | Enterprise, Embedded Cluster | Red Hat/OpenShift users |

### Recommendation: k3s

For Tekton Runner, **k3s** is recommended because:

1. **sqlite3 storage**: Simpler than etcd for single-node deployments
2. **Mature & proven**: Large community, extensive documentation
3. **Control-plane-only mode**: Can run without agents using server roles
   ```bash
   # Option 1: Disable components, run minimal server
   k3s server --disable=traefik,servicelb,local-storage,metrics-server

   # Option 2: Control-plane only (requires external etcd or etcd-only node)
   k3s server --disable-etcd --disable-agent --egress-selector-mode=pod
   ```
   **Note**: The `--disable-agent` flag has known issues and may require workarounds. For production, consider using role-based configuration.
4. **Smallest footprint**: <70MB binary, ~100-150MB RAM for control plane
5. **Easy embedding**: Well-documented patterns for embedding in Go

**When to choose alternatives:**
- **k0s**: If using Replicated Embedded Cluster (already based on k0s)
- **MicroShift**: If targeting Red Hat ecosystem or need OpenShift compatibility

---

## 4.2 Phase 2+: kcp for Multi-Tenant Deployments

For multi-tenant scenarios (SaaS platforms, enterprise with hundreds of teams), Tekton Runner will support kcp as an alternative control plane backend.

### What is kcp?

kcp (Kubernetes Control Plane) is a CNCF Sandbox project that provides a horizontally scalable, multi-tenant control plane for Kubernetes-like APIs. Unlike traditional Kubernetes, kcp is designed specifically for API delivery without any execution layer.

**Key Characteristics:**
- **Workspace-based multi-tenancy**: Each tenant gets an isolated logical cluster called a "workspace"
- **No execution layer**: Intentionally excludes Pods, containers, and workload scheduling
- **Horizontal scalability**: Designed to support millions of workspaces across thousands of shards
- **API-first**: Perfect fit for Tekton Runner's "API without execution" model

### Core Concepts

#### 1. Workspaces

Workspaces are kcp's fundamental isolation unit - logical clusters that appear as independent Kubernetes API servers:

```bash
# Create and navigate workspaces (like directories)
kubectl ws :root                          # Go to root
kubectl create-workspace org:acme --enter # Create organization
kubectl create-workspace team-a --enter   # Create team workspace
kubectl ws ..                             # Go to parent
kubectl ws                                # Show current workspace
```

Each workspace has:
- Unique cluster path: `/clusters/<cluster-name>`
- Own API resources, RBAC, and isolation
- Hierarchical organization (root → organization → teams → projects)

#### 2. Sharding Architecture

kcp scales horizontally by distributing workspaces across multiple shard instances:

```
┌─────────────────────────────────────────────┐
│  Front-Proxy (Routes by cluster path)       │
│  - Watches all shards or uses external DB   │
│  - Routes /clusters/<name> to correct shard │
└──────────────┬──────────────────────────────┘
               │
       ┌───────┼────────┐
       ▼       ▼        ▼
   ┌──────┐ ┌──────┐ ┌──────┐
   │Shard1│ │Shard2│ │Shard3│
   │      │ │      │ │      │
   └──────┘ └──────┘ └──────┘
   Workspaces distributed across shards
```

- **Shards**: Individual kcp instances hosting sets of workspaces
- **Front-proxy**: Routes requests to appropriate shards
- **Cache server**: Replicates critical objects (APIExports) for cross-shard discovery
- **Eventual consistency**: No strict synchronization between shards

#### 3. APIExport & APIBinding

kcp uses APIExport/APIBinding to share APIs across workspaces:

```yaml
# Service provider workspace: tekton-system
apiVersion: apis.kcp.io/v1alpha1
kind: APIExport
metadata:
  name: tekton-apis
spec:
  latestResourceSchemas:
  - tekton.dev.taskruns.v1
  - tekton.dev.pipelineruns.v1
  - tekton.dev.tasks.v1
  - tekton.dev.pipelines.v1

---
# Consumer workspace: team-a
apiVersion: apis.kcp.io/v1alpha1
kind: APIBinding
metadata:
  name: tekton
spec:
  reference:
    export:
      path: root:tekton-system
      name: tekton-apis
```

**How it works:**
1. **Provider** defines Tekton CRDs as APIResourceSchemas and exports them via APIExport
2. **Consumers** bind to the export via APIBinding
3. Consumers can now create TaskRuns/PipelineRuns in their workspace
4. Resources remain isolated per workspace

### Tekton Runner with kcp Architecture

```
┌────────────────────────────────────────────────────┐
│  kcp (Multi-tenant control plane)                  │
│                                                     │
│  ┌──────────────────────────────────────────────┐  │
│  │  Root Workspace                              │  │
│  │  └─ tekton-system (service provider)        │  │
│  │      └─ APIExport (Tekton CRDs)             │  │
│  │                                               │  │
│  │  └─ org:acme (organization)                 │  │
│  │      ├─ team-a workspace                    │  │
│  │      │   ├─ APIBinding → tekton-system      │  │
│  │      │   ├─ TaskRuns (team A only)          │  │
│  │      │   └─ PipelineRuns (team A only)      │  │
│  │      │                                       │  │
│  │      └─ team-b workspace                    │  │
│  │          ├─ APIBinding → tekton-system      │  │
│  │          ├─ TaskRuns (team B only)          │  │
│  │          └─ PipelineRuns (team B only)      │  │
│  └──────────────────────────────────────────────┘  │
└─────────────────────┬──────────────────────────────┘
                      │
      ┌───────────────┴───────────────┐
      │                               │
      ▼                               ▼
┌──────────────────┐        ┌──────────────────┐
│ Tekton Runner    │        │ Tekton Runner    │
│ (Team A)         │        │ (Team B)         │
│ - Watches        │        │ - Watches        │
│   team-a         │        │   team-b         │
│   workspace      │        │   workspace      │
│ - managedBy:     │        │ - managedBy:     │
│   "tekton-runner"│        │   "tekton-runner"│
│ - Executes in    │        │ - Executes in    │
│   Podman/Dagger  │        │   Podman/Dagger  │
└──────────────────┘        └──────────────────┘
```

### Setup Process

**1. Install kcp:**
```bash
# Download and run kcp
go install github.com/kcp-dev/kcp/cmd/kcp@latest
kcp start

# Configure kubectl
export KUBECONFIG=.kcp/admin.kubeconfig
```

**2. Create Tekton API provider workspace:**
```bash
# Navigate to root and create system workspace
kubectl ws :root
kubectl create-workspace tekton-system --enter

# Generate APIResourceSchemas from Tekton CRDs
# (using kcp's apigen tool)
apigen --input tekton-crds.yaml --output tekton-schemas.yaml

# Apply schemas and create APIExport
kubectl apply -f tekton-schemas.yaml
kubectl apply -f - <<EOF
apiVersion: apis.kcp.io/v1alpha1
kind: APIExport
metadata:
  name: tekton-apis
spec:
  latestResourceSchemas:
  - tekton.dev.taskruns.v1
  - tekton.dev.pipelineruns.v1
  - tekton.dev.tasks.v1
  - tekton.dev.pipelines.v1
EOF
```

**3. Create tenant workspaces:**
```bash
# Create organization workspace
kubectl ws :root
kubectl create-workspace org:acme --type organization --enter

# Create team workspaces and bind to Tekton APIs
kubectl create-workspace team-a --enter
kubectl apply -f - <<EOF
apiVersion: apis.kcp.io/v1alpha1
kind: APIBinding
metadata:
  name: tekton
spec:
  reference:
    export:
      path: root:tekton-system
      name: tekton-apis
EOF

# Repeat for team-b
kubectl ws ..
kubectl create-workspace team-b --enter
kubectl apply -f tekton-api-binding.yaml
```

**4. Configure Tekton Runner per workspace:**
```yaml
# /etc/tekton-runner/config.yaml
control_plane:
  type: kcp
  kcp:
    workspace: root:org:acme:team-a
    kubeconfig: /path/to/team-a-kubeconfig
    front_proxy: https://front-proxy.kcp.example.com

executor:
  backend: podman
  workspace_base: /var/lib/tekton-runner/workspaces

tekton:
  managed_by: "tekton-runner-team-a"
```

**5. Run Tekton Runner instances:**
```bash
# Team A runner
tekton-runner serve --config /etc/tekton-runner/team-a-config.yaml

# Team B runner (separate instance)
tekton-runner serve --config /etc/tekton-runner/team-b-config.yaml
```

### User Experience

**Team A user:**
```bash
# Set workspace context
kubectl ws root:org:acme:team-a

# Create TaskRun (stays in team-a workspace)
kubectl apply -f - <<EOF
apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  name: build-app
spec:
  managedBy: "tekton-runner"
  taskSpec:
    steps:
    - name: build
      image: golang:1.21
      script: go build ./...
EOF

# Watch execution
tkn taskrun logs build-app -f

# Team A cannot see team B's resources
kubectl get taskruns  # Only sees team-a TaskRuns
```

**Team B has identical experience but isolated workspace.**

### Benefits of kcp for Tekton Runner

#### ✅ Native Multi-Tenancy
- **Workspace isolation**: Each team gets a logical cluster
- **No namespace collisions**: team-a and team-b can both have a TaskRun named "build"
- **Workspace-level RBAC**: Fine-grained access control per workspace
- **Hierarchical organization**: Mirrors real-world org structures

#### ✅ Horizontal Scalability
- **Sharding**: Distribute millions of workspaces across thousands of shards
- **Independent scaling**: Add shards as workspace count grows
- **No noisy neighbor**: One workspace's load doesn't affect others
- **Front-proxy routing**: Efficient request routing to correct shard

#### ✅ Perfect API Match
- **No execution layer**: kcp intentionally excludes Pods/containers
- **API-first design**: Exactly what Tekton Runner needs
- **Lightweight per-tenant**: Workspaces are logical, not physical clusters
- **Shared infrastructure**: All workspaces share underlying kcp shards

#### ✅ Operational Efficiency
- **Single kcp deployment**: Supports thousands of tenants
- **Unified management**: One control plane for all teams
- **Cost effective**: Much cheaper than separate k3s instances per tenant
- **Elastic**: Add/remove workspaces dynamically

### Comparison: k3s vs kcp

| Aspect | k3s (Phase 1) | kcp (Phase 2+) |
|--------|---------------|----------------|
| **Multi-tenancy** | Namespace-based or multiple instances | Native workspace isolation |
| **Scalability** | One cluster per deployment | Millions of workspaces on shared shards |
| **Resource per tenant** | ~100-150MB per k3s instance | Shared infrastructure, minimal overhead |
| **Isolation** | Namespace boundaries | Workspace = logical cluster |
| **Operations** | Manage each instance separately | Unified control plane |
| **Best for** | <10 tenants, local dev, edge | 100+ tenants, SaaS platforms |
| **Complexity** | Low | Medium |
| **Maturity** | Very mature | CNCF Sandbox, newer |

### When to Use kcp

**Choose kcp when:**
- 🎯 Building a SaaS Tekton platform for many customers
- 🎯 Enterprise with 100+ isolated teams/business units
- 🎯 Need true multi-tenant isolation (not just namespaces)
- 🎯 Workspace hierarchy matches organizational structure
- 🎯 Anticipate massive scale (thousands of tenants)

**Stick with k3s/k0s when:**
- ✋ Single user or small team (<10 users)
- ✋ Local development environment
- ✋ Edge/IoT deployments
- ✋ MVP/proof-of-concept phase
- ✋ Simplicity > multi-tenancy features

### Implementation in Tekton Runner

**Control Plane Interface:**
```go
// Pluggable control plane abstraction
type ControlPlane interface {
    Start(ctx context.Context, config Config) error
    GetKubeconfig() ([]byte, error)
    GetClient() (client.Client, error)
    Healthy() bool
    Stop(ctx context.Context) error
}

// kcp-specific implementation
type KcpControlPlane struct {
    workspace   string
    frontProxy  string
    kubeconfig  string
}

func (k *KcpControlPlane) Start(ctx context.Context, config Config) error {
    // Connect to kcp workspace
    k.workspace = config.KcpWorkspace
    k.frontProxy = config.KcpFrontProxy

    // Generate or load kubeconfig for this workspace
    k.kubeconfig = generateWorkspaceKubeconfig(k.workspace, k.frontProxy)

    return nil
}

func (k *KcpControlPlane) GetClient() (client.Client, error) {
    // Return Kubernetes client configured for kcp workspace
    restConfig, err := clientcmd.BuildConfigFromFlags("", k.kubeconfig)
    if err != nil {
        return nil, err
    }

    return client.New(restConfig, client.Options{})
}
```

**Configuration:**
```yaml
# Phase 1: k3s backend
control_plane:
  type: k3s
  k3s:
    disable_components: [traefik, servicelb]
    data_dir: /var/lib/tekton-runner/k3s

# Phase 2: kcp backend
control_plane:
  type: kcp
  kcp:
    workspace: root:org:acme:team-a
    front_proxy: https://kcp.example.com
    kubeconfig: /etc/tekton-runner/kcp-kubeconfig.yaml
```

**Tekton Runner detects backend automatically and adapts behavior.**

### Migration Path

**Start simple (Phase 1):**
1. Deploy with k3s for MVP
2. Single-tenant or small team deployments
3. Prove value of Tekton Runner concept

**Scale up (Phase 2+):**
1. Deploy kcp control plane
2. Migrate existing TaskRun/PipelineRun definitions (no changes needed)
3. Create workspaces for each tenant
4. Deploy Tekton Runner instances per workspace
5. Same Tekton YAML works in both backends

**No vendor lock-in**: The `managedBy` field and Tekton API remain constant regardless of control plane backend.

### Challenges and Considerations

#### ⚠️ Maturity
- kcp is CNCF Sandbox project (not graduated)
- API may evolve (though stabilizing)
- Smaller community than k3s/k0s
- Less production battle-testing

**Mitigation**: Start with k3s (stable), add kcp when needed

#### ⚠️ Complexity
- More concepts to learn (workspaces, APIExport, sharding)
- Front-proxy adds deployment complexity
- Debugging across shards is harder
- Steeper learning curve than k3s

**Mitigation**: Provide comprehensive documentation, examples, and tooling

#### ⚠️ Operational Overhead
- Need to run kcp server(s)
- Front-proxy for multi-shard deployments
- Cache server for cross-shard discovery
- Monitoring and observability more complex

**Mitigation**: Start single-shard, add sharding only when scale requires it

#### ⚠️ Single-Tenant Overkill
- For 1-10 users, k3s is simpler
- kcp's benefits appear at 100+ tenants
- Added complexity not justified for small deployments

**Mitigation**: Clear guidance on when to use each backend

### Future Enhancements

**Phase 3+ (Long-term):**
- **Workspace templates**: Pre-configured workspaces for common use cases
- **Tenant self-service**: API for tenants to create their own workspaces
- **Cross-workspace collaboration**: Share Tasks/Pipelines across workspaces
- **Workspace quotas**: Limit resources per workspace
- **Audit logging**: Track actions across all workspaces
- **Multi-region kcp**: Distribute shards geographically

---

## 5. Implementation Details

### 5.1 SystemD Service

```ini
# /etc/systemd/system/tekton-runner.service
[Unit]
Description=Tekton Runner Service
Documentation=https://github.com/tektoncd/pipeline/blob/main/tekton-runner-design.md
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
ExecStart=/usr/local/bin/tekton-runner serve
Restart=always
RestartSec=10
LimitNOFILE=1048576
Environment="TEKTON_RUNNER_CONFIG=/etc/tekton-runner/config.yaml"

[Install]
WantedBy=multi-user.target
```

### 5.2 Binary Structure

```bash
tekton-runner install    # Install systemd service and dependencies
tekton-runner serve      # Run the service (called by systemd)
tekton-runner uninstall  # Remove service and cleanup
tekton-runner version    # Show version info
tekton-runner config     # Validate/display configuration
```

### 5.3 Installation Script

Inspired by vCluster:

```bash
#!/bin/bash
# install-tekton-runner.sh

set -e

TEKTON_RUNNER_VERSION="${TEKTON_RUNNER_VERSION:-latest}"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/tekton-runner"

# Download binary
echo "Downloading tekton-runner ${TEKTON_RUNNER_VERSION}..."
# Note: URL is a design placeholder - actual implementation would use real release URL
curl -sfL "https://github.com/tektoncd/tekton-runner/releases/download/${TEKTON_RUNNER_VERSION}/tekton-runner-linux-amd64" \
    -o "${INSTALL_DIR}/tekton-runner"
chmod +x "${INSTALL_DIR}/tekton-runner"

# Create config directory
mkdir -p "${CONFIG_DIR}"

# Generate default config
cat > "${CONFIG_DIR}/config.yaml" <<EOF
executor:
  backend: podman
  workspace_base: /var/lib/tekton-runner/workspaces

k3s:
  disable_components:
    - traefik
    - servicelb
    - local-storage
  server_only: true
EOF

# Install and start service
tekton-runner install
systemctl enable tekton-runner
systemctl start tekton-runner

echo "Tekton Runner installed successfully!"
echo "Check status: systemctl status tekton-runner"
```

### 5.4 Configuration

```yaml
# /etc/tekton-runner/config.yaml

# Executor configuration
executor:
  # Backend: podman, dagger, docker, shell
  backend: podman

  # Base path for workspaces
  workspace_base: /var/lib/tekton-runner/workspaces

  # Workspace persistence
  workspace_retention: 24h

  # Backend-specific options
  podman:
    socket: /run/user/1000/podman/podman.sock
    rootless: true

  dagger:
    engine: automatic

  docker:
    socket: /var/run/docker.sock

# Kubernetes control plane configuration
k3s:
  # Disable unnecessary components
  disable_components:
    - traefik
    - servicelb
    - local-storage
    - metrics-server

  # Server-only mode (no agent/kubelet)
  server_only: true

  # Data directory
  data_dir: /var/lib/tekton-runner/k3s

# Tekton configuration
tekton:
  # Install Tekton CRDs
  install_crds: true

  # Disable standard Tekton controllers
  disable_controllers: true

  # Namespace for Tekton resources
  namespace: tekton-pipelines

# Logging
logging:
  level: info
  format: json
  output: /var/log/tekton-runner/runner.log
```

### 5.5 Controller Implementation

```go
package main

import (
    "context"
    "time"

    "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
    clientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
    informers "github.com/tektoncd/pipeline/pkg/client/informers/externalversions"
    "k8s.io/client-go/tools/cache"
)

type Controller struct {
    client   clientset.Interface
    executor Executor
}

func (c *Controller) Run(ctx context.Context) error {
    // Create informer factory
    factory := informers.NewSharedInformerFactory(c.client, time.Minute)

    // Watch TaskRuns
    taskrunInformer := factory.Tekton().V1().TaskRuns()
    taskrunInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
        AddFunc: func(obj interface{}) {
            tr := obj.(*v1.TaskRun)
            if c.shouldHandle(tr) {
                go c.reconcileTaskRun(ctx, tr)
            }
        },
        UpdateFunc: func(oldObj, newObj interface{}) {
            tr := newObj.(*v1.TaskRun)
            if c.shouldHandle(tr) {
                c.handleUpdate(ctx, tr)
            }
        },
    })

    // Start informers
    factory.Start(ctx.Done())

    <-ctx.Done()
    return ctx.Err()
}

func (c *Controller) shouldHandle(tr *v1.TaskRun) bool {
    if tr.Spec.ManagedBy == nil {
        return false
    }
    return *tr.Spec.ManagedBy == "tekton-runner"
}

func (c *Controller) reconcileTaskRun(ctx context.Context, tr *v1.TaskRun) {
    // Skip if already complete
    if tr.Status.Conditions != nil {
        for _, cond := range tr.Status.Conditions {
            if cond.Type == "Succeeded" &&
               (cond.Status == "True" || cond.Status == "False") {
                return
            }
        }
    }

    // Update status to running
    tr.Status.StartTime = &metav1.Time{Time: time.Now()}
    tr.Status.SetCondition(&apis.Condition{
        Type:   "Succeeded",
        Status: "Unknown",
        Reason: "Running",
    })
    c.updateStatus(ctx, tr)

    // Execute via backend
    if err := c.executor.Execute(ctx, tr); err != nil {
        tr.Status.SetCondition(&apis.Condition{
            Type:    "Succeeded",
            Status:  "False",
            Reason:  "Failed",
            Message: err.Error(),
        })
    } else {
        tr.Status.SetCondition(&apis.Condition{
            Type:   "Succeeded",
            Status: "True",
            Reason: "Succeeded",
        })
    }

    tr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
    c.updateStatus(ctx, tr)
}
```

### 5.6 Executor Implementation

```go
package executor

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"

    "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

type PodmanExecutor struct {
    workspaceBase string
}

func (p *PodmanExecutor) Execute(ctx context.Context, tr *v1.TaskRun) error {
    // Create workspace
    workspace := filepath.Join(p.workspaceBase, tr.Name)
    if err := os.MkdirAll(workspace, 0755); err != nil {
        return fmt.Errorf("failed to create workspace: %w", err)
    }

    // Execute each step
    for i, step := range tr.Spec.TaskSpec.Steps {
        if err := p.executeStep(ctx, tr, i, step, workspace); err != nil {
            return err
        }
    }

    // Extract results
    return p.extractResults(tr, workspace)
}

func (p *PodmanExecutor) executeStep(
    ctx context.Context,
    tr *v1.TaskRun,
    stepIndex int,
    step v1.Step,
    workspace string,
) error {
    // Update step status to running
    if tr.Status.Steps == nil {
        tr.Status.Steps = make([]v1.StepState, len(tr.Spec.TaskSpec.Steps))
    }
    tr.Status.Steps[stepIndex].Running = &v1.StepStateRunning{
        StartedAt: metav1.Time{Time: time.Now()},
    }

    // Build podman command
    args := []string{
        "run",
        "--rm",
        "-v", workspace + ":/workspace:Z",
        "-w", "/workspace",
    }

    // Add environment variables
    for _, env := range step.Env {
        args = append(args, "-e", fmt.Sprintf("%s=%s", env.Name, env.Value))
    }

    // Add image and command
    args = append(args, step.Image)
    if step.Script != "" {
        // Write script to file and execute
        scriptPath := filepath.Join(workspace, fmt.Sprintf("step-%d.sh", stepIndex))
        if err := os.WriteFile(scriptPath, []byte(step.Script), 0755); err != nil {
            return err
        }
        args = append(args, "/bin/sh", "/workspace/"+filepath.Base(scriptPath))
    } else {
        args = append(args, step.Command...)
        args = append(args, step.Args...)
    }

    // Execute
    cmd := exec.CommandContext(ctx, "podman", args...)
    output, err := cmd.CombinedOutput()

    // Update step status
    tr.Status.Steps[stepIndex].Terminated = &v1.StepStateTerminated{
        ExitCode:    cmd.ProcessState.ExitCode(),
        StartedAt:   tr.Status.Steps[stepIndex].Running.StartedAt,
        FinishedAt:  metav1.Time{Time: time.Now()},
        ContainerID: "podman://" + step.Name,
    }

    if err != nil {
        tr.Status.Steps[stepIndex].Terminated.Reason = "Error"
        tr.Status.Steps[stepIndex].Terminated.Message = string(output)
        return fmt.Errorf("step %s failed: %w", step.Name, err)
    }

    tr.Status.Steps[stepIndex].Terminated.Reason = "Completed"
    return nil
}

func (p *PodmanExecutor) extractResults(tr *v1.TaskRun, workspace string) error {
    resultsDir := filepath.Join(workspace, "tekton", "results")
    if _, err := os.Stat(resultsDir); os.IsNotExist(err) {
        return nil // No results to extract
    }

    entries, err := os.ReadDir(resultsDir)
    if err != nil {
        return err
    }

    for _, entry := range entries {
        if entry.IsDir() {
            continue
        }

        content, err := os.ReadFile(filepath.Join(resultsDir, entry.Name()))
        if err != nil {
            continue
        }

        tr.Status.Results = append(tr.Status.Results, v1.TaskRunResult{
            Name:  entry.Name(),
            Value: v1.ParamValue{StringVal: string(content)},
        })
    }

    return nil
}
```

---

## 6. User Experience

### Installation

> **Note**: The installation URLs below are design placeholders for a future implementation. The actual project repository and installation endpoints do not yet exist.

```bash
# Download and install (future implementation)
curl -sfL https://get.tekton-runner.dev | sh

# Or with specific version
export TEKTON_RUNNER_VERSION=v0.1.0
curl -sfL https://get.tekton-runner.dev | sh
```

### Using Tekton Runner

Once installed, users interact with standard Tekton tools:

```bash
# Service is running via systemd
systemctl status tekton-runner

# Create a TaskRun (note the managedBy field)
cat <<EOF | kubectl apply -f -
apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  name: hello-world
spec:
  managedBy: "tekton-runner"
  taskSpec:
    steps:
    - name: hello
      image: ubuntu
      script: |
        echo "Hello from Tekton Runner!"
        echo "Running in Podman, not Kubernetes!"
        date > /workspace/result.txt
EOF

# Watch execution (uses standard Tekton CLI)
tkn taskrun logs hello-world -f

# Check status
tkn taskrun describe hello-world

# List all TaskRuns
kubectl get taskruns
```

### What's Happening Behind the Scenes

1. `kubectl apply` sends TaskRun to k3s API server
2. k3s stores it in sqlite3
3. Tekton Runner controller's informer sees the new TaskRun
4. Controller sees `managedBy: "tekton-runner"` and processes it
5. Podman executor runs each step locally
6. Controller updates TaskRun status via K8s API
7. User sees logs and status via standard `tkn` CLI

### Developer Workflow

```bash
# Developer testing locally
cat > my-task.yaml <<EOF
apiVersion: tekton.dev/v1
kind: Task
metadata:
  name: build-and-test
spec:
  params:
  - name: repo
    type: string
  steps:
  - name: clone
    image: alpine/git
    script: git clone $(params.repo) /workspace/source
  - name: build
    image: golang:1.21
    workingDir: /workspace/source
    script: go build -o app .
  - name: test
    image: golang:1.21
    workingDir: /workspace/source
    script: go test ./...
EOF

# Apply Task definition
kubectl apply -f my-task.yaml

# Create TaskRun
tkn task start build-and-test \
  --param repo=https://github.com/example/app \
  --use-param-defaults \
  --showlog \
  --managed-by tekton-runner
```

---

## 7. Advanced Features

### 7.1 Workspace Management

```yaml
apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  name: with-workspace
spec:
  managedBy: "tekton-runner"
  workspaces:
  - name: source
    emptyDir: {}
  taskSpec:
    workspaces:
    - name: source
    steps:
    - name: write
      image: ubuntu
      script: echo "data" > $(workspaces.source.path)/file.txt
    - name: read
      image: ubuntu
      script: cat $(workspaces.source.path)/file.txt
```

Implementation maps workspaces to local directories:
- `emptyDir` → temp directory under `/var/lib/tekton-runner/workspaces/{taskrun-name}`
- Persistent workspaces → could map to named volumes or network storage

### 7.2 Results

```yaml
apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  name: with-results
spec:
  managedBy: "tekton-runner"
  taskSpec:
    results:
    - name: commit-sha
      description: The git commit SHA
    steps:
    - name: get-commit
      image: alpine/git
      script: |
        git rev-parse HEAD | tee $(results.commit-sha.path)
```

Results are written to `/tekton/results/{name}` and extracted by the executor.

### 7.3 PipelineRun Support

```go
func (c *Controller) reconcilePipelineRun(ctx context.Context, pr *v1.PipelineRun) {
    // For each task in the pipeline:
    for _, pipelineTask := range pr.Spec.PipelineSpec.Tasks {
        // Create TaskRun with managedBy
        tr := &v1.TaskRun{
            ObjectMeta: metav1.ObjectMeta{
                Name:      pr.Name + "-" + pipelineTask.Name,
                Namespace: pr.Namespace,
            },
            Spec: v1.TaskRunSpec{
                ManagedBy: ptr.String("tekton-runner"),
                TaskRef:   pipelineTask.TaskRef,
                // ... params, workspaces, etc.
            },
        }

        // Create and wait for completion
        if _, err := c.client.TektonV1().TaskRuns(pr.Namespace).Create(ctx, tr, metav1.CreateOptions{}); err != nil {
            return err
        }

        // Wait for TaskRun to complete
        // Handle finally tasks
        // Update PipelineRun status
    }
}
```

### 7.4 Multiple Execution Backends

Route tasks to different backends based on labels or annotations:

```go
func (c *Controller) selectExecutor(tr *v1.TaskRun) Executor {
    // Check for backend annotation
    if backend, ok := tr.Annotations["tekton-runner.dev/backend"]; ok {
        switch backend {
        case "dagger":
            return c.daggerExecutor
        case "lambda":
            return c.lambdaExecutor
        default:
            return c.defaultExecutor
        }
    }

    // Check for resource requirements
    if requiresGPU(tr) {
        return c.gpuExecutor // Execute on GPU-enabled host
    }

    return c.defaultExecutor
}
```

Example TaskRun:
```yaml
apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  name: heavy-build
  annotations:
    tekton-runner.dev/backend: "lambda"
spec:
  managedBy: "tekton-runner"
  taskSpec:
    steps:
    - name: build
      image: gradle:latest
      script: gradle build
```

---

## 8. Distribution Models

### 8.1 Single Binary

**Advantages:**
- Simplest distribution
- No dependencies beyond OS
- Easy to version and release

**Structure:**
```
tekton-runner (binary)
├── Embedded k3s server
├── Tekton Runner controller
├── Default configuration
└── Installation logic
```

**Build:**
```go
// Embed k3s binary
//go:embed k3s
var k3sBinary []byte

func startK3s(config *Config) error {
    // Write embedded k3s to disk
    k3sPath := "/tmp/tekton-runner-k3s"
    os.WriteFile(k3sPath, k3sBinary, 0755)

    // Start k3s server
    cmd := exec.Command(k3sPath, "server",
        "--disable=traefik,servicelb,local-storage,metrics-server",
        // Note: --disable-agent has known issues, consider alternative approaches
    )
    return cmd.Start()
}
```

### 8.2 Embedded Cluster Package

For enterprise deployments with air-gap requirements:

```
tekton-runner-installer.tar.gz
├── install.sh
├── tekton-runner (binary)
├── k3s (or k0s) binary
├── Container images (air-gap)
│   ├── ubuntu.tar
│   ├── alpine.tar
│   └── ...
├── Tekton CRDs (YAML)
└── Configuration templates
```

**Installation:**
```bash
# Extract and install
tar -xzf tekton-runner-installer.tar.gz
cd tekton-runner-installer
./install.sh --air-gap
```

**Features:**
- Pre-packaged container images
- KOTS admin console integration
- Update management
- Backup/restore via Velero

### 8.3 Container Image

Run Tekton Runner itself in a container:

```dockerfile
FROM alpine:latest

# Install dependencies
RUN apk add --no-cache podman

# Copy tekton-runner binary
COPY tekton-runner /usr/local/bin/

ENTRYPOINT ["tekton-runner", "serve"]
```

**Usage:**
```bash
docker run -d \
  --name tekton-runner \
  --privileged \
  -v /var/run/podman:/var/run/podman \
  -v /var/lib/tekton-runner:/var/lib/tekton-runner \
  -p 6443:6443 \
  tekton-runner:latest
```

### 8.4 Package Managers

**Homebrew (macOS/Linux):**
```bash
brew install tekton-runner
tekton-runner install
```

**APT (Debian/Ubuntu):**
```bash
sudo apt-get install tekton-runner
sudo systemctl enable tekton-runner
```

**YUM/DNF (RHEL/Fedora):**
```bash
sudo dnf install tekton-runner
sudo systemctl enable tekton-runner
```

---

## 9. Comparison to Alternatives

### vs. Standard Tekton on Kubernetes

| Aspect | Standard Tekton | Tekton Runner |
|--------|-----------------|---------------|
| Infrastructure | Full K8s cluster with workers | Control plane only, no workers |
| Execution | Pods in K8s | Podman/Dagger/other backends |
| Installation | Complex (K8s + Tekton) | Single binary |
| Resource Usage | High (kubelet, runtime, etc.) | Low (~200MB total) |
| API Compatibility | Native | Full (via K8s API) |
| Tooling | kubectl, tkn | kubectl, tkn (same) |
| Use Case | Production K8s environments | Local dev, edge, constrained envs |

### vs. act (GitHub Actions Locally)

| Aspect | act | Tekton Runner |
|--------|-----|---------------|
| Workflow Format | GitHub Actions YAML | Tekton YAML |
| Execution | Docker | Pluggable (Podman, Dagger, etc.) |
| API Server | None | K8s API (state, history, watch) |
| Pipeline Features | Basic | Advanced (DAGs, conditionals, etc.) |
| Ecosystem | GitHub-specific | Cloud-native, portable |

### vs. Dagger

| Aspect | Dagger | Tekton Runner |
|--------|--------|---------------|
| Definition | Code (Go, Python, etc.) | YAML |
| Execution | Dagger engine | Multiple backends |
| Caching | Built-in, sophisticated | Depends on backend |
| Learning Curve | Programming required | Declarative YAML |
| Portability | Very high | High |

### vs. Earthly

| Aspect | Earthly | Tekton Runner |
|--------|---------|---------------|
| Definition | Earthfile (Dockerfile-like) | Tekton YAML |
| Caching | Built-in | Depends on backend |
| Execution | buildkit | Multiple backends |
| Kubernetes Integration | Limited | Native (via API) |

### What Makes Tekton Runner Unique

1. **Full Tekton API compatibility** without requiring K8s workers
2. **Pluggable execution backends** - not tied to one runtime
3. **Enterprise-ready** - can leverage Embedded Cluster for air-gap, updates
4. **Gradual migration path** - Same YAML works in K8s and locally
5. **Existing ecosystem** - Works with Tekton Dashboard, Triggers, Catalog

---

## 10. Implementation Roadmap

### Phase 1: MVP (Months 1-2)

**Goals:**
- Proof of concept with basic TaskRun execution
- Single backend (Podman)
- Core status updates

**Deliverables:**
- [ ] Embedded k3s control plane
- [ ] Tekton CRD installation
- [ ] Basic controller with managedBy filter
- [ ] Podman executor
- [ ] Status updates (running, succeeded, failed)
- [ ] Simple workspace support (emptyDir only)
- [ ] SystemD service packaging
- [ ] Installation script

**Success Criteria:**
- Can execute simple TaskRuns locally
- Status visible via `kubectl get taskrun`
- Logs accessible via `tkn taskrun logs`

### Phase 2: Full TaskRun Support (Months 3-4)

**Goals:**
- Production-ready TaskRun execution
- Multiple backends
- Advanced features

**Deliverables:**
- [ ] Params and results
- [ ] Script execution
- [ ] Workspace persistence options
- [ ] Timeout handling
- [ ] Cancellation support
- [ ] Dagger executor
- [ ] Docker executor
- [ ] Configuration file support
- [ ] Logging improvements

**Success Criteria:**
- Pass Tekton conformance tests for TaskRun
- Multiple backends selectable via config
- Production-quality error handling

### Phase 3: PipelineRun Support (Months 5-6)

**Goals:**
- Full Pipeline orchestration
- DAG execution

**Deliverables:**
- [ ] PipelineRun controller
- [ ] Task dependency resolution
- [ ] Finally task support
- [ ] When expressions
- [ ] Pipeline results
- [ ] Matrix/fan-out support

**Success Criteria:**
- Can execute complex multi-task Pipelines
- Proper ordering and dependencies
- Error handling and finally tasks

### Phase 4: Enterprise Features (Months 7-8)

**Goals:**
- Production hardening
- Enterprise deployment

**Deliverables:**
- [ ] Embedded Cluster packaging
- [ ] Air-gap support with image bundling
- [ ] KOTS admin console integration
- [ ] Multi-user support and RBAC
- [ ] Backup/restore
- [ ] High availability (multi-node)
- [ ] Monitoring and metrics
- [ ] Update management

**Success Criteria:**
- Can deploy to enterprise environments
- Air-gap installation works
- HA deployment tested

### Phase 5: Advanced Backends (Months 9+)

**Goals:**
- Remote and cloud execution
- Advanced use cases

**Deliverables:**
- [ ] Remote SSH executor
- [ ] AWS Lambda executor
- [ ] Cloud Run executor
- [ ] Custom executor SDK
- [ ] Sidecar support
- [ ] Result artifacts (OCI, S3)

---

## 11. Technical Considerations

### 11.1 Security

**Rootless Execution:**
```yaml
executor:
  podman:
    rootless: true
    socket: /run/user/1000/podman/podman.sock
```

**User Isolation:**
- Each TaskRun executes in isolated namespace (via Podman user namespaces)
- Workspace directories have restricted permissions
- Results are sanitized before writing to K8s API

**Secrets Handling:**
```go
// Read secret from K8s API
secret, err := c.client.CoreV1().Secrets(tr.Namespace).Get(ctx, secretName, metav1.GetOptions{})

// Inject into container as environment variable
env := fmt.Sprintf("%s=%s", envVar, string(secret.Data[key]))
args = append(args, "-e", env)
```

### 11.2 Resource Management

Since we're not using K8s resource limits, implement custom limits:

```yaml
executor:
  limits:
    max_concurrent_tasks: 10
    max_memory_per_task: "4Gi"
    max_cpu_per_task: "2"
    max_execution_time: "1h"
```

```go
// Enforce CPU limits via cgroups (Podman supports this)
args := []string{"run", "--cpus=2", "--memory=4g", ...}
```

### 11.3 Workspace Persistence

**Strategies:**

1. **Ephemeral** (default):
   ```go
   workspace := filepath.Join("/tmp/tekton-runner", tr.Name)
   defer os.RemoveAll(workspace)
   ```

2. **Timed retention**:
   ```go
   // Cleanup workspaces older than retention period
   if age > config.WorkspaceRetention {
       os.RemoveAll(workspace)
   }
   ```

3. **Persistent volumes** (for PVCs):
   ```go
   // Map PVC to named directory
   workspace := filepath.Join("/var/lib/tekton-runner/pvc", pvcName)
   ```

### 11.4 Logging

**Log Storage:**
- Option 1: Stream to K8s API (store in TaskRun status)
- Option 2: Write to files, expose via HTTP endpoint
- Option 3: Forward to external logging system

```go
// Stream logs to status
logOutput := &bytes.Buffer{}
cmd.Stdout = logOutput
cmd.Stderr = logOutput

// After execution, store in status (truncated if too long)
if logOutput.Len() < maxLogSize {
    tr.Status.Steps[i].Terminated.Message = logOutput.String()
}
```

### 11.5 High Availability

For HA deployments:

1. **Multiple control plane nodes**: k3s supports embedded HA
2. **Leader election**: Use K8s leader election for controller
3. **Distributed execution**: Multiple executor nodes with work distribution

```go
import "k8s.io/client-go/tools/leaderelection"

func runWithLeaderElection(ctx context.Context) {
    leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
        Lock: &resourcelock.LeaseLock{...},
        ReleaseOnCancel: true,
        LeaseDuration:   15 * time.Second,
        RenewDeadline:   10 * time.Second,
        RetryPeriod:     2 * time.Second,
        Callbacks: leaderelection.LeaderCallbacks{
            OnStartedLeading: func(ctx context.Context) {
                runController(ctx)
            },
            OnStoppedLeading: func() {
                log.Info("Lost leadership")
            },
        },
    })
}
```

---

## 12. Future Enhancements

### 12.1 Tekton Triggers Support

Support event-driven execution:

```yaml
apiVersion: triggers.tekton.dev/v1
kind: EventListener
metadata:
  name: github-listener
spec:
  serviceAccountName: tekton-runner
  triggers:
  - name: github-push
    bindings:
    - ref: github-push-binding
    template:
      ref: pipeline-template
```

The controller would:
1. Watch EventListeners
2. Expose webhook endpoints (via K8s Service)
3. Create TaskRuns/PipelineRuns on events
4. Execute via standard managed-by flow

### 12.2 Tekton Dashboard Integration

Install Tekton Dashboard alongside Tekton Runner:

```bash
kubectl apply -f https://github.com/tektoncd/dashboard/releases/latest/download/release.yaml
kubectl port-forward -n tekton-pipelines svc/tekton-dashboard 9097:9097
```

Dashboard works unchanged since it uses K8s API.

### 12.3 Remote Execution

Execute on remote machines via SSH:

```go
type SSHExecutor struct {
    client *ssh.Client
}

func (s *SSHExecutor) Execute(ctx context.Context, tr *v1.TaskRun) error {
    session, err := s.client.NewSession()
    if err != nil {
        return err
    }
    defer session.Close()

    // Transfer workspace files via SCP
    // Execute steps remotely
    // Retrieve results
}
```

### 12.4 Integration with CI/CD Platforms

Bridge to existing CI/CD systems:

```go
type JenkinsExecutor struct {
    jenkinsURL string
    auth       *jenkins.Auth
}

func (j *JenkinsExecutor) Execute(ctx context.Context, tr *v1.TaskRun) error {
    // Convert TaskRun to Jenkins job
    // Trigger Jenkins build
    // Poll for completion
    // Update TaskRun status
}
```

---

## 13. Conclusion

Tekton Runner demonstrates that Tekton's value extends beyond traditional Kubernetes deployments. By leveraging the `managedBy` field and treating Kubernetes as an API server and state store rather than a workload orchestrator, we can bring Tekton's powerful workflow abstractions to environments where full Kubernetes clusters are impractical or unnecessary.

### Key Takeaways

1. **Kubernetes as API Layer**: Control plane-only K8s provides API, state management, and ecosystem compatibility without operational overhead

2. **Execution Flexibility**: Decoupling workflow definition from execution enables running tasks in Podman, Dagger, serverless functions, or any backend

3. **Proven Patterns**: vCluster Standalone and Embedded Cluster validate the systemd service + control plane approach

4. **Full Compatibility**: Standard Tekton YAML, kubectl, and tkn CLI work unchanged, enabling gradual adoption and migration

5. **Enterprise Ready**: Air-gap support, HA, and update management can be achieved through Embedded Cluster packaging

### Next Steps

1. **Community Feedback**: Propose to Tekton community, gather requirements
2. **POC Implementation**: Build Phase 1 MVP with k3s + Podman executor
3. **Testing**: Validate with real-world Tekton tasks and pipelines
4. **Documentation**: Create user guides and tutorials
5. **Release**: Package as single binary with installation script

---

## References

> **Note**: All external links below have been verified as of document creation (January 2025).

- **Tekton Pipeline Managed-By Documentation**:
  - [TaskRun docs](https://github.com/tektoncd/pipeline/blob/main/docs/taskruns.md) (see "Specifying Tekton to Manage TaskRuns" section)
  - [PipelineRun docs](https://github.com/tektoncd/pipeline/blob/main/docs/pipelineruns.md) (see "Delegating reconciliation" section)

- **vCluster Standalone**:
  - [Announcement Blog](https://www.vcluster.com/blog/vcluster-standalone-multi-tenancy-kubernetes)
  - [Architecture Docs](https://www.vcluster.com/docs/vcluster/introduction/architecture/)

- **Replicated Embedded Cluster**:
  - [Overview](https://docs.replicated.com/vendor/embedded-overview)

- **Lightweight Kubernetes**:
  - [k3s](https://k3s.io/)
  - [k0s](https://k0sproject.io/)
  - [MicroShift](https://developers.redhat.com/articles/2025/02/20/why-developers-should-use-microshift)

- **Execution Backends**:
  - [Podman](https://podman.io/)
  - [Dagger](https://dagger.io/)

- **Kubernetes Kueue** (uses managedBy for multi-cluster workload management):
  - [Kueue Documentation](https://kueue.sigs.k8s.io/)
  - [Kueue Tekton Integration](https://kueue.sigs.k8s.io/docs/tasks/run/external_workloads/tektoncd/)
  - [MultiKueue managedBy Usage](https://kueue.sigs.k8s.io/docs/tasks/run/multikueue/kubeflow/) (see managedBy field documentation)

- **kcp - Multi-Tenant Control Plane** (Phase 2+ option for massive scale):
  - [kcp Website](https://www.kcp.io/)
  - [kcp GitHub Repository](https://github.com/kcp-dev/kcp)
  - [kcp Documentation](https://docs.kcp.io/kcp/)
  - [Quickstart: Tenancy and APIs](https://docs.kcp.io/kcp/main/concepts/quickstart-tenancy-and-apis/)
  - [Sharding Architecture](https://docs.kcp.io/kcp/main/concepts/sharding/shards/)
  - [APIExport and APIBinding](https://docs.kcp.io/kcp/main/concepts/apis/exporting-apis/)
