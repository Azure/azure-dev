// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/exegraph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression coverage for issue #9054: the synthetic cmd.package / cmd.provision
// / cmd.deploy spans under `azd up` must reflect the real per-phase outcome
// rather than always reporting success. aggregatePhaseError and
// deployPhaseSpanTiming are the pure building blocks the deferred span finalizer
// relies on, so they are asserted directly here.
func TestAggregatePhaseError(t *testing.T) {
	t.Parallel()

	provisionErr := errors.New("bicep deployment failed")
	packageErr := errors.New("docker build failed")
	deployErr := errors.New("zip deploy failed")
	publishErr := errors.New("image push failed")
	skipErr := &exegraph.StepSkippedError{StepName: "deploy-web"}

	tests := []struct {
		name   string
		result *exegraph.RunResult
		tags   []string
		want   error
	}{
		{
			name:   "nil result",
			result: nil,
			tags:   []string{"provision"},
			want:   nil,
		},
		{
			name: "provision failure is surfaced",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "provision", Status: exegraph.StepFailed, Tags: []string{"provision"}, Err: provisionErr},
			}},
			tags: []string{"provision"},
			want: provisionErr,
		},
		{
			name: "success returns nil",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "provision", Status: exegraph.StepDone, Tags: []string{"provision"}},
			}},
			tags: []string{"provision"},
			want: nil,
		},
		{
			name: "skipped provision step is not blamed",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "provision", Status: exegraph.StepSkipped, Tags: []string{"provision"}, Err: skipErr},
			}},
			tags: []string{"provision"},
			want: nil,
		},
		{
			name: "unrelated phase failure is ignored for provision",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "deploy-web", Status: exegraph.StepFailed, Tags: []string{"deploy"}, Err: deployErr},
			}},
			tags: []string{"provision"},
			want: nil,
		},
		{
			name: "deploy aggregates both deploy and publish tags",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "publish-web", Status: exegraph.StepFailed, Tags: []string{"publish"}, Err: publishErr},
			}},
			tags: []string{"deploy", "publish"},
			want: publishErr,
		},
		{
			name: "first failure in completion order wins",
			result: &exegraph.RunResult{Steps: []exegraph.StepTiming{
				{Name: "package-api", Status: exegraph.StepDone, Tags: []string{"package"}},
				{Name: "package-web", Status: exegraph.StepFailed, Tags: []string{"package"}, Err: packageErr},
				{Name: "package-worker", Status: exegraph.StepFailed, Tags: []string{"package"}, Err: deployErr},
			}},
			tags: []string{"package"},
			want: packageErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := aggregatePhaseError(tt.result, tt.tags...)
			require.ErrorIs(t, got, tt.want)
		})
	}
}

func TestDeployPhaseSpanTiming(t *testing.T) {
	t.Parallel()

	base := time.Now()

	tests := []struct {
		name      string
		steps     []exegraph.StepTiming
		wantRan   bool
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:    "no deploy or publish steps",
			steps:   []exegraph.StepTiming{{Name: "provision", Status: exegraph.StepDone, Tags: []string{"provision"}}},
			wantRan: false,
		},
		{
			name: "all deploy steps skipped means phase did not run",
			steps: []exegraph.StepTiming{
				{Name: "publish-web", Status: exegraph.StepSkipped, Tags: []string{"publish"}},
				{Name: "deploy-web", Status: exegraph.StepSkipped, Tags: []string{"deploy"}},
			},
			wantRan: false,
		},
		{
			name: "single deploy step envelope",
			steps: []exegraph.StepTiming{
				{
					Name: "deploy-web", Status: exegraph.StepDone, Tags: []string{"deploy"},
					Start: base.Add(30 * time.Second), End: base.Add(90 * time.Second),
				},
			},
			wantRan:   true,
			wantStart: base.Add(30 * time.Second),
			wantEnd:   base.Add(90 * time.Second),
		},
		{
			name: "publish and deploy span the full envelope; skipped excluded",
			steps: []exegraph.StepTiming{
				{
					Name: "publish-web", Status: exegraph.StepDone, Tags: []string{"publish"},
					Start: base.Add(10 * time.Second), End: base.Add(40 * time.Second),
				},
				{
					Name: "deploy-web", Status: exegraph.StepFailed, Tags: []string{"deploy"},
					Start: base.Add(40 * time.Second), End: base.Add(75 * time.Second),
				},
				{
					Name: "deploy-api", Status: exegraph.StepSkipped, Tags: []string{"deploy"},
					Start: base, End: base.Add(500 * time.Second),
				},
			},
			wantRan:   true,
			wantStart: base.Add(10 * time.Second),
			wantEnd:   base.Add(75 * time.Second),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			start, end, ran := deployPhaseSpanTiming(tt.steps)
			assert.Equal(t, tt.wantRan, ran)
			if tt.wantRan {
				assert.True(t, tt.wantStart.Equal(start), "start = %s, want %s", start, tt.wantStart)
				assert.True(t, tt.wantEnd.Equal(end), "end = %s, want %s", end, tt.wantEnd)
			}
		})
	}
}
