//go:build e2e

/*
Copyright 2026 The Tekton Authors

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

package test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/test/parse"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	knativetest "knative.dev/pkg/test"
	"knative.dev/pkg/test/helpers"
)

// TestPipelineRunLoopMaxIterations verifies that a PipelineTask with a Loop
// field runs sequentially for the configured number of iterations. The test
// creates a Pipeline with a looped task that echoes the current iteration
// number and writes it as a result, followed by a summary task that runs
// after the loop completes. It asserts that exactly MaxIterations TaskRuns
// are created with the correct iteration parameter values.
// @test:execution=parallel
func TestPipelineRunLoopMaxIterations(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c, namespace := setup(ctx, t, requireAnyGate(map[string]string{
		"enable-api-fields": "alpha",
	}))
	knativetest.CleanupOnInterrupt(func() { tearDown(ctx, t, c, namespace) }, t.Logf)
	defer tearDown(ctx, t, c, namespace)

	pipelineName := helpers.ObjectNameForTest(t)
	pipelineRunName := helpers.ObjectNameForTest(t)

	t.Logf("Creating Pipeline %s in namespace %s", pipelineName, namespace)

	pipeline := parse.MustParseV1Pipeline(t, fmt.Sprintf(`
metadata:
  name: %s
  namespace: %s
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
            image: mirror.gcr.io/alpine
            script: |
              echo "Iteration: $(params.current-iteration)"
              echo -n "$(params.current-iteration)" | tee $(results.iteration.path)
              echo -n "Hello from iteration $(params.current-iteration)" | tee $(results.message.path)
      loop:
        maxIterations: 3
        iterationParams:
          - name: current-iteration
            value: "$(loop.iteration)"
    - name: summary
      runAfter:
        - count
      taskSpec:
        steps:
          - name: report
            image: mirror.gcr.io/alpine
            script: |
              echo "Loop completed!"
`, pipelineName, namespace))

	pipelineRun := parse.MustParseV1PipelineRun(t, fmt.Sprintf(`
metadata:
  name: %s
  namespace: %s
spec:
  pipelineRef:
    name: %s
`, pipelineRunName, namespace, pipelineName))

	if _, err := c.V1PipelineClient.Create(ctx, pipeline, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Failed to create Pipeline %q: %s", pipelineName, err)
	}

	if _, err := c.V1PipelineRunClient.Create(ctx, pipelineRun, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Failed to create PipelineRun %q: %s", pipelineRunName, err)
	}

	t.Logf("Waiting for PipelineRun %s in namespace %s to complete", pipelineRunName, namespace)
	if err := WaitForPipelineRunState(ctx, c, pipelineRunName, timeout, PipelineRunSucceed(pipelineRunName), "PipelineRunSuccess", v1Version); err != nil {
		t.Fatalf("Error waiting for PipelineRun %s to finish: %s", pipelineRunName, err)
	}

	// Verify the PipelineRun completed successfully
	pr, err := c.V1PipelineRunClient.Get(ctx, pipelineRunName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get PipelineRun %q: %s", pipelineRunName, err)
	}

	// Verify LoopState is tracked in the PipelineRun status
	if len(pr.Status.LoopStates) == 0 {
		t.Fatal("Expected LoopStates to be populated in PipelineRun status, but found none")
	}

	var loopState *v1.LoopState
	for i := range pr.Status.LoopStates {
		if pr.Status.LoopStates[i].PipelineTaskName == "count" {
			loopState = &pr.Status.LoopStates[i]
			break
		}
	}
	if loopState == nil {
		t.Fatal("Expected LoopState for PipelineTask 'count' but found none")
	}

	// Verify loop ran for 3 iterations
	if loopState.MaxIterations != 3 {
		t.Errorf("Expected MaxIterations=3, got %d", loopState.MaxIterations)
	}
	if loopState.CurrentIteration != 3 {
		t.Errorf("Expected CurrentIteration=3 (completed all iterations), got %d", loopState.CurrentIteration)
	}
	if len(loopState.Iterations) != 3 {
		t.Errorf("Expected 3 iteration records, got %d", len(loopState.Iterations))
	}

	// Verify all iterations succeeded
	for _, iter := range loopState.Iterations {
		if iter.Status != "Succeeded" {
			t.Errorf("Expected iteration %d status to be 'Succeeded', got %q", iter.Iteration, iter.Status)
		}
	}

	// Verify the TaskRuns were created with correct labels
	actualTaskRunList, err := c.V1TaskRunClient.List(ctx, metav1.ListOptions{
		LabelSelector: "tekton.dev/pipelineRun=" + pipelineRunName,
	})
	if err != nil {
		t.Fatalf("Error listing TaskRuns for PipelineRun %s: %s", pipelineRunName, err)
	}

	// Expect 3 loop TaskRuns + 1 summary TaskRun = 4 total
	if len(actualTaskRunList.Items) != 4 {
		t.Errorf("Expected 4 TaskRuns (3 loop iterations + 1 summary), got %d", len(actualTaskRunList.Items))
		for _, tr := range actualTaskRunList.Items {
			t.Logf("  TaskRun: %s", tr.Name)
		}
	}

	// Verify each loop TaskRun has the expected iteration parameter
	expectedLoopTaskRuns := []v1.TaskRun{}
	for i := 0; i < 3; i++ {
		expectedLoopTaskRuns = append(expectedLoopTaskRuns, v1.TaskRun{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-count-loop-%d", pipelineRunName, i),
			},
			Status: v1.TaskRunStatus{
				Status: duckv1.Status{Conditions: []apis.Condition{{
					Type:   apis.ConditionSucceeded,
					Status: "True",
					Reason: "Succeeded",
				}}},
				TaskRunStatusFields: v1.TaskRunStatusFields{
					Artifacts: &v1.Artifacts{},
					Results: []v1.TaskRunResult{{
						Name:  "iteration",
						Type:  v1.ResultsTypeString,
						Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: fmt.Sprintf("%d", i)},
					}, {
						Name:  "message",
						Type:  v1.ResultsTypeString,
						Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: fmt.Sprintf("Hello from iteration %d", i)},
					}},
				},
			},
		})
	}

	ignoreTypeMeta := cmpopts.IgnoreFields(metav1.TypeMeta{}, "Kind", "APIVersion")
	ignoreObjectMeta := cmpopts.IgnoreFields(metav1.ObjectMeta{}, "Namespace", "UID", "Generation", "CreationTimestamp", "OwnerReferences", "ManagedFields", "Labels", "ResourceVersion", "Annotations")
	ignoreTaskRunStatusFields := cmpopts.IgnoreFields(v1.TaskRunStatusFields{}, "StartTime", "CompletionTime", "PodName", "Steps", "TaskSpec", "Provenance", "Sidecars")
	ignoreConditionMessage := cmpopts.IgnoreFields(apis.Condition{}, "Message")
	ignoreLastTransitionTime := cmpopts.IgnoreFields(apis.Condition{}, "LastTransitionTime.Inner.Time")
	ignoreSpec := cmpopts.IgnoreFields(v1.TaskRun{}, "Spec")
	sortTaskRuns := cmpopts.SortSlices(func(x, y v1.TaskRun) bool { return x.Name < y.Name })

	// Filter to only loop TaskRuns (exclude the summary TaskRun)
	var actualLoopTaskRuns []v1.TaskRun
	for _, tr := range actualTaskRunList.Items {
		for _, expected := range expectedLoopTaskRuns {
			if tr.Name == expected.Name {
				actualLoopTaskRuns = append(actualLoopTaskRuns, tr)
				break
			}
		}
	}

	if d := cmp.Diff(expectedLoopTaskRuns, actualLoopTaskRuns,
		ignoreTypeMeta, ignoreObjectMeta, ignoreTaskRunStatusFields,
		ignoreConditionMessage, ignoreLastTransitionTime, ignoreSpec,
		sortTaskRuns,
	); d != "" {
		t.Fatalf("Loop TaskRuns did not match expected (-want +got):\n%s", d)
	}

	t.Logf("Successfully finished test TestPipelineRunLoopMaxIterations")
}

// TestPipelineRunLoopUntilCondition verifies that a PipelineTask with a Loop
// and an Until CEL expression terminates early when the condition is met.
// The test creates a Pipeline where a looped task simulates a training process
// that "converges" after a few iterations, and asserts that the loop stops
// before reaching MaxIterations when the Until condition evaluates to true.
// @test:execution=parallel
func TestPipelineRunLoopUntilCondition(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c, namespace := setup(ctx, t, requireAnyGate(map[string]string{
		"enable-api-fields": "alpha",
	}))
	knativetest.CleanupOnInterrupt(func() { tearDown(ctx, t, c, namespace) }, t.Logf)
	defer tearDown(ctx, t, c, namespace)

	pipelineName := helpers.ObjectNameForTest(t)
	pipelineRunName := helpers.ObjectNameForTest(t)

	t.Logf("Creating Pipeline %s in namespace %s", pipelineName, namespace)

	// This pipeline simulates a training loop. Each iteration computes a
	// fitness score as iteration * 100 + 50. The Until condition checks if
	// the "converged" result from the previous iteration is "true".
	// Iteration 0: fitness=50,  converged=false
	// Iteration 1: fitness=150, converged=false
	// Iteration 2: fitness=250, converged=false
	// Iteration 3: fitness=350, converged=false
	// Iteration 4: fitness=450, converged=true  -> loop stops after this
	pipeline := parse.MustParseV1Pipeline(t, fmt.Sprintf(`
metadata:
  name: %s
  namespace: %s
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
            image: mirror.gcr.io/alpine
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
    - name: report
      runAfter:
        - train
      taskSpec:
        steps:
          - name: done
            image: mirror.gcr.io/alpine
            script: |
              echo "Training converged!"
`, pipelineName, namespace))

	pipelineRun := parse.MustParseV1PipelineRun(t, fmt.Sprintf(`
metadata:
  name: %s
  namespace: %s
spec:
  pipelineRef:
    name: %s
`, pipelineRunName, namespace, pipelineName))

	if _, err := c.V1PipelineClient.Create(ctx, pipeline, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Failed to create Pipeline %q: %s", pipelineName, err)
	}

	if _, err := c.V1PipelineRunClient.Create(ctx, pipelineRun, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Failed to create PipelineRun %q: %s", pipelineRunName, err)
	}

	t.Logf("Waiting for PipelineRun %s in namespace %s to complete", pipelineRunName, namespace)
	if err := WaitForPipelineRunState(ctx, c, pipelineRunName, timeout, PipelineRunSucceed(pipelineRunName), "PipelineRunSuccess", v1Version); err != nil {
		t.Fatalf("Error waiting for PipelineRun %s to finish: %s", pipelineRunName, err)
	}

	// Verify the PipelineRun completed successfully
	pr, err := c.V1PipelineRunClient.Get(ctx, pipelineRunName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get PipelineRun %q: %s", pipelineRunName, err)
	}

	// Verify LoopState is present and shows convergence
	if len(pr.Status.LoopStates) == 0 {
		t.Fatal("Expected LoopStates to be populated in PipelineRun status, but found none")
	}

	var loopState *v1.LoopState
	for i := range pr.Status.LoopStates {
		if pr.Status.LoopStates[i].PipelineTaskName == "train" {
			loopState = &pr.Status.LoopStates[i]
			break
		}
	}
	if loopState == nil {
		t.Fatal("Expected LoopState for PipelineTask 'train' but found none")
	}

	// Verify the loop converged (Until condition was met)
	if !loopState.Converged {
		t.Error("Expected LoopState.Converged to be true, but it was false")
	}

	// Verify the loop ran for 5 iterations (0 through 4), NOT the full 100
	// Iteration 4 produces fitness=450 which triggers converged=true.
	// The Until condition is evaluated after iteration 4 completes, so the
	// loop stops with CurrentIteration=5 (advanced past the last completed).
	expectedIterationCount := 5
	if loopState.CurrentIteration != expectedIterationCount {
		t.Errorf("Expected CurrentIteration=%d (loop converged after iteration 4), got %d",
			expectedIterationCount, loopState.CurrentIteration)
	}
	if len(loopState.Iterations) != expectedIterationCount {
		t.Errorf("Expected %d iteration records, got %d", expectedIterationCount, len(loopState.Iterations))
	}

	// Verify all iterations succeeded
	for _, iter := range loopState.Iterations {
		if iter.Status != "Succeeded" {
			t.Errorf("Expected iteration %d status to be 'Succeeded', got %q", iter.Iteration, iter.Status)
		}
	}

	// Verify the final iteration's results
	if len(loopState.Iterations) > 0 {
		lastIter := loopState.Iterations[len(loopState.Iterations)-1]
		var fitnessResult, convergedResult string
		for _, r := range lastIter.Results {
			switch r.Name {
			case "fitness":
				fitnessResult = r.Value.StringVal
			case "converged":
				convergedResult = r.Value.StringVal
			}
		}
		if fitnessResult != "450" {
			t.Errorf("Expected final iteration fitness=450, got %q", fitnessResult)
		}
		if convergedResult != "true" {
			t.Errorf("Expected final iteration converged=true, got %q", convergedResult)
		}
	}

	// Verify the total number of TaskRuns: 5 loop iterations + 1 report = 6
	actualTaskRunList, err := c.V1TaskRunClient.List(ctx, metav1.ListOptions{
		LabelSelector: "tekton.dev/pipelineRun=" + pipelineRunName,
	})
	if err != nil {
		t.Fatalf("Error listing TaskRuns for PipelineRun %s: %s", pipelineRunName, err)
	}
	expectedTotalTaskRuns := 6 // 5 loop + 1 report
	if len(actualTaskRunList.Items) != expectedTotalTaskRuns {
		t.Errorf("Expected %d TaskRuns (%d loop iterations + 1 report), got %d",
			expectedTotalTaskRuns, expectedIterationCount, len(actualTaskRunList.Items))
		for _, tr := range actualTaskRunList.Items {
			t.Logf("  TaskRun: %s", tr.Name)
		}
	}

	t.Logf("Successfully finished test TestPipelineRunLoopUntilCondition")
}

// TestPipelineRunLoopPreviousResults verifies that $(loop.previousResult.<name>)
// variables are correctly substituted across loop iterations. Each iteration
// reads a counter from the previous iteration's result and increments it,
// verifying the result-chaining mechanism.
// @test:execution=parallel
func TestPipelineRunLoopPreviousResults(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c, namespace := setup(ctx, t, requireAnyGate(map[string]string{
		"enable-api-fields": "alpha",
	}))
	knativetest.CleanupOnInterrupt(func() { tearDown(ctx, t, c, namespace) }, t.Logf)
	defer tearDown(ctx, t, c, namespace)

	pipelineName := helpers.ObjectNameForTest(t)
	pipelineRunName := helpers.ObjectNameForTest(t)

	t.Logf("Creating Pipeline %s in namespace %s", pipelineName, namespace)

	// Each iteration reads previous-count from the previous iteration
	// (defaulting to "0" for iteration 0), increments it, and writes it as
	// a result. After 4 iterations we expect the count to be 4.
	pipeline := parse.MustParseV1Pipeline(t, fmt.Sprintf(`
metadata:
  name: %s
  namespace: %s
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
            image: mirror.gcr.io/alpine
            script: |
              PREV=$(params.previous-count)
              # On iteration 0 the previous result placeholder is not yet resolved,
              # so default to 0 if the value still contains the placeholder or is empty
              if echo "$PREV" | grep -q 'loop.previousResult'; then
                PREV=0
              fi
              NEXT=$((PREV + 1))
              echo "Iteration $(params.iteration): previous=$PREV next=$NEXT"
              echo -n "$NEXT" | tee $(results.count.path)
      loop:
        maxIterations: 4
        iterationParams:
          - name: iteration
            value: "$(loop.iteration)"
          - name: previous-count
            value: "$(loop.previousResult.count)"
`, pipelineName, namespace))

	pipelineRun := parse.MustParseV1PipelineRun(t, fmt.Sprintf(`
metadata:
  name: %s
  namespace: %s
spec:
  pipelineRef:
    name: %s
`, pipelineRunName, namespace, pipelineName))

	if _, err := c.V1PipelineClient.Create(ctx, pipeline, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Failed to create Pipeline %q: %s", pipelineName, err)
	}

	if _, err := c.V1PipelineRunClient.Create(ctx, pipelineRun, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Failed to create PipelineRun %q: %s", pipelineRunName, err)
	}

	t.Logf("Waiting for PipelineRun %s in namespace %s to complete", pipelineRunName, namespace)
	if err := WaitForPipelineRunState(ctx, c, pipelineRunName, timeout, PipelineRunSucceed(pipelineRunName), "PipelineRunSuccess", v1Version); err != nil {
		t.Fatalf("Error waiting for PipelineRun %s to finish: %s", pipelineRunName, err)
	}

	// Verify the PipelineRun completed
	pr, err := c.V1PipelineRunClient.Get(ctx, pipelineRunName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get PipelineRun %q: %s", pipelineRunName, err)
	}

	// Find the loop state
	var loopState *v1.LoopState
	for i := range pr.Status.LoopStates {
		if pr.Status.LoopStates[i].PipelineTaskName == "accumulate" {
			loopState = &pr.Status.LoopStates[i]
			break
		}
	}
	if loopState == nil {
		t.Fatal("Expected LoopState for PipelineTask 'accumulate' but found none")
	}

	// Verify 4 iterations ran
	if len(loopState.Iterations) != 4 {
		t.Fatalf("Expected 4 iteration records, got %d", len(loopState.Iterations))
	}

	// Verify each iteration's count result incremented correctly
	for i, iter := range loopState.Iterations {
		expectedCount := fmt.Sprintf("%d", i+1)
		var countResult string
		for _, r := range iter.Results {
			if r.Name == "count" {
				countResult = r.Value.StringVal
			}
		}
		if countResult != expectedCount {
			t.Errorf("Iteration %d: expected count=%q, got %q", i, expectedCount, countResult)
		}
	}

	// Verify the last iteration produced count=4
	lastIter := loopState.Iterations[len(loopState.Iterations)-1]
	var finalCount string
	for _, r := range lastIter.Results {
		if r.Name == "count" {
			finalCount = r.Value.StringVal
		}
	}
	if finalCount != "4" {
		t.Errorf("Expected final count=4, got %q", finalCount)
	}

	t.Logf("Successfully finished test TestPipelineRunLoopPreviousResults")
}
