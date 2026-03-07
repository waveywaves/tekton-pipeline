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
	"fmt"
	"testing"

	"github.com/tektoncd/pipeline/pkg/reconciler/pipelinerun/resources"
)

// TestHandleLoopedTask_TaskRunNameFormat verifies that the TaskRun names
// produced by GetLoopTaskRunName follow the expected convention used by
// handleLoopedTask when creating iteration TaskRuns.
func TestHandleLoopedTask_TaskRunNameFormat(t *testing.T) {
	tests := []struct {
		name             string
		pipelineRunName  string
		pipelineTaskName string
		iteration        int
		want             string
	}{{
		name:             "first iteration",
		pipelineRunName:  "my-pipeline-run",
		pipelineTaskName: "train",
		iteration:        0,
		want:             "my-pipeline-run-train-loop-0",
	}, {
		name:             "subsequent iteration",
		pipelineRunName:  "my-pipeline-run",
		pipelineTaskName: "train",
		iteration:        5,
		want:             "my-pipeline-run-train-loop-5",
	}, {
		name:             "name format includes loop suffix",
		pipelineRunName:  "pr",
		pipelineTaskName: "task",
		iteration:        42,
		want:             "pr-task-loop-42",
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resources.GetLoopTaskRunName(tt.pipelineRunName, tt.pipelineTaskName, tt.iteration)
			if got != tt.want {
				t.Errorf("GetLoopTaskRunName() = %q, want %q", got, tt.want)
			}
			// Verify the name contains the expected "-loop-<N>" suffix
			expectedSuffix := fmt.Sprintf("-loop-%d", tt.iteration)
			if len(got) < len(expectedSuffix) || got[len(got)-len(expectedSuffix):] != expectedSuffix {
				t.Errorf("GetLoopTaskRunName() = %q does not end with expected suffix %q", got, expectedSuffix)
			}
		})
	}
}
