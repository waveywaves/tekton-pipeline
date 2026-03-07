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
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tektoncd/pipeline/test/diff"
	"knative.dev/pkg/apis"
)

func TestLoop_HasLoop(t *testing.T) {
	tests := []struct {
		name string
		loop *Loop
		want bool
	}{{
		name: "nil loop",
		loop: nil,
		want: false,
	}, {
		name: "empty loop (zero MaxIterations)",
		loop: &Loop{},
		want: false,
	}, {
		name: "loop with negative MaxIterations",
		loop: &Loop{MaxIterations: -1},
		want: false,
	}, {
		name: "valid loop with MaxIterations",
		loop: &Loop{MaxIterations: 10},
		want: true,
	}, {
		name: "valid loop with MaxIterations and Until",
		loop: &Loop{MaxIterations: 5, Until: "true"},
		want: true,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.loop.HasLoop()
			if d := cmp.Diff(tt.want, got); d != "" {
				t.Errorf("Loop.HasLoop() %s", diff.PrintWantGot(d))
			}
		})
	}
}

func TestPipelineTask_IsLooped(t *testing.T) {
	tests := []struct {
		name string
		pt   PipelineTask
		want bool
	}{{
		name: "no loop configured",
		pt: PipelineTask{
			Name:    "task",
			TaskRef: &TaskRef{Name: "my-task"},
		},
		want: false,
	}, {
		name: "nil loop",
		pt: PipelineTask{
			Name:    "task",
			TaskRef: &TaskRef{Name: "my-task"},
			Loop:    nil,
		},
		want: false,
	}, {
		name: "loop with zero MaxIterations",
		pt: PipelineTask{
			Name:    "task",
			TaskRef: &TaskRef{Name: "my-task"},
			Loop:    &Loop{MaxIterations: 0},
		},
		want: false,
	}, {
		name: "valid looped task",
		pt: PipelineTask{
			Name:    "task",
			TaskRef: &TaskRef{Name: "my-task"},
			Loop:    &Loop{MaxIterations: 100},
		},
		want: true,
	}, {
		name: "valid looped task with Until expression",
		pt: PipelineTask{
			Name:    "task",
			TaskRef: &TaskRef{Name: "my-task"},
			Loop: &Loop{
				MaxIterations: 50,
				Until:         "$(loop.iteration) > 10",
			},
		},
		want: true,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.pt.IsLooped()
			if d := cmp.Diff(tt.want, got); d != "" {
				t.Errorf("PipelineTask.IsLooped() %s", diff.PrintWantGot(d))
			}
		})
	}
}

func TestPipelineTask_ValidateLoop_Valid(t *testing.T) {
	tests := []struct {
		name string
		pt   PipelineTask
	}{{
		name: "no loop configured (nil)",
		pt: PipelineTask{
			Name:    "task",
			TaskRef: &TaskRef{Name: "my-task"},
		},
	}, {
		name: "valid loop with MaxIterations",
		pt: PipelineTask{
			Name:    "task",
			TaskRef: &TaskRef{Name: "my-task"},
			Loop:    &Loop{MaxIterations: 100},
		},
	}, {
		name: "valid loop with MaxIterations and Until",
		pt: PipelineTask{
			Name:    "task",
			TaskRef: &TaskRef{Name: "my-task"},
			Loop: &Loop{
				MaxIterations: 50,
				Until:         "$(loop.iteration) > 10",
			},
		},
	}, {
		name: "valid loop with IterationParams not overlapping regular Params",
		pt: PipelineTask{
			Name:    "task",
			TaskRef: &TaskRef{Name: "my-task"},
			Params: Params{{
				Name:  "regular-param",
				Value: ParamValue{Type: ParamTypeString, StringVal: "value"},
			}},
			Loop: &Loop{
				MaxIterations: 10,
				IterationParams: Params{{
					Name:  "iter-param",
					Value: ParamValue{Type: ParamTypeString, StringVal: "$(loop.iteration)"},
				}},
			},
		},
	}, {
		name: "valid loop at max allowed iterations (256)",
		pt: PipelineTask{
			Name:    "task",
			TaskRef: &TaskRef{Name: "my-task"},
			Loop:    &Loop{MaxIterations: 256},
		},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := tt.pt.validateLoop(ctx)
			if err != nil {
				t.Errorf("PipelineTask.validateLoop() returned unexpected error: %v", err)
			}
		})
	}
}

