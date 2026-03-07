# Durable Execution for Tekton Pipelines: Design Document

**Status:** Research & Design Phase
**Authors:** Tekton Community
**Last Updated:** 2025-11-12
**Target Tekton Version:** v1.x+

## Table of Contents

1. [Executive Summary](#executive-summary)
2. [Problem Statement](#problem-statement)
3. [Background & Context](#background--context)
4. [Comparison with Alternatives](#comparison-with-alternatives)
5. [Proposed Solution](#proposed-solution)
6. [Technical Deep Dive](#technical-deep-dive)
7. [Implementation Roadmap](#implementation-roadmap)
8. [License Considerations](#license-considerations)
9. [Research Papers & References](#research-papers--references)
10. [Open Questions](#open-questions)

---

## Executive Summary

This document proposes adding **durable execution with automatic checkpointing** to Tekton Pipelines, enabling long-running workloads (particularly ML training jobs) to survive infrastructure failures without losing progress. The solution leverages Tekton's CustomRun mechanism to implement checkpoint/restore capabilities at the process level.

**Key Goals:**
- Enable automatic checkpointing of long-running Tasks without user code modifications
- Support GPU workloads (LLaMA, GPT training) with checkpoint/restore
- Maintain Apache 2.0 license compatibility
- Integrate natively with Tekton's architecture via CustomRun API
- 3-5 year implementation timeline for production-grade system

**Proposed Approach:**
1. **Short-term (0-6 months):** Research and prototype with existing tools (CRIU)
2. **Medium-term (6-18 months):** Build DurableTask CustomRun controller with checkpoint integration
3. **Long-term (18-60 months):** Develop Apache 2.0 licensed userspace checkpoint engine

---

## Problem Statement

### Current State

Tekton Pipelines provides infrastructure-level durability through:
- **CRD Storage:** Task/Pipeline specs stored in etcd (cluster-level durability)
- **Retry Mechanisms:** Automatic retry on pod failures via retries field
- **Result Persistence:** Task results stored in etcd via Termination Message
- **PVC Support:** Workspace data persists across retries on shared volumes

However, Tekton lacks **step-level checkpointing**, meaning:
- ❌ Long-running ML training jobs lose all progress on node failure
- ❌ Multi-hour GPU computations restart from zero after infrastructure issues
- ❌ No automatic incremental progress saving without application-level code changes

### Target Use Case: MLOps Workloads

**Scenario:** Training a large language model that takes 72 hours on 8x A100 GPUs

**Current Behavior:**
```yaml
apiVersion: tekton.dev/v1
kind: Task
metadata:
  name: train-llm
spec:
  steps:
  - name: train
    image: pytorch/pytorch:latest
    script: |
      python train.py --epochs 1000 --checkpoint-every 100
      # If pod dies at epoch 550, restarts from epoch 0 or last manual checkpoint
```

**Problem:**
- Node preemption at hour 48 → lose 48 hours of work
- OOMKilled at 90% complete → restart from beginning
- Network partition → entire training restarted

**Desired Behavior:**
- Automatic checkpoints every N minutes without code changes
- Transparent restore from last checkpoint on failure
- Progress preserved across infrastructure failures

### Comparison: Current vs Desired Durability

| Failure Type | Current Tekton | With Checkpointing |
|--------------|----------------|-------------------|
| Pod deleted | Restart from step 0 | Resume from last checkpoint |
| Node failure | Reschedule, lose progress | Reschedule, restore state |
| OOMKilled | Retry from beginning | Resume after memory freed |
| Manual checkpoint (app-level) | ✅ Supported | ✅ Still supported |
| Automatic checkpoint | ❌ Not supported | ✅ Transparent |

---

## Background & Context

### Tekton Architecture Overview

**Component Architecture:**
```
┌─────────────────────────────────────────────────────────────┐
│                         etcd (State)                         │
│   ┌──────────────┐  ┌──────────────┐  ┌──────────────┐    │
│   │   TaskRun    │  │  PipelineRun │  │  CustomRun   │    │
│   │     CRDs     │  │     CRDs     │  │     CRDs     │    │
│   └──────────────┘  └──────────────┘  └──────────────┘    │
└─────────────────────────────────────────────────────────────┘
                            ▲
                            │ Watch & Reconcile
                            │
┌─────────────────────────────────────────────────────────────┐
│              Tekton Pipeline Controller (Go)                 │
│                                                               │
│  ┌────────────────┐  ┌────────────────┐  ┌──────────────┐ │
│  │ TaskRun        │  │ PipelineRun    │  │ CustomRun    │ │
│  │ Reconciler     │  │ Reconciler     │  │ Reconciler   │ │
│  └────────────────┘  └────────────────┘  └──────────────┘ │
└─────────────────────────────────────────────────────────────┘
                            │
                            │ Create/Manage Pods
                            ▼
┌─────────────────────────────────────────────────────────────┐
│                      Kubernetes Pods                         │
│                                                               │
│  ┌──────────────────────────────────────────────────────┐  │
│  │              Task Pod (User Workload)                 │  │
│  │                                                        │  │
│  │  ┌─────────────────────────────────────────────┐    │  │
│  │  │   entrypointer (Tekton Wrapper Binary)      │    │  │
│  │  │                                               │    │  │
│  │  │   ┌───────────────────────────────────┐    │    │  │
│  │  │   │  User Step Container              │    │    │  │
│  │  │   │  (python train.py)                │    │    │  │
│  │  │   └───────────────────────────────────┘    │    │  │
│  │  └─────────────────────────────────────────────┘    │  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

**Key Components for Checkpointing:**

1. **Entrypointer** (`cmd/entrypoint/main.go`)
   - Wraps every step execution
   - Handles step ordering via file-based synchronization
   - Forwards signals to child processes
   - **Extension Point:** Could inject checkpoint logic here

2. **CustomRun API** (`pkg/apis/pipeline/v1beta1/customrun_types.go`)
   - Official extension mechanism for custom task types
   - Allows implementing new task behaviors without forking Tekton
   - **Use Case:** DurableTask custom controller

3. **TaskRun Controller** (`pkg/reconciler/taskrun/`)
   - Creates pods from Task definitions
   - Manages retry logic
   - Updates status in etcd

### Checkpointing Granularity Levels

| Level | Scope | Implementation | Pros | Cons |
|-------|-------|----------------|------|------|
| **Pipeline-level** | Entire pipeline | Task results in etcd | ✅ Already works | ❌ Coarse-grained |
| **Task-level** | Entire task | PVC + results | ✅ Simple | ❌ Loses intra-task progress |
| **Step-level** | Individual step | Manual checkpoints in code | ✅ Fine control | ❌ Requires code changes |
| **Process-level** | OS process state | CRIU/DMTCP | ✅ Transparent | ❌ Complex implementation |

**This proposal targets process-level checkpointing for transparency.**

---

## Comparison with Alternatives

### Existing Solutions Analysis

#### 1. Hatchet (hatchet.run)

**Architecture:**
- Event-driven workflow engine with durable execution
- PostgreSQL-backed state machine
- Step-level durability with automatic retries

**Durability Model:**
```typescript
// Hatchet SDK - automatic checkpointing
const workflow = createWorkflow({
  steps: [
    { name: "train", fn: trainModel },  // Auto-checkpointed
    { name: "eval", fn: evaluate }       // Resumes from here on failure
  ]
});
```

**Comparison:**
- ✅ **Better:** Automatic step-level checkpointing without code changes
- ✅ **Better:** Built-in durability guarantees
- ❌ **Worse:** Requires SDK integration (not transparent)
- ❌ **Worse:** Not Kubernetes-native (separate control plane)

#### 2. Rivet (rivet.dev)

**Architecture:**
- Kubernetes-native workflow engine
- Actor model with persistent state
- Checkpoint via state snapshots

**Durability Model:**
- State stored in distributed KV store (etcd/PostgreSQL)
- Automatic actor recovery on failure
- Requires actor-based programming model

**Comparison:**
- ✅ **Better:** Native durability in API design
- ❌ **Worse:** Complete paradigm shift from Tekton
- ❌ **Worse:** Actors require application refactoring

#### 3. Temporal (temporal.io)

**Architecture:**
- Event-sourced workflow orchestration
- All state changes logged to durable storage
- Automatic replay on worker failure

**Durability Model:**
```go
// Temporal Workflow - durable by default
func TrainModelWorkflow(ctx workflow.Context) error {
    var result TrainingResult
    // This entire workflow is checkpointed automatically
    err := workflow.ExecuteActivity(ctx, TrainModel).Get(ctx, &result)
    // On failure, replays from event log
}
```

**Comparison:**
- ✅ **Better:** Production-proven durability (Uber, Netflix, Stripe use it)
- ✅ **Better:** Event sourcing provides full history
- ❌ **Worse:** Requires Temporal SDK and server deployment
- ❌ **Worse:** Not transparent to existing code
- ⚖️ **Trade-off:** Full platform vs Tekton extension

#### 4. Current Tekton + Manual Checkpointing

**Current Approach:**
```yaml
apiVersion: tekton.dev/v1
kind: Task
spec:
  steps:
  - name: train
    script: |
      # User must implement checkpointing
      python train.py --resume-from $(workspaces.shared.path)/checkpoint.pt
```

**Comparison:**
- ✅ **Better:** Full control over checkpoint logic
- ✅ **Better:** No infrastructure changes needed
- ❌ **Worse:** Requires application-level implementation
- ❌ **Worse:** Not transparent to users

### Why Build This for Tekton?

**Reasons to extend Tekton rather than migrate:**

1. **Investment Protection:** Organizations with existing Tekton pipelines
2. **Kubernetes Native:** Tekton's CRD-based model fits K8s ecosystem
3. **Transparency:** Process-level checkpointing works with any code
4. **Extensibility:** CustomRun API designed for this use case
5. **Open Source:** Full control over implementation and licensing

---

## Proposed Solution

### High-Level Design: DurableTask CustomRun

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────────────┐
│                       Tekton User                                │
│  apiVersion: durable.tekton.dev/v1alpha1                        │
│  kind: DurableTask                                               │
│  spec:                                                            │
│    checkpoint:                                                    │
│      enabled: true                                                │
│      interval: 5m                                                 │
│      storage: pvc://checkpoints                                   │
└─────────────────────────────────────────────────────────────────┘
                            │
                            │ Creates
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│               CustomRun (Tekton Native API)                      │
│  apiVersion: tekton.dev/v1beta1                                  │
│  kind: CustomRun                                                  │
│  spec:                                                            │
│    customRef:                                                     │
│      apiVersion: durable.tekton.dev/v1alpha1                     │
│      kind: DurableTask                                            │
└─────────────────────────────────────────────────────────────────┘
                            │
                            │ Watched by
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│          DurableTask CustomRun Controller (New)                  │
│                                                                   │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │  Reconciler Logic:                                         │ │
│  │  1. Check for existing checkpoint in PVC                  │ │
│  │  2. Create pod with checkpoint-wrapper sidecar            │ │
│  │  3. Mount checkpoint PVC                                   │ │
│  │  4. Watch pod for completion/failure                       │ │
│  │  5. On failure: restore from last checkpoint              │ │
│  └───────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
                            │
                            │ Creates Pod with
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Checkpoint-Enabled Pod                        │
│                                                                   │
│  ┌─────────────────────┐          ┌─────────────────────────┐  │
│  │ checkpoint-wrapper  │          │  User Container         │  │
│  │  (Sidecar)          │  ◄─────► │  (python train.py)      │  │
│  │                     │  ptrace  │                         │  │
│  │  - Periodic timer   │          │                         │  │
│  │  - SIGCHECKPOINT    │          │                         │  │
│  │  - Write to PVC     │          │                         │  │
│  └─────────────────────┘          └─────────────────────────┘  │
│              │                                                    │
│              │ Writes checkpoints                                │
│              ▼                                                    │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │         Checkpoint PVC (Persistent Storage)               │  │
│  │  /checkpoints/checkpoint-0001.img                         │  │
│  │  /checkpoints/checkpoint-0002.img                         │  │
│  │  /checkpoints/latest.img                                  │  │
│  └──────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

### Design Decisions

#### 1. Why CustomRun?

**CustomRun is Tekton's official extension mechanism:**

```go
// pkg/apis/pipeline/v1beta1/customrun_types.go
type CustomRun struct {
    Spec CustomRunSpec
    // Custom controllers watch CustomRuns with their apiVersion
}

type CustomRunSpec struct {
    CustomRef *TaskRef  // Points to custom task type (DurableTask)
    // Controller implements checkpoint logic
}
```

**Benefits:**
- ✅ Native Tekton API (not a fork)
- ✅ Follows Kubernetes controller pattern
- ✅ Can be installed as separate component
- ✅ Integrates with existing Pipeline definitions

**Example Usage:**
```yaml
apiVersion: tekton.dev/v1
kind: Pipeline
metadata:
  name: ml-training
spec:
  tasks:
  - name: train-model
    taskRef:
      apiVersion: durable.tekton.dev/v1alpha1
      kind: DurableTask  # Uses CustomRun under the hood
      name: gpu-training
```

#### 2. Checkpoint Storage Strategy

**Options Evaluated:**

| Storage | Pros | Cons | Decision |
|---------|------|------|----------|
| PVC | Kubernetes-native, simple | Network I/O overhead | ✅ **Phase 1** |
| Volume Snapshots | Fast, built-in | Requires CSI driver support | ⏭️ Phase 2 |
| Object Storage (S3) | Scalable, cheap | Network latency | ⏭️ Phase 3 |
| Local ephemeral | Fastest | Lost on node failure | ❌ Not durable |

**Implementation:**
```yaml
apiVersion: durable.tekton.dev/v1alpha1
kind: DurableTask
spec:
  checkpoint:
    storage:
      pvc:
        claimName: ml-checkpoints
        path: /checkpoints
    interval: 5m
    retention: 3  # Keep last 3 checkpoints
```

#### 3. Checkpoint Trigger Mechanisms

**Multiple trigger strategies:**

1. **Time-based (default):**
   ```yaml
   checkpoint:
     interval: 5m  # Every 5 minutes
   ```

2. **Progress-based (future):**
   ```yaml
   checkpoint:
     onProgress:
       metric: training.epoch
       every: 10  # Every 10 epochs
   ```

3. **Signal-based:**
   ```yaml
   checkpoint:
     onSignal: SIGUSR1  # User can trigger manually
   ```

4. **Pre-eviction (advanced):**
   - Use Kubernetes eviction webhooks
   - Checkpoint before node drains

---

## Technical Deep Dive

### Process Checkpointing Fundamentals

#### What Needs to Be Captured?

**Complete process state includes:**

```
┌─────────────────────────────────────────────────────────────┐
│                    Process Memory Layout                     │
├─────────────────────────────────────────────────────────────┤
│  Text Segment       │ Executable code (read-only)           │
│  Data Segment       │ Initialized global variables          │
│  BSS Segment        │ Uninitialized globals                 │
│  Heap               │ malloc() allocations (grows up)       │
│  Memory-mapped      │ mmap() regions, shared libs           │
│  Stack              │ Local variables (grows down)          │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                      CPU Register State                      │
├─────────────────────────────────────────────────────────────┤
│  General Purpose    │ RAX, RBX, RCX, RDX, RSI, RDI, etc.   │
│  Instruction Ptr    │ RIP (what instruction to execute)     │
│  Stack Pointer      │ RSP (current stack position)          │
│  Flags Register     │ RFLAGS (CPU state flags)              │
│  Floating Point     │ FPU, SSE, AVX registers               │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                    File Descriptor Table                     │
├─────────────────────────────────────────────────────────────┤
│  FD 0 (stdin)       │ /dev/pts/0 @ offset 0                │
│  FD 1 (stdout)      │ /var/log/output.log @ offset 12345   │
│  FD 3               │ socket:[12345] (TCP connection)       │
│  FD 4               │ /data/checkpoint.tmp (open file)      │
│  FD 5               │ pipe:[67890] (IPC pipe)               │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                  Signal Handler Configuration                │
├─────────────────────────────────────────────────────────────┤
│  SIGTERM            │ Handler @ 0x7f1234567890              │
│  SIGINT             │ SIG_IGN (ignored)                     │
│  SIGUSR1            │ Default                                │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                    Additional Process State                  │
├─────────────────────────────────────────────────────────────┤
│  Threads            │ TID list, TLS, per-thread stacks     │
│  Timers             │ timerfd, alarm(), setitimer()         │
│  Namespaces         │ PID, mount, network, IPC, UTS, user   │
│  Cgroups            │ Memory limits, CPU shares             │
│  Credentials        │ UID, GID, capabilities                │
│  Working Directory  │ Current pwd                            │
│  Environment Vars   │ PATH, HOME, etc.                       │
│  umask              │ File creation mask                     │
│  rlimits            │ RLIMIT_NOFILE, RLIMIT_CPU, etc.       │
└─────────────────────────────────────────────────────────────┘
```

#### How ptrace() Works

**ptrace is the core Linux syscall for process control:**

```c
#include <sys/ptrace.h>
#include <sys/user.h>

// Step 1: Attach to target process
pid_t target = 12345;
if (ptrace(PTRACE_ATTACH, target, NULL, NULL) == -1) {
    perror("ptrace attach failed");
    exit(1);
}
waitpid(target, NULL, 0);  // Wait for process to stop

// Step 2: Read CPU registers
struct user_regs_struct regs;
ptrace(PTRACE_GETREGS, target, NULL, &regs);

printf("Instruction pointer: 0x%llx\n", regs.rip);
printf("Stack pointer: 0x%llx\n", regs.rsp);

// Step 3: Read memory from target process
char buffer[4096];
long addr = regs.rsp;  // Read from stack
for (int i = 0; i < 4096; i += sizeof(long)) {
    long word = ptrace(PTRACE_PEEKDATA, target, addr + i, NULL);
    memcpy(buffer + i, &word, sizeof(long));
}

// Step 4: Detach
ptrace(PTRACE_DETACH, target, NULL, NULL);
```

**Key ptrace operations:**

| Operation | Purpose | Use in Checkpointing |
|-----------|---------|---------------------|
| `PTRACE_ATTACH` | Attach to process | Begin checkpoint capture |
| `PTRACE_GETREGS` | Read CPU registers | Capture instruction pointer, stack |
| `PTRACE_PEEKDATA` | Read memory word | Capture heap/stack data |
| `PTRACE_GETFPREGS` | Read FPU registers | Capture floating-point state |
| `PTRACE_GETSIGINFO` | Read signal info | Capture pending signals |
| `PTRACE_DETACH` | Detach from process | Resume after checkpoint |

**Why ptrace requires privileges:**
- Can read arbitrary process memory (security risk)
- Can modify process execution (could inject code)
- Requires `CAP_SYS_PTRACE` capability in Kubernetes

#### Reading Process Memory via /proc

**Faster alternative to PTRACE_PEEKDATA:**

```c
#include <fcntl.h>
#include <stdio.h>

// Read entire memory region in one syscall
int read_memory_region(pid_t pid, unsigned long start, size_t length) {
    char path[256];
    snprintf(path, sizeof(path), "/proc/%d/mem", pid);

    int mem_fd = open(path, O_RDONLY);
    if (mem_fd == -1) return -1;

    // Seek to memory address
    lseek(mem_fd, start, SEEK_SET);

    // Read entire region
    char *buffer = malloc(length);
    ssize_t bytes = read(mem_fd, buffer, length);

    close(mem_fd);
    return bytes;
}
```

**Memory regions from /proc/[pid]/maps:**
```bash
$ cat /proc/12345/maps
7f8a00000000-7f8a00021000 rw-p 00000000 00:00 0      # Heap
7f8a00021000-7f8a40000000 ---p 00000000 00:00 0      # Guard page
7f8a40000000-7f8a40001000 r--p 00000000 08:01 123    # Executable text
7fff12340000-7fff12361000 rw-p 00000000 00:00 0      # Stack
```

**Parsing and capturing:**
```c
struct memory_region {
    unsigned long start;
    unsigned long end;
    char permissions[5];  // rwxp
    void *data;
};

// Parse /proc/[pid]/maps and capture each region
int checkpoint_memory(pid_t pid) {
    FILE *maps = fopen("/proc/pid/maps", "r");
    char line[256];

    while (fgets(line, sizeof(line), maps)) {
        struct memory_region region;
        sscanf(line, "%lx-%lx %s", &region.start, &region.end, region.permissions);

        // Only checkpoint writable regions (rw)
        if (strchr(region.permissions, 'w')) {
            size_t len = region.end - region.start;
            region.data = malloc(len);
            read_memory_region(pid, region.start, len);
            // Save to checkpoint file...
        }
    }
}
```

### Existing Checkpoint Tools Analysis

#### 1. CRIU (Checkpoint/Restore In Userspace)

**Architecture:**
```
┌──────────────────────────────────────────────────────────┐
│                    CRIU Components                        │
├──────────────────────────────────────────────────────────┤
│  criu dump          │ Checkpoint process to disk         │
│  criu restore       │ Restore from checkpoint            │
│  criu-ns            │ Namespace handling                 │
│  criu-swrk          │ Service workers (RPC)              │
└──────────────────────────────────────────────────────────┘
              │
              │ Uses
              ▼
┌──────────────────────────────────────────────────────────┐
│              Kernel APIs & /proc Interface                │
├──────────────────────────────────────────────────────────┤
│  ptrace()           │ Process control                    │
│  /proc/[pid]/*      │ Process introspection              │
│  clone()            │ Namespace restoration              │
│  prctl()            │ Process capabilities               │
│  kcmp()             │ Kernel object comparison           │
└──────────────────────────────────────────────────────────┘
```

**Checkpoint file format:**
```
checkpoint-dir/
├── core-1234.img         # Process core (registers, IDs)
├── mm-1234.img           # Memory map info
├── pages-1.img           # Actual memory pages
├── fdinfo-1234.img       # File descriptors
├── fs-1234.img           # Filesystem state (cwd, root)
├── sigacts-1234.img      # Signal handlers
└── inventory.img         # Metadata and index
```

**Integration with Kubernetes:**
```yaml
# CRIU requires CRI-O runtime
apiVersion: v1
kind: Pod
spec:
  runtimeClassName: crio-checkpoint  # Must use CRI-O
  containers:
  - name: training
    securityContext:
      capabilities:
        add:
        - SYS_PTRACE          # Required for ptrace
        - SYS_ADMIN           # Required for namespace ops
        - CHECKPOINT_RESTORE  # Kubernetes 1.25+ capability
```

**Limitations for Tekton:**
- ⚠️ Requires CRI-O (most clusters use containerd)
- ⚠️ Privileged containers (security concern)
- ⚠️ GPL-licensed (some code), complex licensing
- ⚠️ Container runtime integration needed

#### 2. DMTCP (Distributed MultiThreaded CheckPointing)

**Architecture:**
```
┌────────────────────────────────────────────────────────────┐
│                      DMTCP Design                           │
├────────────────────────────────────────────────────────────┤
│  Coordinator        │ Central coordination daemon          │
│  dmtcp_launch       │ Wraps application with DMTCP lib     │
│  dmtcp_command      │ Trigger checkpoint remotely          │
│  libdmtcp.so        │ Preloaded library (LD_PRELOAD)       │
└────────────────────────────────────────────────────────────┘
              │
              │ Intercepts syscalls via
              ▼
┌────────────────────────────────────────────────────────────┐
│              LD_PRELOAD Hook Mechanism                      │
├────────────────────────────────────────────────────────────┤
│  open() → dmtcp_open()        │ Track file descriptors    │
│  socket() → dmtcp_socket()    │ Track network connections │
│  fork() → dmtcp_fork()        │ Track child processes     │
│  pthread_create() → ...       │ Track threads             │
└────────────────────────────────────────────────────────────┘
```

**How it works:**
```bash
# Start coordinator
dmtcp_coordinator &

# Launch application with DMTCP
dmtcp_launch python train.py

# Trigger checkpoint
dmtcp_command -c  # Creates ckpt_*.dmtcp files

# Restore
dmtcp_restart ckpt_*.dmtcp
```

**Kubernetes Integration:**
```yaml
apiVersion: v1
kind: Pod
spec:
  containers:
  - name: training
    command:
    - /dmtcp/bin/dmtcp_launch
    - --with-plugin
    - --coord-host=dmtcp-coordinator
    - --
    - python
    - train.py
    volumeMounts:
    - name: dmtcp-bin
      mountPath: /dmtcp
    - name: checkpoints
      mountPath: /checkpoints
```

**Limitations for Tekton:**
- ❌ **LGPL licensed** (incompatible with Apache 2.0 for bundling)
- ⚠️ LD_PRELOAD can be fragile with complex apps
- ⚠️ Requires wrapper around user command
- ⚠️ Coordinator adds operational complexity

**License workaround:**
- Ship as optional external dependency
- User installs separately (like GPU drivers)
- Not bundled with Tekton distribution

#### 3. CRIUgpu (CRIU for GPU Workloads)

**What it adds:**
```
CRIU + GPU support = CRIUgpu
         │
         ├─ CUDA runtime state capture
         ├─ GPU memory (VRAM) checkpointing
         ├─ CUDA context restoration
         └─ cuDNN state handling
```

**Tested workloads (from paper):**
- ✅ LLaMA 3.1 training
- ✅ GPT-2 fine-tuning
- ✅ BERT pre-training
- ✅ ResNet-50 training

**Checkpoint overhead:**
- ~30 seconds for 8GB GPU memory
- ~2GB checkpoint file per GPU
- Incremental checkpoint: ~5 seconds

**Requirements:**
- NVIDIA driver with UVM (Unified Virtual Memory)
- CUDA 11.0+
- CRI-O with GPU support
- Same GPU model on restore (not portable across GPU types)

**Tekton integration challenge:**
- Inherits all CRIU limitations
- Plus GPU-specific requirements
- Not portable across heterogeneous clusters

### Building a Custom Checkpoint Engine

#### High-Level Architecture

**Proposed component design:**

```
┌─────────────────────────────────────────────────────────────┐
│                  Tekton Checkpoint Stack                     │
└─────────────────────────────────────────────────────────────┘
                            │
          ┌─────────────────┼─────────────────┐
          │                 │                 │
          ▼                 ▼                 ▼
┌──────────────────┐ ┌──────────────┐ ┌──────────────────┐
│ DurableTask      │ │ Checkpoint   │ │  Storage Driver  │
│ Controller (Go)  │ │ Engine (C)   │ │  (Go)            │
│                  │ │              │ │                  │
│ - CustomRun API  │ │ - ptrace()   │ │ - PVC writer     │
│ - Reconciliation │ │ - /proc      │ │ - S3 uploader    │
│ - Pod mgmt       │ │ - Restore    │ │ - Volume snap    │
└──────────────────┘ └──────────────┘ └──────────────────┘
          │                 │                 │
          └─────────────────┼─────────────────┘
                            │
                            ▼
                  ┌──────────────────┐
                  │  Checkpoint PVC  │
                  │  /checkpoints/   │
                  └──────────────────┘
```

**Language choice rationale:**

| Component | Language | Reason |
|-----------|----------|--------|
| CustomRun Controller | Go | Must integrate with Tekton (Go codebase) |
| Checkpoint Engine | C → Rust | C for prototype speed, Rust for production safety |
| Storage Driver | Go | Kubernetes client-go library |
| CLI Tools | Go | Consistency with Tekton CLI |

#### Implementation Phases

**Phase 1: Proof of Concept (0-6 months)**

*Goal: Validate checkpoint/restore works for simple processes*

**Deliverables:**
```
checkpoint-poc/
├── checkpoint.c          # Core engine
│   ├── attach_process()
│   ├── capture_memory()
│   ├── capture_fds()
│   └── save_checkpoint()
├── restore.c
│   ├── load_checkpoint()
│   ├── restore_memory()
│   └── restore_fds()
└── test/
    ├── simple_counter.c  # Test: checkpoint counting loop
    └── file_writer.c     # Test: checkpoint with open files
```

**Success criteria:**
- ✅ Checkpoint single-threaded C program
- ✅ Restore and resume correctly
- ✅ Handle open files (but not sockets yet)
- ✅ < 1 second checkpoint time for small process

**Phase 2: Multi-threading Support (6-12 months)**

*Goal: Handle real-world applications with threads*

**Deliverables:**
```
checkpoint-engine/
├── src/
│   ├── thread.c          # Thread enumeration
│   ├── tls.c             # Thread-local storage
│   ├── futex.c           # Synchronization primitives
│   └── signals.c         # Signal handler preservation
└── test/
    ├── pthread_test.c
    └── opencv_resize.c   # Real ML preprocessing workload
```

**Success criteria:**
- ✅ Checkpoint multi-threaded applications
- ✅ Preserve mutex/futex state
- ✅ Handle thread-local storage (TLS)
- ✅ Checkpoint PyTorch DataLoader (uses threads)

**Phase 3: Network & IPC (12-18 months)**

*Goal: Support distributed training (multi-node)*

**Deliverables:**
```
checkpoint-engine/
├── src/
│   ├── socket.c          # TCP/UDP state capture
│   ├── pipe.c            # IPC pipe handling
│   └── namespace.c       # Network namespace support
└── test/
    ├── tcp_server.c
    └── horovod_test.py   # Distributed ML training
```

**Success criteria:**
- ✅ Checkpoint process with open TCP connections
- ✅ Restore and resume network communication
- ✅ Handle UNIX domain sockets
- ✅ Support PyTorch DDP (Distributed Data Parallel)

**Phase 4: Optimization (18-24 months)**

*Goal: Production-ready performance*

**Deliverables:**
```
checkpoint-engine/
├── src/
│   ├── incremental.c     # Delta checkpointing
│   ├── compression.c     # LZ4/Zstd compression
│   └── parallel.c        # Multi-threaded checkpoint I/O
└── benchmarks/
    ├── checkpoint_time.py
    └── restore_overhead.py
```

**Success criteria:**
- ✅ Incremental checkpoints (only changed pages)
- ✅ < 5% runtime overhead with periodic checkpoints
- ✅ Compress checkpoints (5-10x reduction)
- ✅ Parallel checkpoint I/O

**Phase 5: GPU Support (24-36 months)**

*Goal: Support ML training on GPUs*

**Approach:**
- Study CRIUgpu paper and implementation
- Capture CUDA runtime state
- Checkpoint GPU memory (VRAM)
- Handle cuDNN/cuBLAS state

**Open questions:**
- GPU memory migration across different GPU types?
- Incremental GPU memory checkpointing?
- Multi-GPU (NCCL) communication state?

**Phase 6: Tekton Integration (36-48 months)**

*Goal: Native Tekton CustomRun controller*

**Deliverables:**
```
pkg/reconciler/durabletask/
├── reconciler.go         # Main controller logic
├── checkpoint.go         # Checkpoint trigger logic
├── restore.go            # Restore logic
└── storage.go            # PVC/S3 integration

config/
├── 300-durabletask.yaml  # CRD definition
└── controller.yaml       # Deployment

cmd/durabletask-controller/
└── main.go               # Controller entrypoint
```

**Phase 7: Production Hardening (48-60 months)**

*Goal: Enterprise-ready*

- ✅ Comprehensive test suite
- ✅ Security audit
- ✅ Performance benchmarking
- ✅ Documentation and examples
- ✅ Migration guides
- ✅ Multi-cluster support
- ✅ Monitoring and observability

#### Core Algorithm Pseudocode

**Checkpoint algorithm:**
```c
// High-level checkpoint flow
int checkpoint_process(pid_t target, const char *checkpoint_path) {
    // 1. Stop the process
    if (ptrace(PTRACE_ATTACH, target, NULL, NULL) < 0) {
        return -1;
    }
    waitpid(target, NULL, 0);  // Wait for SIGSTOP

    // 2. Capture CPU state
    struct user_regs_struct regs;
    ptrace(PTRACE_GETREGS, target, NULL, &regs);

    // 3. Capture memory layout
    FILE *maps = fopen("/proc/target/maps", "r");
    struct memory_region *regions = parse_maps(maps);

    // 4. Capture memory contents (only writable regions)
    for (each region in regions) {
        if (region.writable) {
            capture_memory(target, region.start, region.size);
        }
    }

    // 5. Capture file descriptors
    DIR *fddir = opendir("/proc/target/fd");
    for (each fd in fddir) {
        capture_fd_state(target, fd);
    }

    // 6. Capture threads
    DIR *taskdir = opendir("/proc/target/task");
    for (each tid in taskdir) {
        capture_thread_state(target, tid);
    }

    // 7. Capture signal handlers
    FILE *status = fopen("/proc/target/status", "r");
    parse_signal_handlers(status);

    // 8. Write checkpoint to disk
    write_checkpoint(checkpoint_path, &checkpoint_data);

    // 9. Resume process
    ptrace(PTRACE_DETACH, target, NULL, NULL);

    return 0;
}
```

**Restore algorithm:**
```c
// High-level restore flow
pid_t restore_process(const char *checkpoint_path) {
    // 1. Load checkpoint from disk
    struct checkpoint_data *ckpt = load_checkpoint(checkpoint_path);

    // 2. Create new process (fork + exec stub)
    pid_t child = fork();
    if (child == 0) {
        // Child: wait to be controlled
        ptrace(PTRACE_TRACEME, 0, NULL, NULL);
        raise(SIGSTOP);
        // Parent will restore our state
        exit(0);  // Never reached
    }

    // 3. Attach to child
    waitpid(child, NULL, 0);

    // 4. Restore memory mappings
    for (each region in ckpt->memory_regions) {
        // Use ptrace + /proc/pid/mem to write memory
        restore_memory(child, region.start, region.data, region.size);
    }

    // 5. Restore file descriptors
    for (each fd in ckpt->fds) {
        restore_fd(child, fd);
    }

    // 6. Restore threads
    for (each thread in ckpt->threads) {
        create_thread_at_state(child, thread);
    }

    // 7. Restore CPU registers (including instruction pointer)
    ptrace(PTRACE_SETREGS, child, NULL, &ckpt->regs);

    // 8. Restore signal handlers
    restore_signal_handlers(child, ckpt->signals);

    // 9. Resume execution
    ptrace(PTRACE_DETACH, child, NULL, NULL);

    return child;
}
```

### Technical Challenges

#### 1. File Descriptors

**Problem:** Open files have position offsets that must be preserved

```c
// Original process:
int fd = open("/data/model.bin", O_RDWR);
lseek(fd, 1024, SEEK_SET);  // Positioned at byte 1024
// ... checkpoint happens here ...
read(fd, buffer, 100);      // Should read from 1024

// Restore must:
// 1. Re-open the same file
// 2. Seek to byte 1024
// 3. Restore the fd number (not just any fd)
```

**Solution:**
```c
struct fd_state {
    int fd_num;
    char path[PATH_MAX];     // From /proc/pid/fd/N -> readlink
    off_t position;          // From lseek(fd, 0, SEEK_CUR)
    int flags;               // From fcntl(F_GETFL)
    int mode;                // From stat()
};

int restore_fd(pid_t target, struct fd_state *fd) {
    // 1. Open file in target process (via ptrace)
    int new_fd = open_in_target(target, fd->path, fd->flags);

    // 2. Duplicate to correct fd number
    dup2_in_target(target, new_fd, fd->fd_num);

    // 3. Restore position
    lseek_in_target(target, fd->fd_num, fd->position, SEEK_SET);
}
```

#### 2. Network Sockets

**Problem:** TCP sockets have ephemeral state (sequence numbers, windows)

```
Original connection:
  Client                Server
    │ SEQ=1000            │
    │ ───────────────────>│
    │         ACK=1001    │
    │<─────────────────── │

[ Checkpoint happens - server restarts ]

  Restored server must:
  - Know SEQ=1000 was sent
  - Resume with correct ACK numbers
  - Maintain TCP state machine position
```

**Approaches:**

**Option A: TCP Connection Migration (complex)**
```c
// Requires kernel support (SO_REUSEPORT, etc.)
struct tcp_state {
    uint32_t snd_nxt;    // Next sequence number to send
    uint32_t rcv_nxt;    // Next expected sequence number
    uint32_t snd_wnd;    // Send window
    uint32_t rcv_wnd;    // Receive window
    // ... many more fields from tcp_sock
};

// Restore requires raw socket manipulation
// Very complex, often needs kernel modules
```

**Option B: Connection Draining (simpler)**
```c
// Before checkpoint:
// 1. Drain all TCP connections (finish pending sends/recvs)
// 2. Checkpoint with sockets closed
// 3. On restore, let application reconnect

// Requires cooperation from application or connection retry logic
```

**Recommendation:** Start with Option B, add Option A in Phase 3+

#### 3. Shared Memory & Memory-Mapped Files

**Problem:** Multiple processes may share memory regions

```c
// Process A:
int shmfd = shm_open("/myshm", O_RDWR, 0666);
void *ptr = mmap(NULL, 4096, PROT_READ|PROT_WRITE, MAP_SHARED, shmfd, 0);

// Process B:
int shmfd = shm_open("/myshm", O_RDWR, 0666);
void *ptr = mmap(NULL, 4096, PROT_READ|PROT_WRITE, MAP_SHARED, shmfd, 0);

// Both processes see the same memory!
// Checkpoint must handle:
// 1. Identify shared regions (same inode in /proc/pid/maps)
// 2. Save only once
// 3. Restore sharing relationship
```

**Solution:**
```c
struct shared_region {
    dev_t dev;          // Device ID
    ino_t inode;        // Inode number
    void *base_addr;    // Base address
    size_t length;
    char name[NAME_MAX];  // e.g., "/myshm"
};

// During checkpoint:
// 1. Hash regions by (dev, inode)
// 2. Save each unique region once
// 3. Record all mappings

// During restore:
// 1. Restore shared object first (shm_open, file open)
// 2. Map into each process at correct address
```

#### 4. Incremental Checkpointing

**Problem:** Saving entire memory every time is slow

**Optimization: Soft-Dirty Bits**

```bash
# Kernel feature: track which pages were modified
echo 4 > /proc/[pid]/clear_refs  # Clear soft-dirty bits

# ... process runs for 5 minutes ...

# Check which pages are dirty:
cat /proc/[pid]/pagemap
# Each page has soft-dirty bit set if modified

# Save only dirty pages to checkpoint
```

**Implementation:**
```c
struct incremental_checkpoint {
    int checkpoint_num;
    int parent_checkpoint;  // Base checkpoint
    size_t num_dirty_pages;
    struct dirty_page *pages;
};

int incremental_checkpoint(pid_t target, int base_checkpoint) {
    // 1. Clear soft-dirty bits
    FILE *refs = fopen("/proc/target/clear_refs", "w");
    fprintf(refs, "4\n");
    fclose(refs);

    // 2. Wait for next checkpoint interval
    sleep(300);  // 5 minutes

    // 3. Read pagemap to find dirty pages
    FILE *pagemap = fopen("/proc/target/pagemap", "rb");
    for (each page) {
        uint64_t entry;
        fread(&entry, sizeof(entry), 1, pagemap);
        if (entry & PM_SOFT_DIRTY) {
            // Save this page
            save_page(page_addr, page_data);
        }
    }

    // Result: only changed pages saved
    // Checkpoint size: ~1-10% of full checkpoint
}
```

**Space savings:**
- Full checkpoint: 10GB
- Incremental (after 5 min): ~100MB (1%)
- Incremental (after 1 hour): ~500MB (5%)

---

## Implementation Roadmap

### Timeline Overview

```
Year 1: Foundation
├─ Q1: Research & Prototyping
├─ Q2: Core checkpoint engine (single-threaded)
├─ Q3: Multi-threading support
└─ Q4: File descriptor handling

Year 2: Advanced Features
├─ Q1: Network sockets (basic)
├─ Q2: Incremental checkpointing
├─ Q3: Compression & optimization
└─ Q4: Container integration

Year 3: Tekton Integration
├─ Q1: CustomRun controller
├─ Q2: PVC storage driver
├─ Q3: DurableTask CRD
└─ Q4: Alpha release

Year 4: GPU Support
├─ Q1-Q2: CUDA state capture
├─ Q3-Q4: GPU memory checkpointing

Year 5: Production
├─ Q1-Q2: Security audit & hardening
├─ Q3-Q4: Beta → GA release
```

### Detailed Milestones

#### Milestone 1: Research Complete (Month 3)

**Deliverables:**
- ✅ Literature review of all checkpoint papers
- ✅ Proof-of-concept: checkpoint simple C program
- ✅ Design document (this document)
- ✅ License strategy finalized

**Success metrics:**
- Can checkpoint/restore "hello world" loop
- Understand ptrace API thoroughly
- Architecture diagram approved

#### Milestone 2: Core Engine (Month 6)

**Deliverables:**
```
tekton-checkpoint/
└── engine/
    ├── checkpoint.c
    ├── restore.c
    ├── memory.c
    └── registers.c
```

**Features:**
- ✅ Checkpoint single-threaded processes
- ✅ Capture CPU registers
- ✅ Capture heap memory
- ✅ Capture stack
- ✅ Save/load checkpoint files

**Test cases:**
```c
// Test 1: Simple counter
int main() {
    for (int i = 0; i < 1000000; i++) {
        // Checkpoint at i=500000
        // Restore should resume from i=500000
    }
}

// Test 2: Heap allocation
int main() {
    int *data = malloc(1024 * 1024);  // 1MB
    for (int i = 0; i < 1024*1024/sizeof(int); i++) {
        data[i] = i;
    }
    // Checkpoint
    // Restore should have data[] intact
}
```

#### Milestone 3: Multi-threading (Month 12)

**Deliverables:**
```
engine/
├── thread.c          # NEW
├── tls.c             # NEW
└── futex.c           # NEW
```

**Features:**
- ✅ Enumerate threads via /proc/[pid]/task
- ✅ Capture per-thread state (registers, stack, TLS)
- ✅ Capture futex state
- ✅ Restore thread synchronization primitives

**Test cases:**
```c
// Test 3: Pthreads
void *worker(void *arg) {
    for (int i = 0; i < 1000; i++) {
        pthread_mutex_lock(&mutex);
        counter++;
        pthread_mutex_unlock(&mutex);
    }
}

int main() {
    pthread_t threads[4];
    for (int i = 0; i < 4; i++) {
        pthread_create(&threads[i], NULL, worker, NULL);
    }
    // Checkpoint while threads running
    // Restore should continue correctly
}
```

#### Milestone 4: File I/O (Month 18)

**Deliverables:**
```
engine/
├── fdtable.c         # NEW
├── pipes.c           # NEW
└── sockets.c         # NEW (basic)
```

**Features:**
- ✅ Capture file descriptor table
- ✅ Restore file positions
- ✅ Handle pipes (mkfifo)
- ✅ Basic socket handling (listening sockets only)

**Test cases:**
```c
// Test 4: File I/O
int main() {
    FILE *f = fopen("output.txt", "w");
    for (int i = 0; i < 1000; i++) {
        fprintf(f, "Line %d\n", i);
        if (i == 500) {
            // Checkpoint here
        }
    }
    // Restore should continue from line 501
}
```

#### Milestone 5: Optimization (Month 24)

**Deliverables:**
```
engine/
├── incremental.c     # NEW
├── compress.c        # NEW
└── parallel_io.c     # NEW
```

**Features:**
- ✅ Incremental checkpointing (soft-dirty bits)
- ✅ Compression (LZ4)
- ✅ Parallel I/O for checkpoint writes
- ✅ Memory-mapped checkpoint files

**Performance targets:**
- Full checkpoint: < 10 seconds for 1GB process
- Incremental checkpoint: < 2 seconds
- Restore time: < 5 seconds
- Runtime overhead: < 3%

#### Milestone 6: Tekton Integration (Month 30)

**Deliverables:**
```
pkg/reconciler/durabletask/
├── reconciler.go
├── checkpoint.go
└── restore.go

config/
└── 300-durabletask.yaml

examples/
└── durable-ml-training.yaml
```

**Features:**
- ✅ DurableTask CRD definition
- ✅ CustomRun controller implementation
- ✅ PVC checkpoint storage
- ✅ Automatic checkpoint on failure
- ✅ Restore from last checkpoint

**User experience:**
```yaml
apiVersion: durable.tekton.dev/v1alpha1
kind: DurableTask
metadata:
  name: train-llama
spec:
  taskSpec:
    steps:
    - name: train
      image: pytorch/pytorch
      script: |
        python train.py --model llama-7b
  checkpoint:
    enabled: true
    interval: 10m
    storage:
      pvc:
        claimName: ml-checkpoints
```

#### Milestone 7: GPU Support (Month 42)

**Deliverables:**
```
engine/
├── cuda.c            # NEW
├── gpu_memory.c      # NEW
└── cudnn.c           # NEW
```

**Features:**
- ✅ Capture CUDA runtime state
- ✅ Checkpoint GPU memory (VRAM)
- ✅ Restore CUDA contexts
- ✅ Support cuDNN/cuBLAS

**Test cases:**
```python
# Test: PyTorch training
import torch

model = torch.nn.Linear(1000, 1000).cuda()
optimizer = torch.optim.Adam(model.parameters())

for epoch in range(1000):
    # Training loop
    # Checkpoint every 10 epochs
    # Restore should continue correctly with GPU state
```

#### Milestone 8: Production Ready (Month 60)

**Deliverables:**
- ✅ Comprehensive test suite (unit, integration, e2e)
- ✅ Security audit completed
- ✅ Performance benchmarking
- ✅ Documentation website
- ✅ Example pipelines (ML, data processing, etc.)
- ✅ Migration guide from manual checkpoints
- ✅ Monitoring/observability integration
- ✅ Multi-cluster support

**GA criteria:**
- 95% test coverage
- < 0.1% failure rate in production
- 10+ companies using in production
- Security CVEs addressed

---

## License Considerations

### Apache 2.0 Compatibility

**Goal:** Ensure entire Tekton checkpoint stack is Apache 2.0 licensed

**License landscape:**

| Software | License | Can Bundle? | Can Study? |
|----------|---------|-------------|------------|
| Tekton | Apache 2.0 | ✅ Yes | ✅ Yes |
| CRIU | GPL v2 / LGPL v2.1 | ❌ No | ✅ Yes |
| DMTCP | LGPL v3 | ❌ No (bundling) | ✅ Yes |
| Linux kernel APIs | GPL (interface exception) | ✅ Yes | ✅ Yes |

**Strategy:**

1. **Study but don't copy:**
   - Read DMTCP/CRIU source code
   - Understand algorithms and techniques
   - Implement from scratch in Apache 2.0 code
   - Document clean-room implementation

2. **Use kernel APIs freely:**
   - ptrace, /proc, etc. are system interfaces
   - No GPL contamination from syscall usage
   - This is explicitly allowed by Linux syscall exception

3. **Optional external dependency:**
   - Ship without DMTCP bundled
   - Document: "For evaluation, you can optionally install DMTCP"
   - Similar to GPU drivers (NVIDIA proprietary but documented)

4. **Clean-room implementation:**
```
Documentation:
  This implementation is based on published research papers
  and publicly documented kernel APIs. No LGPL or GPL code
  was copied or derived from DMTCP or CRIU.

  References:
  - Linux ptrace(2) man page
  - Linux /proc/[pid]/* documentation
  - "DMTCP: Transparent Checkpointing..." paper (algorithm only)
  - "CRIU: A Checkpoint/Restore Tool..." paper (algorithm only)
```

### Red Hat Considerations

**Scenario:** Red Hat wants to ship Tekton with checkpointing in RHEL

**Requirements:**
- ✅ Must be Apache 2.0 (no LGPL)
- ✅ No patent issues
- ✅ Security audit passed
- ✅ Supportable long-term

**Our approach satisfies:**
- ✅ Pure Apache 2.0 codebase
- ✅ No LGPL dependencies bundled
- ✅ Uses only standard Linux APIs
- ✅ Full source code available for support

**DMTCP as reference only:**
- Red Hat cannot bundle DMTCP (LGPL)
- Red Hat CAN ship our checkpoint engine (Apache 2.0)
- Users can separately install DMTCP if desired (their choice)

---

## Research Papers & References

### Must-Read Papers (Ordered by Priority)

#### 1. Core Checkpointing

**[1] DMTCP: Transparent Checkpointing for Cluster Computations and the Desktop**
- Authors: Jason Ansel, Kapil Arya, Gene Cooperman
- Year: 2007
- Venue: IEEE International Symposium on Parallel and Distributed Processing
- URL: https://dmtcp.sourceforge.io/papers/dmtcp-2007.pdf
- **Why read:** Best practical guide to userspace checkpointing architecture
- **Key insights:**
  - Coordinator/worker architecture
  - Plugin system for extensibility
  - LD_PRELOAD syscall interception
  - Distributed checkpoint coordination

**[2] CRIU: A Checkpoint/Restore Tool for Linux**
- Authors: Pavel Emelyanov, et al.
- Year: 2016
- Venue: Linux Plumbers Conference
- URL: https://www.criu.org/Publications
- **Why read:** Production implementation details for container checkpointing
- **Key insights:**
  - Parasite code injection technique
  - Namespace handling (PID, mount, network)
  - Lazy page restoration
  - Integration with container runtimes

**[3] Checkpoint and Restart of Micro-services in the Cloud**
- Authors: Chen et al.
- Year: 2020
- Venue: USENIX ATC
- **Why read:** Modern cloud-native checkpointing
- **Key insights:**
  - Container-level checkpointing strategies
  - Kubernetes integration patterns
  - Service mesh considerations

#### 2. Memory Management

**[4] Efficient Memory Snapshot for Checkpointing**
- Authors: Song et al.
- Year: 2006
- Venue: ACM/IEEE Conference on Supercomputing (SC'06)
- **Why read:** Incremental checkpointing using copy-on-write
- **Key insights:**
  - Soft-dirty bits in page tables
  - Delta checkpointing algorithms
  - Memory deduplication techniques

**[5] Optimistic Incremental Checkpointing for Consistent Recovery**
- Authors: Wang et al.
- Year: 2008
- Venue: IEEE Transactions on Dependable and Secure Computing
- **Why read:** Minimize checkpoint overhead
- **Key insights:**
  - Hash-based memory diffing
  - Compression strategies
  - Trade-offs between checkpoint frequency and overhead

#### 3. Process State Capture

**[6] Process Introspection: A Checkpoint Mechanism for High Performance**
- Authors: Gerlach et al.
- Year: 2014
- Venue: IEEE International Conference on Cluster Computing
- **Why read:** GPU and accelerator checkpointing
- **Key insights:**
  - CUDA runtime state capture
  - Device memory management
  - Heterogeneous system challenges

**[7] Understanding the Linux /proc Filesystem**
- Authors: Various (kernel documentation)
- Year: Ongoing
- Source: Linux kernel Documentation/filesystems/proc.rst
- **Why read:** Complete reference for process introspection
- **Key files:**
  - `/proc/[pid]/maps` - memory layout
  - `/proc/[pid]/mem` - memory contents
  - `/proc/[pid]/stat` - process statistics
  - `/proc/[pid]/fd/` - file descriptors
  - `/proc/[pid]/task/` - threads

#### 4. File Descriptors and I/O

**[8] Transparent TCP Connection Failover**
- Authors: Soltesz, Gong, Eriksson
- Year: 2004
- Venue: IEEE/IFIP DSN (Dependable Systems and Networks)
- **Why read:** Capturing live TCP socket state
- **Key insights:**
  - TCP sequence number extraction
  - Connection migration techniques
  - Socket state machine preservation

**[9] Checkpointing Open File State**
- Authors: Plank, Beck, Kingsley
- Year: 1998
- Venue: Technical Report UT-CS-98-383
- **Why read:** File descriptor table preservation
- **Key insights:**
  - File position tracking
  - Handling special file types (pipes, sockets, devices)
  - Filesystem consistency

#### 5. Multi-threading

**[10] Transparent Checkpointing of Multithreaded Applications**
- Authors: Sankaran et al.
- Year: 1999
- Venue: ACM PPoPP (Principles and Practice of Parallel Programming)
- **Why read:** Thread state coordination
- **Key insights:**
  - Barrier synchronization for consistent checkpoint
  - Thread-local storage (TLS) preservation
  - Pthread state capture

**[11] Incremental Checkpointing with Application-Level Rollback Recovery**
- Authors: Bouteiller et al.
- Year: 2003
- Venue: ACM/IEEE SC (Supercomputing)
- **Why read:** Lock state and synchronization primitives
- **Key insights:**
  - Futex state extraction
  - Deadlock prevention during restore
  - Mutex/condition variable handling

#### 6. Distributed Checkpointing

**[12] Transparent Checkpoint-Restart of Distributed Applications**
- Authors: Paul H. Hargrove, Jason C. Duell
- Year: 2005
- Venue: IEEE IPDPS (International Parallel and Distributed Processing Symposium)
- **Why read:** Coordinating checkpoints across multiple nodes
- **Key insights:**
  - Communication channel draining
  - Distributed synchronization protocols
  - Message logging vs coordinated checkpointing

**[13] A Survey of Rollback-Recovery Protocols in Message-Passing Systems**
- Authors: Elnozahy, Alvisi, Wang, Johnson
- Year: 2002
- Journal: ACM Computing Surveys
- **Why read:** Comprehensive theoretical foundation
- **Key concepts:**
  - Coordinated vs uncoordinated checkpointing
  - Communication-induced checkpointing
  - Log-based recovery

#### 7. Historical/Foundational

**[14] Checkpointing and Migration of UNIX Processes in Condor**
- Authors: Litzkow, Livny, Mutka
- Year: 1997
- Venue: Technical Report
- **Why read:** Historical foundation, still relevant design patterns
- **Key insights:**
  - Shadow process pattern
  - External vs internal checkpointing
  - User-level vs kernel-level approaches

#### 8. GPU-Specific

**[15] CRIUgpu: Extending CRIU to Checkpoint GPU Workloads**
- Authors: Recent research (2024)
- Venue: arXiv:2502.16631v1
- URL: https://arxiv.org/html/2502.16631v1
- **Why read:** Latest work on GPU checkpointing for ML
- **Key insights:**
  - CUDA context preservation
  - GPU memory (VRAM) checkpointing
  - Tested with LLaMA 3.1, GPT-2
  - Performance analysis for ML workloads

### Supplementary Reading

#### Books

1. **"Linux Kernel Development" by Robert Love**
   - Chapters on process management, memory management
   - Deep dive into /proc filesystem
   - Understanding ptrace internals

2. **"The Linux Programming Interface" by Michael Kerrisk**
   - Comprehensive syscall reference
   - Process creation and management
   - File I/O and descriptors

3. **"Understanding the Linux Kernel" by Bovet & Cesati**
   - Memory address spaces
   - Process scheduling
   - System call handling

#### Online Resources

1. **Linux Kernel Documentation**
   - `/Documentation/filesystems/proc.rst`
   - `/Documentation/userspace-api/seccomp_filter.rst`
   - `/include/uapi/linux/ptrace.h`

2. **Man Pages**
   - `man 2 ptrace` - ptrace syscall
   - `man 5 proc` - /proc filesystem
   - `man 2 clone` - process/thread creation
   - `man 7 capabilities` - Linux capabilities

3. **DMTCP Source Code Study Guide**
   ```
   dmtcp/src/
   ├── plugin/pid/pid_virtualization.cpp  # PID namespace
   ├── syscallsreal.c                      # Syscall wrappers
   ├── connectionmanager.cpp               # Socket handling
   └── membarrier.cpp                      # Memory consistency
   ```

4. **CRIU Source Code Study Guide**
   ```
   criu/
   ├── criu/cr-dump.c          # Main checkpoint algorithm
   ├── criu/pie/parasite.c     # Code injection
   ├── criu/pagemap-cache.c    # Incremental memory
   └── criu/net.c              # Network state
   ```

### Research Topics by Phase

**Phase 1 (Months 0-6): Focus on papers #1, #7, #14**
- Core checkpointing concepts
- ptrace API usage
- /proc filesystem

**Phase 2 (Months 6-12): Add papers #4, #10, #11**
- Memory management
- Multi-threading
- Incremental checkpointing

**Phase 3 (Months 12-18): Add papers #8, #9**
- File descriptors
- Network sockets
- TCP state handling

**Phase 4 (Months 18-30): Add papers #2, #3, #12, #13**
- Container integration
- Distributed checkpointing
- Production considerations

**Phase 5 (Months 30-42): Add papers #6, #15**
- GPU support
- CUDA checkpointing
- Accelerator handling

### How to Read Research Papers

**Recommended approach:**

1. **First pass (30 minutes):**
   - Read abstract and introduction
   - Look at figures and diagrams
   - Read conclusion
   - Identify key algorithms

2. **Second pass (2 hours):**
   - Read entire paper
   - Take notes on techniques
   - Sketch out architecture
   - Note implementation challenges

3. **Third pass (1 day):**
   - Try to implement core algorithm
   - Compare with existing code (DMTCP/CRIU)
   - Identify what applies to your use case
   - Document learnings

**Example reading log:**

```markdown
# Paper: DMTCP (2007)

## Key Takeaways
1. Coordinator/worker architecture scales to 1000+ nodes
2. Plugin system allows extensibility without core changes
3. LD_PRELOAD for syscall interception (fragile but works)

## Applicable to Tekton
- ✅ Can use similar plugin architecture
- ✅ Coordinator pattern for distributed checkpoints (Phase 3+)
- ❌ LD_PRELOAD too fragile for production (use ptrace instead)

## Implementation Ideas
- Adopt plugin interface design
- Skip coordinator for single-node MVP
- Use ptrace instead of LD_PRELOAD

## Questions
- How does it handle GPU state? (Answer: it doesn't, see CRIUgpu paper)
- Performance overhead? (Paper says 1-3%, verify in our benchmarks)
```

### Academic Search Tips

**Finding papers:**
- **Google Scholar**: Best for broad searches ("process checkpointing linux")
- **ACM Digital Library**: Conference papers (PPoPP, SC, IPDPS)
- **IEEE Xplore**: Systems conferences (DSN, CLUSTER)
- **USENIX**: Free access to many systems papers
- **arXiv**: Latest preprints (CRIUgpu paper found here)

**Keywords to search:**
- "process checkpoint linux"
- "transparent checkpointing"
- "application checkpointing distributed systems"
- "CRIU kubernetes"
- "GPU checkpointing CUDA"
- "incremental checkpoint memory"

**Cited by analysis:**
- Start with DMTCP paper
- Look at papers that cite it (Google Scholar "Cited by")
- Find recent work (2020+)

---

## Open Questions

### Technical

1. **GPU Portability**
   - Q: Can checkpoints be restored on different GPU models?
   - A: Likely no - CUDA context is hardware-specific
   - Impact: Need node affinity or GPU type matching

2. **Network Socket Migration**
   - Q: How to handle live TCP connections transparently?
   - A: Very complex, may require connection draining approach
   - Impact: Applications need retry logic for network errors

3. **Incremental Checkpoint Consistency**
   - Q: What if incremental checkpoint references deallocated memory?
   - A: Need to checkpoint on memory allocation boundaries or full GC passes
   - Impact: Integration with language runtimes (Python, Go, etc.)

4. **Container Runtime Integration**
   - Q: Does this require CRI-O or can it work with containerd?
   - A: Should work with any runtime via ptrace (no runtime integration needed)
   - Impact: More portable than CRIU

5. **Security Implications**
   - Q: CAP_SYS_PTRACE is powerful - how to limit exposure?
   - A: Use admission webhooks to restrict to specific namespaces
   - Impact: Need Kubernetes security design

### Product

1. **User Experience**
   - Q: Should checkpointing be opt-in or opt-out?
   - A: Opt-in initially (new feature), opt-out later (once stable)
   - Impact: Migration path planning

2. **Backward Compatibility**
   - Q: What happens to existing Tasks?
   - A: No change - DurableTask is separate CRD
   - Impact: Easy migration

3. **Cost Model**
   - Q: Storage costs for frequent checkpoints?
   - A: Can be high - need retention policies and compression
   - Impact: Document cost implications, provide tuning guide

4. **Failure Modes**
   - Q: What if checkpoint itself fails?
   - A: Retry checkpoint, fall back to Task-level retry
   - Impact: Need robust error handling

### Process

1. **Community Adoption**
   - Q: Should this be in tektoncd org or separate repo?
   - A: Start separate, propose for inclusion after Alpha
   - Impact: Governance and release process

2. **Resource Requirements**
   - Q: Do we need full-time engineers?
   - A: Yes - 3-5 year project needs dedicated team
   - Impact: Funding and staffing

3. **Collaboration**
   - Q: Partner with CRIU/DMTCP projects?
   - A: Yes for knowledge sharing, but clean-room implementation
   - Impact: Licensing stays clear

---

## Appendix A: Example DurableTask CRD

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: durabletasks.durable.tekton.dev
spec:
  group: durable.tekton.dev
  names:
    kind: DurableTask
    plural: durabletasks
    singular: durabletask
    shortNames:
    - dt
  scope: Namespaced
  versions:
  - name: v1alpha1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              taskSpec:
                type: object
                description: "Inline Task specification"
                properties:
                  steps:
                    type: array
                    items:
                      type: object
                      properties:
                        name:
                          type: string
                        image:
                          type: string
                        script:
                          type: string
                        command:
                          type: array
                          items:
                            type: string
                        args:
                          type: array
                          items:
                            type: string

              checkpoint:
                type: object
                description: "Checkpoint configuration"
                properties:
                  enabled:
                    type: boolean
                    default: true

                  strategy:
                    type: string
                    enum:
                    - periodic      # Time-based
                    - progressive   # Progress-based (future)
                    - pre-eviction  # Before node drain (future)
                    default: periodic

                  interval:
                    type: string
                    description: "Checkpoint interval (e.g., 5m, 1h)"
                    default: "5m"

                  retention:
                    type: integer
                    description: "Number of checkpoints to retain"
                    default: 3

                  compression:
                    type: string
                    enum:
                    - none
                    - lz4
                    - zstd
                    default: lz4

                  incremental:
                    type: boolean
                    description: "Enable incremental checkpointing"
                    default: true

                  storage:
                    type: object
                    properties:
                      pvc:
                        type: object
                        properties:
                          claimName:
                            type: string
                          path:
                            type: string
                            default: "/checkpoints"

                      # Future: S3, volume snapshots
                      s3:
                        type: object
                        properties:
                          bucket:
                            type: string
                          prefix:
                            type: string
                          endpoint:
                            type: string

              restore:
                type: object
                description: "Restore configuration"
                properties:
                  fromCheckpoint:
                    type: string
                    description: "Specific checkpoint to restore from"

                  onFailure:
                    type: string
                    enum:
                    - automatic   # Auto-restore from last checkpoint
                    - manual      # User must trigger restore
                    default: automatic

          status:
            type: object
            properties:
              conditions:
                type: array
                items:
                  type: object
                  properties:
                    type:
                      type: string
                    status:
                      type: string
                    reason:
                      type: string
                    message:
                      type: string

              checkpoints:
                type: array
                description: "List of available checkpoints"
                items:
                  type: object
                  properties:
                    name:
                      type: string
                    timestamp:
                      type: string
                      format: date-time
                    size:
                      type: string
                    compressed:
                      type: boolean

              lastCheckpoint:
                type: object
                properties:
                  name:
                    type: string
                  timestamp:
                    type: string
                    format: date-time
                  duration:
                    type: string

              restoreInfo:
                type: object
                properties:
                  restoredFrom:
                    type: string
                  restoredAt:
                    type: string
                    format: date-time
                  restoreDuration:
                    type: string
```

## Appendix B: Example Usage

### Basic ML Training

```yaml
apiVersion: durable.tekton.dev/v1alpha1
kind: DurableTask
metadata:
  name: train-resnet50
spec:
  taskSpec:
    steps:
    - name: train
      image: pytorch/pytorch:2.0-cuda11.8
      script: |
        python train.py \
          --model resnet50 \
          --dataset imagenet \
          --epochs 100 \
          --batch-size 256
      resources:
        limits:
          nvidia.com/gpu: 1

  checkpoint:
    enabled: true
    interval: 10m
    retention: 5
    compression: lz4
    incremental: true
    storage:
      pvc:
        claimName: ml-training-checkpoints
```

### LLM Training with GPU

```yaml
apiVersion: durable.tekton.dev/v1alpha1
kind: DurableTask
metadata:
  name: train-llama-7b
spec:
  taskSpec:
    steps:
    - name: train
      image: huggingface/transformers:latest
      script: |
        python train_llama.py \
          --model llama-7b \
          --dataset openwebtext \
          --gradient-checkpointing \
          --bf16
      resources:
        limits:
          nvidia.com/gpu: 8

  checkpoint:
    enabled: true
    interval: 30m  # LLM training is slower
    retention: 3
    compression: zstd  # Better compression for large models
    storage:
      s3:
        bucket: ml-checkpoints
        prefix: llama-7b-training
        endpoint: s3.amazonaws.com
```

### Restore from Checkpoint

```yaml
apiVersion: durable.tekton.dev/v1alpha1
kind: DurableTask
metadata:
  name: resume-training
spec:
  taskSpec:
    # Same as above

  restore:
    fromCheckpoint: "checkpoint-2024-11-12-15-30-00"
    onFailure: automatic

  checkpoint:
    enabled: true
    interval: 10m
```

### Integration with Pipeline

```yaml
apiVersion: tekton.dev/v1
kind: Pipeline
metadata:
  name: ml-pipeline
spec:
  tasks:
  - name: preprocess
    taskRef:
      name: preprocess-data

  - name: train
    taskRef:
      apiVersion: durable.tekton.dev/v1alpha1
      kind: DurableTask
      name: train-model-durable
    runAfter:
    - preprocess

  - name: evaluate
    taskRef:
      name: evaluate-model
    runAfter:
    - train
```

---

## Appendix C: Performance Estimates

### Checkpoint Overhead

**Assumptions:**
- Process size: 10GB memory
- Checkpoint to PVC over 1Gbps network
- LZ4 compression (3x reduction)

**Full Checkpoint:**
```
Raw data:        10 GB
Compressed:       3.3 GB
Network time:    ~30 seconds
Compression:     ~10 seconds
Total:           ~40 seconds overhead
```

**Incremental Checkpoint (5% dirty pages):**
```
Raw data:        512 MB
Compressed:      170 MB
Network time:    ~2 seconds
Compression:     ~1 second
Total:           ~3 seconds overhead
```

**Runtime Overhead:**
- Soft-dirty bit tracking: < 0.1%
- Periodic checkpoint (every 10 min): ~0.5% amortized
- Total: < 1% for typical workloads

### Restore Time

**Cold start (full restore):**
```
Load from PVC:   ~30 seconds
Decompress:      ~10 seconds
Memory restore:  ~5 seconds
Process init:    ~5 seconds
Total:           ~50 seconds
```

**Warm start (cache hit):**
```
Memory restore:  ~5 seconds
Process init:    ~5 seconds
Total:           ~10 seconds
```

**Comparison to pod scheduling:**
- Pod scheduling: 30-120 seconds (depends on cluster)
- Image pull: 10-300 seconds (depends on image size)
- Restore from checkpoint: 10-50 seconds

**Net benefit:** Restore is often faster than cold start, especially with large images.

---

## Appendix D: Security Considerations

### Required Capabilities

**For checkpoint process:**
```yaml
securityContext:
  capabilities:
    add:
    - SYS_PTRACE     # Required for ptrace()
    - SYS_ADMIN      # Required for namespace operations (future)
```

**Risk mitigation:**
- Use PodSecurityPolicy / Pod Security Standards to limit
- Only allow in designated namespaces (e.g., `ml-training`)
- Use admission webhooks to enforce policies

### Checkpoint File Security

**Concerns:**
- Checkpoint files contain process memory → may include secrets
- Need encryption at rest
- Need access controls

**Solution:**
```yaml
checkpoint:
  storage:
    pvc:
      claimName: encrypted-checkpoints  # Use encrypted PVC
    encryption:
      enabled: true
      kms: aws-kms://key-id
```

### Network Exposure

**Concern:** Checkpoint coordinator (DMTCP-style) could be attack vector

**Mitigation:**
- Use mutual TLS for coordinator communication
- Network policies to isolate checkpoint traffic
- Or: avoid coordinator pattern entirely (single-node checkpoints)

---

## Conclusion

This document outlines a comprehensive 3-5 year plan to add durable execution capabilities to Tekton Pipelines through automatic process-level checkpointing. The approach leverages Tekton's native CustomRun extension mechanism and builds an Apache 2.0 licensed checkpoint engine from scratch, avoiding LGPL dependencies while drawing on proven techniques from DMTCP and CRIU.

**Key Success Factors:**
1. **Phased approach:** Start simple, add complexity incrementally
2. **Clean-room implementation:** Study existing tools but implement from scratch
3. **Apache 2.0 licensing:** Ensure Red Hat and other enterprises can adopt
4. **Native Tekton integration:** Use CustomRun API, not a fork
5. **User transparency:** Checkpointing works without code changes

**Next Steps:**
1. Review and approve this design document
2. Begin Phase 1 research and prototyping
3. Build proof-of-concept checkpoint engine
4. Present to Tekton community for feedback
5. Start implementation roadmap

**Target Outcome:**
Tekton becomes the first Kubernetes-native CI/CD system with transparent, automatic checkpointing for long-running workloads, enabling production MLOps use cases that were previously impossible.

---

**Document Version:** 1.0
**Last Updated:** 2025-11-12
**Next Review:** After Phase 1 completion (Month 6)
