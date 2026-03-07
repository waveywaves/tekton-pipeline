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

package v1

import (
	"context"
	"fmt"

	"github.com/tektoncd/pipeline/pkg/apis/config"
	"knative.dev/pkg/apis"
)

// Loop is used to iterate a PipelineTask until a condition is met.
// Each iteration creates a new TaskRun. Results from one iteration
// can feed into the next via $(loop.previousResult.<name>).
//
// Loop is mutually exclusive with Matrix: Matrix fans out in parallel,
// Loop iterates sequentially with feedback.
type Loop struct {
	// MaxIterations is the maximum number of iterations to run.
	// This is a safety limit to prevent infinite loops.
	// Required field.
	MaxIterations int `json:"maxIterations"`

	// Until is a CEL expression that is evaluated after each iteration.
	// When it evaluates to true, the loop terminates.
	// The expression has access to:
	//   - loop.iteration: current iteration number (0-indexed)
	//   - loop.previousResult.<name>: results from the previous iteration's TaskRun
	// If not specified, the loop runs for exactly MaxIterations.
	// +optional
	Until string `json:"until,omitempty"`

	// IterationParams are additional parameters passed to each iteration.
	// Values can reference $(loop.iteration) for the current iteration number
	// and $(loop.previousResult.<name>) for results from the previous iteration.
	// +optional
	IterationParams Params `json:"iterationParams,omitempty"`
}

// LoopIterationState tracks the state of a single loop iteration in the PipelineRun status.
type LoopIterationState struct {
	// Iteration is the iteration number (0-indexed).
	Iteration int `json:"iteration"`

	// TaskRunName is the name of the TaskRun for this iteration.
	TaskRunName string `json:"taskRunName,omitempty"`

	// Status is the status of this iteration: Running, Succeeded, Failed.
	Status string `json:"status,omitempty"`

	// Results are the results from this iteration's TaskRun.
	// +optional
	Results []TaskRunResult `json:"results,omitempty"`
}

// LoopState tracks the overall loop state for a PipelineTask in the PipelineRun status.
type LoopState struct {
	// PipelineTaskName is the name of the PipelineTask this loop belongs to.
	PipelineTaskName string `json:"pipelineTaskName"`

	// CurrentIteration is the current iteration number.
	CurrentIteration int `json:"currentIteration"`

	// MaxIterations is the configured maximum.
	MaxIterations int `json:"maxIterations"`

	// Converged is true if the Until condition evaluated to true.
	Converged bool `json:"converged,omitempty"`

	// Iterations is the history of all iteration states.
	// +optional
	// +listType=atomic
	Iterations []LoopIterationState `json:"iterations,omitempty"`
}

// HasLoop returns true if the Loop is configured.
func (l *Loop) HasLoop() bool {
	return l != nil && l.MaxIterations > 0
}

// IsLooped returns true if the PipelineTask has a Loop configuration.
func (pt *PipelineTask) IsLooped() bool {
	return pt.Loop != nil && pt.Loop.HasLoop()
}

// validateLoop validates the Loop configuration on a PipelineTask.
func (pt *PipelineTask) validateLoop(ctx context.Context) (errs *apis.FieldError) {
	if pt.Loop == nil {
		return nil
	}

	// Loop is an alpha feature — require alpha API fields to be enabled
	errs = errs.Also(config.ValidateEnabledAPIFields(ctx, "loop", config.AlphaAPIFields))

	// MaxIterations must be positive
	if pt.Loop.MaxIterations <= 0 {
		errs = errs.Also(apis.ErrInvalidValue(pt.Loop.MaxIterations, "loop.maxIterations",
			"maxIterations must be a positive integer"))
	}

	// Enforce a configurable upper bound on MaxIterations
	maxAllowed := config.FromContextOrDefaults(ctx).Defaults.DefaultMaxMatrixCombinationsCount
	if pt.Loop.MaxIterations > maxAllowed {
		errs = errs.Also(apis.ErrOutOfBoundsValue(pt.Loop.MaxIterations, 1, maxAllowed, "loop.maxIterations"))
	}

	// Loop and Matrix are mutually exclusive
	if pt.IsMatrixed() {
		errs = errs.Also(apis.ErrMultipleOneOf("loop", "matrix"))
	}

	// Validate Until is a non-empty string if provided (CEL validation happens at runtime)
	// No-op for now — CEL parsing can be added later

	// Validate IterationParams don't duplicate regular Params
	if len(pt.Loop.IterationParams) > 0 {
		regularParamNames := pt.Params.ExtractNames()
		for _, iterParam := range pt.Loop.IterationParams {
			if regularParamNames.Has(iterParam.Name) {
				errs = errs.Also(apis.ErrMultipleOneOf(
					fmt.Sprintf("loop.iterationParams[%s]", iterParam.Name),
					fmt.Sprintf("params[%s]", iterParam.Name),
				))
			}
		}
	}

	return errs
}

// validateLoopAndMatrix ensures loop and matrix are not used together across the pipeline.
func validateLoop(ctx context.Context, tasks []PipelineTask) (errs *apis.FieldError) {
	for i, task := range tasks {
		errs = errs.Also(task.validateLoop(ctx).ViaFieldIndex("tasks", i))
	}
	return errs
}
