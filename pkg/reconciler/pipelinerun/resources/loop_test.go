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

package resources_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/reconciler/pipelinerun/resources"
	"github.com/tektoncd/pipeline/test/diff"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newPipelineRun(name string) *v1.PipelineRun {
	return &v1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
	}
}

func TestLoopGetOrCreateLoopState(t *testing.T) {
	t.Run("creates new loop state when none exists", func(t *testing.T) {
		pr := newPipelineRun("test-pr")
		ls := resources.GetOrCreateLoopState(pr, "my-task", 10)

		if ls == nil {
			t.Fatal("GetOrCreateLoopState returned nil")
		}
		if ls.PipelineTaskName != "my-task" {
			t.Errorf("expected PipelineTaskName 'my-task', got %q", ls.PipelineTaskName)
		}
		if ls.MaxIterations != 10 {
			t.Errorf("expected MaxIterations 10, got %d", ls.MaxIterations)
		}
		if ls.CurrentIteration != 0 {
			t.Errorf("expected CurrentIteration 0, got %d", ls.CurrentIteration)
		}
		if ls.Converged {
			t.Error("expected Converged to be false")
		}
		if len(pr.Status.LoopStates) != 1 {
			t.Errorf("expected 1 LoopState in status, got %d", len(pr.Status.LoopStates))
		}
	})

	t.Run("returns existing loop state", func(t *testing.T) {
		pr := newPipelineRun("test-pr")
		pr.Status.LoopStates = []v1.LoopState{{
			PipelineTaskName: "my-task",
			CurrentIteration: 3,
			MaxIterations:    10,
			Converged:        false,
		}}

		ls := resources.GetOrCreateLoopState(pr, "my-task", 10)

		if ls == nil {
			t.Fatal("GetOrCreateLoopState returned nil")
		}
		if ls.CurrentIteration != 3 {
			t.Errorf("expected existing CurrentIteration 3, got %d", ls.CurrentIteration)
		}
		// Should not have added a new entry
		if len(pr.Status.LoopStates) != 1 {
			t.Errorf("expected 1 LoopState in status, got %d", len(pr.Status.LoopStates))
		}
	})

	t.Run("creates separate state for different task", func(t *testing.T) {
		pr := newPipelineRun("test-pr")
		pr.Status.LoopStates = []v1.LoopState{{
			PipelineTaskName: "task-a",
			CurrentIteration: 2,
			MaxIterations:    5,
		}}

		ls := resources.GetOrCreateLoopState(pr, "task-b", 8)

		if ls.PipelineTaskName != "task-b" {
			t.Errorf("expected PipelineTaskName 'task-b', got %q", ls.PipelineTaskName)
		}
		if len(pr.Status.LoopStates) != 2 {
			t.Errorf("expected 2 LoopStates in status, got %d", len(pr.Status.LoopStates))
		}
	})
}

func TestLoopIsComplete(t *testing.T) {
	tests := []struct {
		name string
		ls   *v1.LoopState
		want bool
	}{{
		name: "nil loop state",
		ls:   nil,
		want: false,
	}, {
		name: "not complete - iteration less than max",
		ls: &v1.LoopState{
			CurrentIteration: 2,
			MaxIterations:    10,
			Converged:        false,
		},
		want: false,
	}, {
		name: "complete - max iterations reached",
		ls: &v1.LoopState{
			CurrentIteration: 10,
			MaxIterations:    10,
			Converged:        false,
		},
		want: true,
	}, {
		name: "complete - exceeded max iterations",
		ls: &v1.LoopState{
			CurrentIteration: 15,
			MaxIterations:    10,
			Converged:        false,
		},
		want: true,
	}, {
		name: "complete - converged",
		ls: &v1.LoopState{
			CurrentIteration: 3,
			MaxIterations:    10,
			Converged:        true,
		},
		want: true,
	}, {
		name: "complete - converged at max",
		ls: &v1.LoopState{
			CurrentIteration: 10,
			MaxIterations:    10,
			Converged:        true,
		},
		want: true,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resources.IsLoopComplete(tt.ls)
			if d := cmp.Diff(tt.want, got); d != "" {
				t.Errorf("IsLoopComplete() %s", diff.PrintWantGot(d))
			}
		})
	}
}

