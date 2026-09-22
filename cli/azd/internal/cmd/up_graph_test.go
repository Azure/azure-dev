// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

type previewerState struct {
	paused      bool
	pauseCount  int
	resumeCount int
}

func (p *previewerState) PausePreviewer() {
	p.paused = true
	p.pauseCount++
}

func (p *previewerState) ResumePreviewer() {
	p.paused = false
	p.resumeCount++
}

type previewerStateWriter struct {
	bytes.Buffer
	previewer   *previewerState
	writeStates []bool
}

func (w *previewerStateWriter) Write(p []byte) (int, error) {
	w.writeStates = append(w.writeStates, w.previewer.paused)
	return w.Buffer.Write(p)
}

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

func TestFinalizeUpDeployProgress_RestoresPreviewerBeforeFinalRender(t *testing.T) {
	previewer := &previewerState{}
	writer := &previewerStateWriter{previewer: previewer}
	tracker := newDeployProgressTracker(writer, true, []string{"web"})
	tracker.Update("web", phaseDone, "")
	previewer.PausePreviewer()
	tracker.Render()
	finalized := false
	stopTicker := func() {
		previewer.ResumePreviewer()
	}

	finalizeUpDeployProgress(tracker, stopTicker, &finalized)

	require.False(t, previewer.paused)
	require.Equal(t, 1, previewer.pauseCount)
	require.Equal(t, 1, previewer.resumeCount)
	require.NotEmpty(t, writer.writeStates)
	require.True(t, writer.writeStates[0], "the live progress table must render while previewer output is paused")
	require.False(t, writer.writeStates[len(writer.writeStates)-1],
		"the final table must render after previewer output is restored")
	require.Contains(t, writer.String(), "Status")

	outputAfterFirstFinalize := writer.String()
	finalizeUpDeployProgress(tracker, stopTicker, &finalized)
	require.Equal(t, outputAfterFirstFinalize, writer.String())
	require.Equal(t, 1, previewer.resumeCount)
}

func TestDeployHookStartMessage(t *testing.T) {
	hooks := map[string][]*ext.HookConfig{
		"predeploy": {
			{Run: "echo first"},
			{Run: "echo second"},
		},
	}

	require.Equal(t, "Running predeploy hooks...", deployHookStartMessage(hooks, preDeployHookStep))
	require.Empty(t, deployHookStartMessage(hooks, postDeployHookStep))
	require.Empty(t, deployHookStartMessage(hooks, "deploy-web"))

	hooks["postdeploy"] = []*ext.HookConfig{{Run: "echo postdeploy"}}
	require.Equal(t, "Running postdeploy hook...", deployHookStartMessage(hooks, postDeployHookStep))
}

func TestUpDeployStepStartHandler_HookAndProgressOrdering(t *testing.T) {
	var events []string
	hooks := map[string][]*ext.HookConfig{
		"predeploy":  {{Run: "echo predeploy"}},
		"postdeploy": {{Run: "echo postdeploy"}},
	}
	handler := newUpDeployStepStartHandler(
		func(stepName string) { events = append(events, "base:"+stepName) },
		func() { events = append(events, "finalize") },
		func(stepName string) {
			if message := deployHookStartMessage(hooks, stepName); message != "" {
				events = append(events, "message:"+message)
			}
		},
		func() { events = append(events, "start-progress") },
		func(serviceName string, phase deployPhase, _ string) {
			events = append(events, "update:"+serviceName+":"+string(phase))
		},
	)

	handler(preDeployHookStep)
	require.Equal(t, []string{
		"base:" + preDeployHookStep,
		"message:Running predeploy hook...",
	}, events)

	events = nil
	handler("publish-web")
	require.Equal(t, []string{
		"base:publish-web",
		"start-progress",
		"update:web:Publishing",
	}, events)

	events = nil
	handler(postDeployHookStep)
	require.Equal(t, []string{
		"base:" + postDeployHookStep,
		"finalize",
		"message:Running postdeploy hook...",
	}, events)
}

func TestFinalizeUpDeployProgress_NoTracker(t *testing.T) {
	finalized := false

	finalizeUpDeployProgress(nil, nil, &finalized)

	require.True(t, finalized)
}

func TestUpGraphRunOptionsConcurrency(t *testing.T) {
	tests := []struct {
		name        string
		upValue     *string
		deployValue *string
		expected    int
	}{
		{name: "unset", expected: 0},
		{name: "deploy fallback", deployValue: new("4"), expected: 4},
		{name: "up takes precedence", upValue: new("2"), deployValue: new("4"), expected: 2},
		{name: "invalid up does not use deploy fallback", upValue: new("invalid"), deployValue: new("4"), expected: 0},
		{name: "up clamped", upValue: new("100"), expected: 64},
		{name: "single worker", upValue: new("1"), expected: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ostest.Unsetenvs(t, []string{
				concurrencyMaxEnvVar, packageConcurrencyEnvVar, provisionConcurrencyEnvVar,
				upConcurrencyEnvVar, deployConcurrencyEnvVar,
			})
			if tt.upValue != nil {
				t.Setenv("AZD_UP_CONCURRENCY", *tt.upValue)
			}
			if tt.deployValue != nil {
				t.Setenv("AZD_DEPLOY_CONCURRENCY", *tt.deployValue)
			}

			action := &UpGraphAction{env: environment.New("test")}
			require.Equal(t, tt.expected, action.runOptions().MaxConcurrency)
		})
	}
}
