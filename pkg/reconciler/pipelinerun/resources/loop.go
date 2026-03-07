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

package resources

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/google/cel-go/cel"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// GetLoopState returns the LoopState for a PipelineTask from the PipelineRun status,
// or nil if no loop state exists yet.
func GetLoopState(pr *v1.PipelineRun, pipelineTaskName string) *v1.LoopState {
	for i := range pr.Status.LoopStates {
		if pr.Status.LoopStates[i].PipelineTaskName == pipelineTaskName {
			return &pr.Status.LoopStates[i]
		}
	}
	return nil
}

// GetOrCreateLoopState returns the existing LoopState for a PipelineTask,
// or creates and appends a new one to the PipelineRun status.
func GetOrCreateLoopState(pr *v1.PipelineRun, pipelineTaskName string, maxIterations int) *v1.LoopState {
	if ls := GetLoopState(pr, pipelineTaskName); ls != nil {
		return ls
	}
	ls := v1.LoopState{
		PipelineTaskName: pipelineTaskName,
		CurrentIteration: 0,
		MaxIterations:    maxIterations,
		Converged:        false,
	}
	pr.Status.LoopStates = append(pr.Status.LoopStates, ls)
	return &pr.Status.LoopStates[len(pr.Status.LoopStates)-1]
}

// IsLoopComplete returns true if the loop has finished — either converged or hit max iterations.
func IsLoopComplete(ls *v1.LoopState) bool {
	if ls == nil {
		return false
	}
	return ls.Converged || ls.CurrentIteration >= ls.MaxIterations
}

// GetCurrentLoopIteration returns the current iteration number for a looped task.
// Returns 0 if no loop state exists yet.
func GetCurrentLoopIteration(pr *v1.PipelineRun, pipelineTaskName string) int {
	ls := GetLoopState(pr, pipelineTaskName)
	if ls == nil {
		return 0
	}
	return ls.CurrentIteration
}

// GetLoopTaskRunName generates the name for a TaskRun at a specific iteration.
// Format: <pipelinerun>-<pipelinetask>-loop-<iteration>
func GetLoopTaskRunName(pipelineRunName, pipelineTaskName string, iteration int) string {
	return fmt.Sprintf("%s-%s-loop-%d", pipelineRunName, pipelineTaskName, iteration)
}

// GetLatestLoopIterationResults returns the results from the most recently
// completed iteration, if any.
func GetLatestLoopIterationResults(ls *v1.LoopState) map[string]string {
	results := make(map[string]string)
	if ls == nil || len(ls.Iterations) == 0 {
		return results
	}
	latest := ls.Iterations[len(ls.Iterations)-1]
	for _, r := range latest.Results {
		results[r.Name] = r.Value.StringVal
	}
	return results
}

// ApplyLoopContextToParams substitutes loop context variables in params:
//   - $(loop.iteration) -> current iteration number
//   - $(loop.previousResult.<name>) -> result from previous iteration
func ApplyLoopContextToParams(params v1.Params, iteration int, previousResults map[string]string) v1.Params {
	applied := make(v1.Params, len(params))
	for i, p := range params {
		p.DeepCopyInto(&applied[i])
		val := applied[i].Value.StringVal

		// Replace $(loop.iteration)
		val = strings.ReplaceAll(val, "$(loop.iteration)", strconv.Itoa(iteration))

		// Replace $(loop.previousResult.<name>)
		for name, result := range previousResults {
			placeholder := fmt.Sprintf("$(loop.previousResult.%s)", name)
			val = strings.ReplaceAll(val, placeholder, result)
		}

		applied[i].Value.StringVal = val
	}
	return applied
}

// EvaluateLoopUntilCondition evaluates the Until CEL expression with loop context.
// Returns true if the loop should STOP (condition is met).
func EvaluateLoopUntilCondition(untilExpr string, iteration int, previousResults map[string]string) (bool, error) {
	if untilExpr == "" {
		return false, nil // No condition means iterate until MaxIterations
	}

	// Substitute loop variables into the CEL expression before evaluation
	expr := strings.ReplaceAll(untilExpr, "$(loop.iteration)", strconv.Itoa(iteration))
	for name, result := range previousResults {
		placeholder := fmt.Sprintf("$(loop.previousResult.%s)", name)
		expr = strings.ReplaceAll(expr, placeholder, fmt.Sprintf("'%s'", result))
	}

	// Create CEL environment and evaluate
	env, err := cel.NewEnv()
	if err != nil {
		return false, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	ast, iss := env.Compile(expr)
	if iss.Err() != nil {
		return false, fmt.Errorf("failed to compile CEL expression %q: %w", expr, iss.Err())
	}

	prg, err := env.Program(ast)
	if err != nil {
		return false, fmt.Errorf("failed to create CEL program: %w", err)
	}

	out, _, err := prg.Eval(map[string]interface{}{})
	if err != nil {
		return false, fmt.Errorf("failed to evaluate CEL expression %q: %w", expr, err)
	}

	result, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("CEL expression %q did not return a boolean, got %T", expr, out.Value())
	}

	return result, nil
}

// RecordLoopIteration records a completed iteration in the LoopState.
func RecordLoopIteration(ls *v1.LoopState, iteration int, taskRunName string, status string, results []v1.TaskRunResult) {
	iterState := v1.LoopIterationState{
		Iteration:   iteration,
		TaskRunName: taskRunName,
		Status:      status,
		Results:     results,
	}

	// Update or append
	for i := range ls.Iterations {
		if ls.Iterations[i].Iteration == iteration {
			ls.Iterations[i] = iterState
			return
		}
	}
	ls.Iterations = append(ls.Iterations, iterState)
}

// AdvanceLoopIteration advances the loop to the next iteration.
// Returns true if the loop should continue, false if it's done.
func AdvanceLoopIteration(ls *v1.LoopState, untilExpr string) (bool, error) {
	// Get results from the just-completed iteration
	previousResults := GetLatestLoopIterationResults(ls)

	// Advance iteration counter
	ls.CurrentIteration++

	// Check max iterations
	if ls.CurrentIteration >= ls.MaxIterations {
		return false, nil
	}

	// Evaluate Until condition if present
	if untilExpr != "" {
		converged, err := EvaluateLoopUntilCondition(untilExpr, ls.CurrentIteration, previousResults)
		if err != nil {
			return false, err
		}
		if converged {
			ls.Converged = true
			return false, nil
		}
	}

	return true, nil
}

// GetLoopIterationCount returns the number of TaskRuns to create for a looped task
// at the current reconciliation. For loops, this is always 1 (one iteration at a time).
// For non-looped tasks, returns 0.
func GetLoopIterationCount(pt *v1.PipelineTask) int {
	if pt.IsLooped() {
		return 1
	}
	return 0
}