func TestLoopGetTaskRunName(t *testing.T) {
	tests := []struct {
		name             string
		pipelineRunName  string
		pipelineTaskName string
		iteration        int
		want             string
	}{{
		name:             "first iteration",
		pipelineRunName:  "my-pipeline-run",
		pipelineTaskName: "my-task",
		iteration:        0,
		want:             "my-pipeline-run-my-task-loop-0",
	}, {
		name:             "fifth iteration",
		pipelineRunName:  "pr",
		pipelineTaskName: "train",
		iteration:        4,
		want:             "pr-train-loop-4",
	}, {
		name:             "large iteration number",
		pipelineRunName:  "run",
		pipelineTaskName: "task",
		iteration:        999,
		want:             "run-task-loop-999",
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resources.GetLoopTaskRunName(tt.pipelineRunName, tt.pipelineTaskName, tt.iteration)
			if d := cmp.Diff(tt.want, got); d != "" {
				t.Errorf("GetLoopTaskRunName() %s", diff.PrintWantGot(d))
			}
		})
	}
}

func TestLoopApplyContextToParams(t *testing.T) {
	tests := []struct {
		name            string
		params          v1.Params
		iteration       int
		previousResults map[string]string
		want            v1.Params
	}{{
		name: "substitute loop.iteration",
		params: v1.Params{{
			Name:  "epoch",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "$(loop.iteration)"},
		}},
		iteration:       3,
		previousResults: map[string]string{},
		want: v1.Params{{
			Name:  "epoch",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "3"},
		}},
	}, {
		name: "substitute loop.previousResult",
		params: v1.Params{{
			Name:  "model-path",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "$(loop.previousResult.checkpoint)"},
		}},
		iteration: 1,
		previousResults: map[string]string{
			"checkpoint": "/data/model-v1.bin",
		},
		want: v1.Params{{
			Name:  "model-path",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "/data/model-v1.bin"},
		}},
	}, {
		name: "substitute both iteration and previousResult",
		params: v1.Params{{
			Name:  "message",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "iter-$(loop.iteration)-loss-$(loop.previousResult.loss)"},
		}},
		iteration: 5,
		previousResults: map[string]string{
			"loss": "0.42",
		},
		want: v1.Params{{
			Name:  "message",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "iter-5-loss-0.42"},
		}},
	}, {
		name: "no substitution needed",
		params: v1.Params{{
			Name:  "static",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "hello-world"},
		}},
		iteration:       0,
		previousResults: map[string]string{},
		want: v1.Params{{
			Name:  "static",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "hello-world"},
		}},
	}, {
		name: "multiple params with mixed substitutions",
		params: v1.Params{{
			Name:  "iter",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "$(loop.iteration)"},
		}, {
			Name:  "prev",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "$(loop.previousResult.accuracy)"},
		}, {
			Name:  "constant",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "fixed"},
		}},
		iteration: 2,
		previousResults: map[string]string{
			"accuracy": "0.95",
		},
		want: v1.Params{{
			Name:  "iter",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "2"},
		}, {
			Name:  "prev",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "0.95"},
		}, {
			Name:  "constant",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "fixed"},
		}},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resources.ApplyLoopContextToParams(tt.params, tt.iteration, tt.previousResults)
			if d := cmp.Diff(tt.want, got); d != "" {
				t.Errorf("ApplyLoopContextToParams() %s", diff.PrintWantGot(d))
			}
		})
	}
}

