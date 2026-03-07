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

package pipelinerun

import (
	"context"
	"fmt"

	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/reconciler/pipelinerun/resources"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/controller"
	logging "knative.dev/pkg/logging"
)

// handleLoopedTask manages the iteration lifecycle for a PipelineTask with a Loop.
// On each reconciliation:
//  1. If no iterations started yet → create the first TaskRun
//  2. If current iteration's TaskRun is running → do nothing (wait)
//  3. If current iteration's TaskRun completed → record results, evaluate Until,
//     advance iteration, create next TaskRun (or mark loop done)
func (c *Reconciler) handleLoopedTask(
	ctx context.Context,
	rpt *resources.ResolvedPipelineTask,
	pr *v1.PipelineRun,
	facts *resources.PipelineRunFacts,
) error {
	logger := logging.FromContext(ctx)
	recorder := controller.GetEventRecorder(ctx)
	loop := rpt.PipelineTask.Loop
	taskName := rpt.PipelineTask.Name

	// Get or create loop state
	ls := resources.GetOrCreateLoopState(pr, taskName, loop.MaxIterations)

	// If loop is already complete, nothing to do
	if resources.IsLoopComplete(ls) {
		return nil
	}

	// Check if there's a TaskRun for the current iteration
	currentTaskRunName := resources.GetLoopTaskRunName(pr.Name, taskName, ls.CurrentIteration)

	var currentTaskRun *v1.TaskRun
	for _, tr := range rpt.TaskRuns {
		if tr.Name == currentTaskRunName {
			currentTaskRun = tr
			break
		}
	}

	// Case 1: No TaskRun for current iteration exists → create it
	if currentTaskRun == nil {
		logger.Infof("Loop task %q: creating TaskRun for iteration %d/%d",
			taskName, ls.CurrentIteration, ls.MaxIterations)

		// Apply loop context to iteration params
		previousResults := resources.GetLatestLoopIterationResults(ls)
		allParams := rpt.PipelineTask.Params
		if len(loop.IterationParams) > 0 {
			appliedIterParams := resources.ApplyLoopContextToParams(
				loop.IterationParams, ls.CurrentIteration, previousResults)
			allParams = append(allParams, appliedIterParams...)
		}

		// Override the TaskRun name for this iteration
		rpt.TaskRunNames = []string{currentTaskRunName}

		// Create the TaskRun
		taskRuns, err := c.createTaskRuns(ctx, rpt, pr, facts)
		if err != nil {
			recorder.Eventf(pr, corev1.EventTypeWarning, "LoopTaskRunCreationFailed",
				"Failed to create TaskRun for loop iteration %d of %q: %v",
				ls.CurrentIteration, taskName, err)
			return fmt.Errorf("error creating TaskRun %s for loop iteration %d of %s: %w",
				currentTaskRunName, ls.CurrentIteration, taskName, err)
		}
		rpt.TaskRuns = append(rpt.TaskRuns, taskRuns...)

		// Record the iteration as started
		resources.RecordLoopIteration(ls, ls.CurrentIteration, currentTaskRunName, "Running", nil)

		logger.Infof("Loop task %q: iteration %d TaskRun %q created",
			taskName, ls.CurrentIteration, currentTaskRunName)
		return nil
	}

	// Case 2: TaskRun exists but not done → wait
	if !currentTaskRun.IsDone() {
		return nil
	}

	// Case 3: TaskRun completed → record results and advance
	if currentTaskRun.IsSuccessful() {
		// Record successful iteration with results
		resources.RecordLoopIteration(ls, ls.CurrentIteration, currentTaskRunName,
			"Succeeded", currentTaskRun.Status.Results)

		logger.Infof("Loop task %q: iteration %d completed successfully",
			taskName, ls.CurrentIteration)

		// Advance to next iteration (evaluates Until condition)
		shouldContinue, err := resources.AdvanceLoopIteration(ls, loop.Until)
		if err != nil {
			logger.Errorf("Loop task %q: error evaluating Until condition: %v", taskName, err)
			return controller.NewPermanentError(
				fmt.Errorf("error evaluating loop Until condition for %s: %w", taskName, err))
		}

		if shouldContinue {
			logger.Infof("Loop task %q: advancing to iteration %d/%d",
				taskName, ls.CurrentIteration, ls.MaxIterations)
			// The next reconciliation will create the TaskRun for the new iteration
		} else {
			reason := "max iterations reached"
			if ls.Converged {
				reason = "Until condition met"
			}
			logger.Infof("Loop task %q: loop complete after %d iterations (%s)",
				taskName, ls.CurrentIteration, reason)
		}
	} else {
		// TaskRun failed — record and stop the loop
		resources.RecordLoopIteration(ls, ls.CurrentIteration, currentTaskRunName,
			"Failed", currentTaskRun.Status.Results)

		logger.Warnf("Loop task %q: iteration %d failed, stopping loop",
			taskName, ls.CurrentIteration)

		// Mark as converged to stop the loop (failed state)
		ls.Converged = true
	}

	return nil
}