func TestPipelineTask_ValidateLoop_InvalidMaxIterations(t *testing.T) {
	tests := []struct {
		name        string
		pt          PipelineTask
		expectedErr *apis.FieldError
	}{{
		name: "maxIterations is zero",
		pt: PipelineTask{
			Name:    "task",
			TaskRef: &TaskRef{Name: "my-task"},
			Loop:    &Loop{MaxIterations: 0},
		},
		expectedErr: apis.ErrInvalidValue(0, "loop.maxIterations",
			"maxIterations must be a positive integer"),
	}, {
		name: "maxIterations is negative",
		pt: PipelineTask{
			Name:    "task",
			TaskRef: &TaskRef{Name: "my-task"},
			Loop:    &Loop{MaxIterations: -5},
		},
		expectedErr: apis.ErrInvalidValue(-5, "loop.maxIterations",
			"maxIterations must be a positive integer"),
	}, {
		name: "maxIterations exceeds default limit (256)",
		pt: PipelineTask{
			Name:    "task",
			TaskRef: &TaskRef{Name: "my-task"},
			Loop:    &Loop{MaxIterations: 500},
		},
		expectedErr: apis.ErrOutOfBoundsValue(500, 1, 256, "loop.maxIterations"),
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := tt.pt.validateLoop(ctx)
			if err == nil {
				t.Errorf("PipelineTask.validateLoop() did not return error for invalid maxIterations")
			}
			if d := cmp.Diff(tt.expectedErr.Error(), err.Error(), cmpopts.IgnoreUnexported(apis.FieldError{})); d != "" {
				t.Errorf("PipelineTask.validateLoop() error diff %s", diff.PrintWantGot(d))
			}
		})
	}
}

func TestPipelineTask_ValidateLoop_MutuallyExclusiveWithMatrix(t *testing.T) {
	pt := PipelineTask{
		Name:    "task",
		TaskRef: &TaskRef{Name: "my-task"},
		Loop:    &Loop{MaxIterations: 10},
		Matrix: &Matrix{
			Params: Params{{
				Name:  "platform",
				Value: ParamValue{Type: ParamTypeArray, ArrayVal: []string{"linux", "mac"}},
			}},
		},
	}

	ctx := context.Background()
	err := pt.validateLoop(ctx)
	if err == nil {
		t.Fatal("PipelineTask.validateLoop() did not return error when loop and matrix are both set")
	}
	expectedErr := apis.ErrMultipleOneOf("loop", "matrix")
	if d := cmp.Diff(expectedErr.Error(), err.Error(), cmpopts.IgnoreUnexported(apis.FieldError{})); d != "" {
		t.Errorf("PipelineTask.validateLoop() error diff %s", diff.PrintWantGot(d))
	}
}

func TestPipelineTask_ValidateLoop_DuplicateParams(t *testing.T) {
	pt := PipelineTask{
		Name:    "task",
		TaskRef: &TaskRef{Name: "my-task"},
		Params: Params{{
			Name:  "shared-param",
			Value: ParamValue{Type: ParamTypeString, StringVal: "value"},
		}, {
			Name:  "other-param",
			Value: ParamValue{Type: ParamTypeString, StringVal: "other"},
		}},
		Loop: &Loop{
			MaxIterations: 10,
			IterationParams: Params{{
				Name:  "shared-param",
				Value: ParamValue{Type: ParamTypeString, StringVal: "$(loop.iteration)"},
			}},
		},
	}

	ctx := context.Background()
	err := pt.validateLoop(ctx)
	if err == nil {
		t.Fatal("PipelineTask.validateLoop() did not return error for duplicate params")
	}
	expectedErr := apis.ErrMultipleOneOf(
		"loop.iterationParams[shared-param]",
		"params[shared-param]",
	)
	if d := cmp.Diff(expectedErr.Error(), err.Error(), cmpopts.IgnoreUnexported(apis.FieldError{})); d != "" {
		t.Errorf("PipelineTask.validateLoop() error diff %s", diff.PrintWantGot(d))
	}
}

func TestPipelineTask_ValidateLoop_MultipleDuplicateParams(t *testing.T) {
	pt := PipelineTask{
		Name:    "task",
		TaskRef: &TaskRef{Name: "my-task"},
		Params: Params{{
			Name:  "param-a",
			Value: ParamValue{Type: ParamTypeString, StringVal: "val-a"},
		}, {
			Name:  "param-b",
			Value: ParamValue{Type: ParamTypeString, StringVal: "val-b"},
		}},
		Loop: &Loop{
			MaxIterations: 10,
			IterationParams: Params{{
				Name:  "param-a",
				Value: ParamValue{Type: ParamTypeString, StringVal: "$(loop.iteration)"},
			}, {
				Name:  "param-b",
				Value: ParamValue{Type: ParamTypeString, StringVal: "$(loop.previousResult.output)"},
			}},
		},
	}

	ctx := context.Background()
	err := pt.validateLoop(ctx)
	if err == nil {
		t.Fatal("PipelineTask.validateLoop() did not return error for multiple duplicate params")
	}
	// Both duplicate params should generate errors
	errStr := err.Error()
	if !containsSubstring(errStr, "loop.iterationParams[param-a]") {
		t.Errorf("expected error to mention param-a, got: %s", errStr)
	}
	if !containsSubstring(errStr, "loop.iterationParams[param-b]") {
		t.Errorf("expected error to mention param-b, got: %s", errStr)
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