func TestLoopEvaluateUntilCondition(t *testing.T) {
	tests := []struct {
		name            string
		untilExpr       string
		iteration       int
		previousResults map[string]string
		want            bool
		wantErr         bool
	}{{
		name:            "empty expression returns false (no condition)",
		untilExpr:       "",
		iteration:       0,
		previousResults: map[string]string{},
		want:            false,
		wantErr:         false,
	}, {
		name:            "true expression",
		untilExpr:       "true",
		iteration:       0,
		previousResults: map[string]string{},
		want:            true,
		wantErr:         false,
	}, {
		name:            "false expression",
		untilExpr:       "false",
		iteration:       0,
		previousResults: map[string]string{},
		want:            false,
		wantErr:         false,
	}, {
		name:            "numeric comparison with iteration substitution",
		untilExpr:       "$(loop.iteration) >= 5",
		iteration:       5,
		previousResults: map[string]string{},
		want:            true,
		wantErr:         false,
	}, {
		name:            "numeric comparison not yet met",
		untilExpr:       "$(loop.iteration) >= 5",
		iteration:       3,
		previousResults: map[string]string{},
		want:            false,
		wantErr:         false,
	}, {
		name:      "string comparison with previousResult",
		untilExpr: "'$(loop.previousResult.status)' == 'converged'",
		iteration: 2,
		previousResults: map[string]string{
			"status": "converged",
		},
		want:    true,
		wantErr: false,
	}, {
		name:      "string comparison not matched",
		untilExpr: "'$(loop.previousResult.status)' == 'converged'",
		iteration: 1,
		previousResults: map[string]string{
			"status": "training",
		},
		want:    false,
		wantErr: false,
	}, {
		name:            "invalid CEL expression",
		untilExpr:       "this is not valid CEL %%%",
		iteration:       0,
		previousResults: map[string]string{},
		want:            false,
		wantErr:         true,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resources.EvaluateLoopUntilCondition(tt.untilExpr, tt.iteration, tt.previousResults)
			if (err != nil) != tt.wantErr {
				t.Errorf("EvaluateLoopUntilCondition() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("EvaluateLoopUntilCondition() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoopAdvanceIteration(t *testing.T) {
	t.Run("advances counter and continues", func(t *testing.T) {
		ls := &v1.LoopState{
			PipelineTaskName: "task",
			CurrentIteration: 0,
			MaxIterations:    5,
			Iterations: []v1.LoopIterationState{{
				Iteration:   0,
				TaskRunName: "pr-task-loop-0",
				Status:      v1.LoopIterationStatusSucceeded,
				Results: []v1.TaskRunResult{{
					Name:  "output",
					Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "result-0"},
				}},
			}},
		}

		shouldContinue, err := resources.AdvanceLoopIteration(ls, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !shouldContinue {
			t.Error("expected loop to continue, got false")
		}
		if ls.CurrentIteration != 1 {
			t.Errorf("expected CurrentIteration 1, got %d", ls.CurrentIteration)
		}
	})

	t.Run("stops at max iterations", func(t *testing.T) {
		ls := &v1.LoopState{
			PipelineTaskName: "task",
			CurrentIteration: 4,
			MaxIterations:    5,
			Iterations: []v1.LoopIterationState{{
				Iteration:   4,
				TaskRunName: "pr-task-loop-4",
				Status:      v1.LoopIterationStatusSucceeded,
			}},
		}

		shouldContinue, err := resources.AdvanceLoopIteration(ls, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if shouldContinue {
			t.Error("expected loop to stop at max iterations, got true")
		}
		if ls.CurrentIteration != 5 {
			t.Errorf("expected CurrentIteration 5, got %d", ls.CurrentIteration)
		}
		if ls.TerminationReason != v1.LoopTerminationReasonMaxIterationsReached {
			t.Errorf("expected TerminationReason %q, got %q",
				v1.LoopTerminationReasonMaxIterationsReached, ls.TerminationReason)
		}
	})

	t.Run("stops on convergence", func(t *testing.T) {
		ls := &v1.LoopState{
			PipelineTaskName: "task",
			CurrentIteration: 2,
			MaxIterations:    10,
			Iterations: []v1.LoopIterationState{{
				Iteration:   2,
				TaskRunName: "pr-task-loop-2",
				Status:      v1.LoopIterationStatusSucceeded,
			}},
		}

		// The expression "true" will always evaluate to true, causing convergence
		shouldContinue, err := resources.AdvanceLoopIteration(ls, "true")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if shouldContinue {
			t.Error("expected loop to stop on convergence, got true")
		}
		if !ls.Converged {
			t.Error("expected Converged to be true")
		}
		if ls.TerminationReason != v1.LoopTerminationReasonConverged {
			t.Errorf("expected TerminationReason %q, got %q",
				v1.LoopTerminationReasonConverged, ls.TerminationReason)
		}
		if ls.CurrentIteration != 3 {
			t.Errorf("expected CurrentIteration 3, got %d", ls.CurrentIteration)
		}
	})

	t.Run("returns error on invalid CEL expression", func(t *testing.T) {
		ls := &v1.LoopState{
			PipelineTaskName: "task",
			CurrentIteration: 0,
			MaxIterations:    10,
			Iterations: []v1.LoopIterationState{{
				Iteration:   0,
				TaskRunName: "pr-task-loop-0",
				Status:      v1.LoopIterationStatusSucceeded,
			}},
		}

		_, err := resources.AdvanceLoopIteration(ls, "invalid CEL %%%")
		if err == nil {
			t.Error("expected error for invalid CEL expression, got nil")
		}
	})
}

func TestLoopRecordIteration(t *testing.T) {
	t.Run("records new iteration", func(t *testing.T) {
		ls := &v1.LoopState{
			PipelineTaskName: "task",
			CurrentIteration: 0,
			MaxIterations:    5,
		}

		results := []v1.TaskRunResult{{
			Name:  "loss",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "0.5"},
		}}
		resources.RecordLoopIteration(ls, 0, "pr-task-loop-0", v1.LoopIterationStatusSucceeded, results)

		if len(ls.Iterations) != 1 {
			t.Fatalf("expected 1 iteration recorded, got %d", len(ls.Iterations))
		}
		if ls.Iterations[0].Iteration != 0 {
			t.Errorf("expected iteration number 0, got %d", ls.Iterations[0].Iteration)
		}
		if ls.Iterations[0].TaskRunName != "pr-task-loop-0" {
			t.Errorf("expected TaskRunName 'pr-task-loop-0', got %q", ls.Iterations[0].TaskRunName)
		}
		if ls.Iterations[0].Status != v1.LoopIterationStatusSucceeded {
			t.Errorf("expected Status 'Succeeded', got %q", ls.Iterations[0].Status)
		}
		if len(ls.Iterations[0].Results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(ls.Iterations[0].Results))
		}
		if ls.Iterations[0].Results[0].Name != "loss" {
			t.Errorf("expected result name 'loss', got %q", ls.Iterations[0].Results[0].Name)
		}
	})

	t.Run("updates existing iteration", func(t *testing.T) {
		ls := &v1.LoopState{
			PipelineTaskName: "task",
			CurrentIteration: 1,
			MaxIterations:    5,
			Iterations: []v1.LoopIterationState{{
				Iteration:   0,
				TaskRunName: "pr-task-loop-0",
				Status:      v1.LoopIterationStatusRunning,
			}},
		}

		results := []v1.TaskRunResult{{
			Name:  "loss",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "0.3"},
		}}
		resources.RecordLoopIteration(ls, 0, "pr-task-loop-0", v1.LoopIterationStatusSucceeded, results)

		if len(ls.Iterations) != 1 {
			t.Fatalf("expected 1 iteration (updated), got %d", len(ls.Iterations))
		}
		if ls.Iterations[0].Status != v1.LoopIterationStatusSucceeded {
			t.Errorf("expected updated Status 'Succeeded', got %q", ls.Iterations[0].Status)
		}
		if len(ls.Iterations[0].Results) != 1 {
			t.Fatalf("expected 1 result after update, got %d", len(ls.Iterations[0].Results))
		}
	})

	t.Run("appends second iteration", func(t *testing.T) {
		ls := &v1.LoopState{
			PipelineTaskName: "task",
			CurrentIteration: 1,
			MaxIterations:    5,
			Iterations: []v1.LoopIterationState{{
				Iteration:   0,
				TaskRunName: "pr-task-loop-0",
				Status:      v1.LoopIterationStatusSucceeded,
			}},
		}

		resources.RecordLoopIteration(ls, 1, "pr-task-loop-1", v1.LoopIterationStatusSucceeded, nil)

		if len(ls.Iterations) != 2 {
			t.Fatalf("expected 2 iterations, got %d", len(ls.Iterations))
		}
		if ls.Iterations[1].Iteration != 1 {
			t.Errorf("expected second iteration number 1, got %d", ls.Iterations[1].Iteration)
		}
	})
}

func TestLoopGetLatestIterationResults(t *testing.T) {
	tests := []struct {
		name string
		ls   *v1.LoopState
		want map[string]string
	}{{
		name: "nil loop state",
		ls:   nil,
		want: map[string]string{},
	}, {
		name: "empty iterations",
		ls: &v1.LoopState{
			PipelineTaskName: "task",
			Iterations:       []v1.LoopIterationState{},
		},
		want: map[string]string{},
	}, {
		name: "single iteration with results",
		ls: &v1.LoopState{
			PipelineTaskName: "task",
			Iterations: []v1.LoopIterationState{{
				Iteration:   0,
				TaskRunName: "pr-task-loop-0",
				Status:      v1.LoopIterationStatusSucceeded,
				Results: []v1.TaskRunResult{{
					Name:  "loss",
					Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "0.5"},
				}, {
					Name:  "accuracy",
					Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "0.85"},
				}},
			}},
		},
		want: map[string]string{
			"loss":     "0.5",
			"accuracy": "0.85",
		},
	}, {
		name: "multiple iterations - returns latest",
		ls: &v1.LoopState{
			PipelineTaskName: "task",
			Iterations: []v1.LoopIterationState{{
				Iteration:   0,
				TaskRunName: "pr-task-loop-0",
				Status:      v1.LoopIterationStatusSucceeded,
				Results: []v1.TaskRunResult{{
					Name:  "loss",
					Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "0.9"},
				}},
			}, {
				Iteration:   1,
				TaskRunName: "pr-task-loop-1",
				Status:      v1.LoopIterationStatusSucceeded,
				Results: []v1.TaskRunResult{{
					Name:  "loss",
					Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "0.3"},
				}},
			}},
		},
		want: map[string]string{
			"loss": "0.3",
		},
	}, {
		name: "iteration with no results",
		ls: &v1.LoopState{
			PipelineTaskName: "task",
			Iterations: []v1.LoopIterationState{{
				Iteration:   0,
				TaskRunName: "pr-task-loop-0",
				Status:      v1.LoopIterationStatusSucceeded,
			}},
		},
		want: map[string]string{},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resources.GetLatestLoopIterationResults(tt.ls)
			if d := cmp.Diff(tt.want, got); d != "" {
				t.Errorf("GetLatestLoopIterationResults() %s", diff.PrintWantGot(d))
			}
		})
	}
}

