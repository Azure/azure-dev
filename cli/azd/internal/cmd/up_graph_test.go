// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/exegraph"
)

func TestPhaseTimingBreakdown(t *testing.T) {
	t.Parallel()
	base := time.Now()

	tests := []struct {
		name  string
		steps []exegraph.StepTiming
		want  string
	}{
		{
			name:  "empty steps",
			steps: nil,
			want:  "",
		},
		{
			name: "provision only",
			steps: []exegraph.StepTiming{
				{Name: "provision-infra", Status: exegraph.StepDone, Start: base, End: base.Add(5 * time.Minute)},
			},
			want: "  Provisioning: 5 minutes",
		},
		{
			name: "deploy only",
			steps: []exegraph.StepTiming{
				{
					Name: "package-web", Status: exegraph.StepDone,
					Start: base, End: base.Add(30 * time.Second),
				},
				{
					Name: "deploy-web", Status: exegraph.StepDone,
					Start: base.Add(30 * time.Second), End: base.Add(90 * time.Second),
				},
			},
			want: "  Deploying:    1 minute",
		},
		{
			name: "both phases",
			steps: []exegraph.StepTiming{
				{
					Name: "provision-infra", Status: exegraph.StepDone,
					Start: base, End: base.Add(9 * time.Minute),
				},
				{
					Name: "package-web", Status: exegraph.StepDone,
					Start: base.Add(9 * time.Minute), End: base.Add(10 * time.Minute),
				},
				{
					Name: "deploy-web", Status: exegraph.StepDone,
					Start: base.Add(10 * time.Minute), End: base.Add(11 * time.Minute),
				},
			},
			want: "  Provisioning: 9 minutes\n  Deploying:    1 minute",
		},
		{
			name: "skipped steps excluded",
			steps: []exegraph.StepTiming{
				{Name: "provision-infra", Status: exegraph.StepSkipped, Start: time.Time{}, End: time.Time{}},
				{Name: "deploy-web", Status: exegraph.StepDone, Start: base, End: base.Add(45 * time.Second)},
			},
			want: "  Deploying:    45 seconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := phaseTimingBreakdown(tt.steps)
			if got != tt.want {
				t.Errorf("phaseTimingBreakdown() =\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}
