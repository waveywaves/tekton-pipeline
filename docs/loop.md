<!--
---
linkTitle: "Loop"
weight: 407
---
-->

# Loop

- [Overview](#overview)
- [Configuring a Loop](#configuring-a-loop)
  - [MaxIterations](#maxiterations)
  - [Until (Early Termination)](#until-early-termination)
  - [IterationParams](#iterationparams)
- [Context Variables](#context-variables)
- [Results](#results)
- [Status Tracking](#status-tracking)
- [Comparison with Matrix](#comparison-with-matrix)
- [Limitations](#limitations)
- [Examples](#examples)
  - [Fixed Iteration Count](#fixed-iteration-count)
  - [Loop with Until Condition](#loop-with-until-condition)
  - [Chaining Results Across Iterations](#chaining-results-across-iterations)

## Overview

`Loop` is used to iterate a `PipelineTask` sequentially until a condition is met or a maximum number of iterations is
reached. Each iteration creates a new `TaskRun`. Results from one iteration can feed into the next via context
variables, enabling iterative refinement workflows such as ML training loops, retry-with-backoff patterns, and
convergence-based processing.

`Loop` is mutually exclusive with `Matrix`: `Matrix` fans out `Tasks` in parallel with static cardinality, while `Loop`
iterates sequentially with dynamic termination.

> **`Loop` is an [alpha](additional-configs.md) feature.**
> The `enable-api-fields` feature flag must be set to `"alpha"` to use `Loop` in a `PipelineTask`.

## Configuring a Loop

A `Loop` is configured on a `PipelineTask` with the following fields:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `maxIterations` | int | Yes | Maximum number of iterations (safety limit). Must be positive. |
| `until` | string | No | CEL expression evaluated after each iteration. Loop stops when it returns `true`. |
| `iterationParams` | Params | No | Parameters passed to each iteration, supporting loop context variable substitution. |

### MaxIterations

`maxIterations` is a required field that sets the upper bound on the number of iterations. This serves as a safety limit
to prevent infinite loops. The loop will run for exactly this many iterations unless an `until` condition terminates it
earlier.

```yaml
tasks:
  - name: retry-task
    taskSpec:
      # ...
    loop:
      maxIterations: 10
```

The maximum allowed value for `maxIterations` is governed by the `default-max-matrix-combinations-count` setting in the
Tekton Pipelines config (the same limit used by `Matrix`).

### Until (Early Termination)

`until` is an optional CEL expression that is evaluated after each iteration completes. When it evaluates to `true`,
the loop terminates early. The expression can reference loop context variables to inspect results from the previous
iteration.

```yaml
tasks:
  - name: train
    taskSpec:
      results:
        - name: converged
          type: string
      # ...
    loop:
      maxIterations: 100
      until: "'$(loop.previousResult.converged)' == 'true'"
```

If `until` is not specified, the loop runs for exactly `maxIterations` iterations.

The CEL expression is evaluated with loop context variables substituted before evaluation. The expression must return a
boolean value.

### IterationParams

`iterationParams` are parameters passed to each iteration of the loop. Their values can reference loop context
variables such as `$(loop.iteration)` and `$(loop.previousResult.<name>)`. These parameters are separate from the
regular `params` on a `PipelineTask` and must not duplicate them.

```yaml
tasks:
  - name: count
    taskSpec:
      params:
        - name: current-iteration
          type: string
      # ...
    loop:
      maxIterations: 5
      iterationParams:
        - name: current-iteration
          value: "$(loop.iteration)"
```

## Context Variables

The following context variables are available within a looped `PipelineTask`:

| Variable | Description |
|----------|-------------|
| `$(loop.iteration)` | Current iteration number (0-indexed). |
| `$(loop.previousResult.<name>)` | The value of result `<name>` from the previous iteration's `TaskRun`. On the first iteration (iteration 0), this is not resolved (no previous results exist). |

These variables can be used in:
- `loop.iterationParams[*].value` -- to pass iteration-specific values to the `Task`
- `loop.until` -- to build termination conditions based on previous results

## Results

Each iteration of a looped `PipelineTask` produces its own set of `TaskRun` results. These results are:

1. **Recorded in `LoopState.Iterations[].Results`** in the `PipelineRun` status, providing a full history of every
   iteration's output.
2. **Available to the next iteration** via `$(loop.previousResult.<name>)`, enabling result chaining.
3. **Available to downstream tasks** after the loop completes. When a downstream task references
   `$(tasks.<looped-task>.results.<name>)`, it receives the result from the **last completed iteration**.

## Status Tracking

Loop execution state is tracked in the `PipelineRun` status under the `loopStates` field:

```yaml
status:
  loopStates:
    - pipelineTaskName: train
      currentIteration: 5
      maxIterations: 100
      converged: true
      iterations:
        - iteration: 0
          taskRunName: my-run-train-loop-0
          status: Succeeded
          results:
            - name: fitness
              type: string
              value: "50"
        - iteration: 1
          taskRunName: my-run-train-loop-1
          status: Succeeded
          results:
            - name: fitness
              type: string
              value: "150"
        # ...
```

### LoopState Fields

| Field | Description |
|-------|-------------|
| `pipelineTaskName` | Name of the `PipelineTask` this loop belongs to. |
| `currentIteration` | Current iteration counter (equals total completed iterations when loop is done). |
| `maxIterations` | Configured maximum from the `Loop` spec. |
| `converged` | `true` if the `until` condition evaluated to `true`. |
| `iterations` | Array of per-iteration state including `TaskRun` name, status, and results. |

### TaskRun Naming

Each loop iteration's `TaskRun` is named using the pattern:

```
<pipelinerun-name>-<pipelinetask-name>-loop-<iteration>
```

For example, a `PipelineRun` named `my-run` with a looped `PipelineTask` named `train` produces:

```
my-run-train-loop-0
my-run-train-loop-1
my-run-train-loop-2
```

## Comparison with Matrix

| Aspect | Matrix | Loop |
|--------|--------|------|
| Execution model | Parallel fan-out | Sequential iteration |
| Cardinality | Static (determined at pipeline start) | Dynamic (can terminate early via `until`) |
| Result flow | Independent per combination | Chained: previous iteration results feed next |
| Use case | Build/test across platforms | Training loops, convergence, retry with feedback |
| TaskRun count | All created at once | One at a time |
| Mutual exclusivity | Cannot use with Loop | Cannot use with Matrix |

## Limitations

- **Sequential only**: Loop iterations run one at a time. There is no parallel iteration mode.
- **CEL evaluation**: The `until` expression is evaluated between iterations by the reconciler. Complex CEL expressions
  may add latency.
- **No nested loops**: A looped `PipelineTask` cannot itself contain another loop.
- **First iteration has no previous results**: `$(loop.previousResult.<name>)` is not resolved on iteration 0.
  Use a default value in your `Task` param or handle the unresolved placeholder in your script.
- **MaxIterations limit**: Subject to the same configurable upper bound as `Matrix` combinations
  (`default-max-matrix-combinations-count`).

## Examples

### Fixed Iteration Count

A simple loop that runs a task 5 times, passing the iteration number as a parameter:

```yaml
apiVersion: tekton.dev/v1
kind: Pipeline
metadata:
  name: loop-demo
spec:
  tasks:
    - name: count
      taskSpec:
        params:
          - name: current-iteration
            type: string
        results:
          - name: iteration
            type: string
          - name: message
            type: string
        steps:
          - name: echo
            image: alpine
            script: |
              echo "Iteration: $(params.current-iteration)"
              echo -n "$(params.current-iteration)" | tee $(results.iteration.path)
              echo -n "Hello from iteration $(params.current-iteration)" | tee $(results.message.path)
      loop:
        maxIterations: 5
        iterationParams:
          - name: current-iteration
            value: "$(loop.iteration)"
    - name: summary
      runAfter:
        - count
      taskSpec:
        steps:
          - name: report
            image: alpine
            script: |
              echo "Loop completed!"
```

### Loop with Until Condition

A training simulation that loops until a fitness threshold is reached:

```yaml
apiVersion: tekton.dev/v1
kind: Pipeline
metadata:
  name: loop-until-demo
spec:
  tasks:
    - name: train
      taskSpec:
        params:
          - name: iteration
            type: string
        results:
          - name: fitness
            type: string
          - name: converged
            type: string
        steps:
          - name: simulate-training
            image: alpine
            script: |
              ITERATION=$(params.iteration)
              FITNESS=$((ITERATION * 100 + 50))
              CONVERGED="false"
              if [ "$FITNESS" -gt 400 ]; then
                CONVERGED="true"
              fi
              echo "Iteration $ITERATION: fitness=$FITNESS converged=$CONVERGED"
              echo -n "$FITNESS" | tee $(results.fitness.path)
              echo -n "$CONVERGED" | tee $(results.converged.path)
      loop:
        maxIterations: 100
        until: "'$(loop.previousResult.converged)' == 'true'"
        iterationParams:
          - name: iteration
            value: "$(loop.iteration)"
```

### Chaining Results Across Iterations

A loop where each iteration reads the previous iteration's output and builds on it:

```yaml
apiVersion: tekton.dev/v1
kind: Pipeline
metadata:
  name: accumulator-demo
spec:
  tasks:
    - name: accumulate
      taskSpec:
        params:
          - name: iteration
            type: string
          - name: previous-count
            type: string
            default: "0"
        results:
          - name: count
            type: string
        steps:
          - name: increment
            image: alpine
            script: |
              PREV=$(params.previous-count)
              NEXT=$((PREV + 1))
              echo "Iteration $(params.iteration): count=$NEXT"
              echo -n "$NEXT" | tee $(results.count.path)
      loop:
        maxIterations: 10
        iterationParams:
          - name: iteration
            value: "$(loop.iteration)"
          - name: previous-count
            value: "$(loop.previousResult.count)"
```

After 10 iterations, the final `count` result will be `10`.