func TestLoopGetTaskRunNameOverflow(t *testing.T) {
	t.Run("long names are capped at 63 chars", func(t *testing.T) {
		got := resources.GetLoopTaskRunName(
			"very-long-pipeline-run-name-that-is-way-too-long",
			"very-long-task-name-that-is-also-too-long",
			42,
		)
		if len(got) > 63 {
			t.Errorf("expected name length <= 63, got %d: %q", len(got), got)
		}
	})

	t.Run("short names are unmodified", func(t *testing.T) {
		got := resources.GetLoopTaskRunName("pr", "task", 0)
		want := "pr-task-loop-0"
		if got != want {
			t.Errorf("expected %q, got %q", want, got)
		}
	})

	t.Run("truncated name ends with iteration suffix", func(t *testing.T) {
		got := resources.GetLoopTaskRunName(
			"very-long-pipeline-run-name-that-is-way-too-long",
			"very-long-task-name-that-is-also-too-long",
			7,
		)
		if len(got) > 63 {
			t.Errorf("expected name length <= 63, got %d: %q", len(got), got)
		}
		// Must end with the iteration suffix for deterministic lookups
		wantSuffix := "-loop-7"
		if got[len(got)-len(wantSuffix):] != wantSuffix {
			t.Errorf("expected name to end with %q, got %q", wantSuffix, got)
		}
	})
}

func TestLoopIsCompleteWithTerminationReason(t *testing.T) {
	tests := []struct {
		name string
		ls   *v1.LoopState
		want bool
	}{{
		name: "complete - IterationFailed termination reason",
		ls: &v1.LoopState{
			CurrentIteration:  2,
			MaxIterations:     10,
			Converged:         false,
			TerminationReason: v1.LoopTerminationReasonIterationFailed,
		},
		want: true,
	}, {
		name: "not complete - no termination reason and iteration < max",
		ls: &v1.LoopState{
			CurrentIteration: 2,
			MaxIterations:    10,
			Converged:        false,
		},
		want: false,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resources.IsLoopComplete(tt.ls)
			if got != tt.want {
				t.Errorf("IsLoopComplete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoopApplyContextToParamsUnresolvedPlaceholders(t *testing.T) {
	t.Run("unresolved previousResult placeholders replaced with empty string on iter 0", func(t *testing.T) {
		params := v1.Params{{
			Name:  "model-path",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "$(loop.previousResult.checkpoint)"},
		}}
		got := resources.ApplyLoopContextToParams(params, 0, map[string]string{})
		want := v1.Params{{
			Name:  "model-path",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: ""},
		}}
		if d := cmp.Diff(want, got); d != "" {
			t.Errorf("ApplyLoopContextToParams() %s", diff.PrintWantGot(d))
		}
	})

	t.Run("mixed placeholder with static text on iter 0", func(t *testing.T) {
		params := v1.Params{{
			Name:  "message",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "prev=$(loop.previousResult.loss)-iter=$(loop.iteration)"},
		}}
		got := resources.ApplyLoopContextToParams(params, 0, map[string]string{})
		want := v1.Params{{
			Name:  "message",
			Value: v1.ParamValue{Type: v1.ParamTypeString, StringVal: "prev=-iter=0"},
		}}
		if d := cmp.Diff(want, got); d != "" {
			t.Errorf("ApplyLoopContextToParams() %s", diff.PrintWantGot(d))
		}
	})
}
