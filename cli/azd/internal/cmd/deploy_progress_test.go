// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeployProgressTracker_NonInteractive(t *testing.T) {
	var buf bytes.Buffer
	tracker := newDeployProgressTracker(&buf, false, []string{"web", "api", "worker"})

	tracker.Update("web", phasePackaging, "building container")
	tracker.Update("api", phasePackaging, "")
	tracker.Update("web", phaseDone, "")

	output := buf.String()
	assert.Contains(t, output, "web: Packaging (building container)")
	assert.Contains(t, output, "api: Packaging")
	assert.Contains(t, output, "web: Done")
}

func TestDeployProgressTracker_Interactive_Render(t *testing.T) {
	var buf bytes.Buffer
	tracker := newDeployProgressTracker(&buf, true, []string{"web", "api"})

	tracker.Update("web", phasePackaging, "zip")
	tracker.Render()

	output := buf.String()
	assert.Contains(t, output, "Service")
	assert.Contains(t, output, "Phase")
	assert.Contains(t, output, "web")
	assert.Contains(t, output, "Packaging")
	assert.Contains(t, output, "api")
	assert.Contains(t, output, "Waiting")
}

func TestDeployProgressTracker_Interactive_RenderOverwrite(t *testing.T) {
	var buf bytes.Buffer
	tracker := newDeployProgressTracker(&buf, true, []string{"svc1"})

	tracker.Render()
	first := buf.Len()
	require.Greater(t, first, 0)

	// Second render should include cursor-up escape
	tracker.Update("svc1", phaseDeploying, "uploading")
	tracker.Render()
	output := buf.String()[first:]
	assert.Contains(t, output, "\033[") // ANSI escape present
}

func TestDeployProgressTracker_RenderFinal(t *testing.T) {
	var buf bytes.Buffer
	tracker := newDeployProgressTracker(&buf, true, []string{"web", "api"})

	tracker.Update("web", phaseDone, "")
	tracker.Update("api", phaseFailed, "")
	tracker.RenderFinal()

	output := buf.String()
	assert.Contains(t, output, "Service")
	assert.Contains(t, output, "Status")
	assert.Contains(t, output, "Duration")
	assert.Contains(t, output, "●") // done icon
	assert.Contains(t, output, "✗") // failed icon
}

func TestDeployProgressTracker_Elapsed(t *testing.T) {
	svc := &serviceStatus{
		name:      "test",
		phase:     phasePackaging,
		startedAt: time.Now().Add(-5 * time.Second),
	}
	elapsed := svc.elapsed()
	assert.GreaterOrEqual(t, elapsed.Seconds(), 4.0)
	assert.LessOrEqual(t, elapsed.Seconds(), 7.0)
}

func TestDeployProgressTracker_ElapsedCompleted(t *testing.T) {
	start := time.Now().Add(-10 * time.Second)
	end := start.Add(5 * time.Second)
	svc := &serviceStatus{
		name:      "test",
		phase:     phaseDone,
		startedAt: start,
		endedAt:   end,
	}
	assert.Equal(t, 5*time.Second, svc.elapsed())
}

func TestDeployProgressTracker_UnknownService(t *testing.T) {
	var buf bytes.Buffer
	tracker := newDeployProgressTracker(&buf, false, []string{"web"})

	// Should not panic
	tracker.Update("nonexistent", phasePackaging, "")
	assert.Empty(t, buf.String())
}

func TestDeployProgressTracker_StartTicker(t *testing.T) {
	var buf bytes.Buffer
	tracker := newDeployProgressTracker(&buf, true, []string{"web"})

	cancel := tracker.StartTicker(t.Context())

	// Let the ticker fire at least once
	time.Sleep(1500 * time.Millisecond)
	cancel()

	output := buf.String()
	assert.Contains(t, output, "web")
	assert.Contains(t, output, "Service")
}

func TestDeployProgressTracker_StartTicker_NonInteractive(t *testing.T) {
	var buf bytes.Buffer
	tracker := newDeployProgressTracker(&buf, false, []string{"web"})

	cancel := tracker.StartTicker(t.Context())
	defer cancel()

	// Non-interactive ticker is a no-op
	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, buf.String()) // no automatic rendering
}

func TestPhaseIcons(t *testing.T) {
	tests := []struct {
		phase deployPhase
		icon  string
	}{
		{phaseWaiting, "○"},
		{phasePackaging, "◐"},
		{phasePublish, "◐"},
		{phaseDeploying, "◐"},
		{phaseDone, "●"},
		{phaseFailed, "✗"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.icon, phaseIcon(tt.phase), "icon for %s", tt.phase)
	}
}

func TestDeployProgressTracker_DetailTruncation(t *testing.T) {
	var buf bytes.Buffer
	tracker := newDeployProgressTracker(&buf, true, []string{"web"})

	longDetail := strings.Repeat("x", 50)
	tracker.Update("web", phasePackaging, longDetail)
	tracker.Render()

	output := buf.String()
	assert.Contains(t, output, "...")
	// Should not contain the full 50-char string
	assert.NotContains(t, output, longDetail)
}

func TestDeployProgressTracker_ServiceNameWithANSI(t *testing.T) {
	// Security: service names come from azure.yaml and could contain
	// ANSI escape codes that hijack terminal output. The progress
	// tracker should not crash or produce malformed output with such
	// names. This is a defense-in-depth test—injection requires a
	// malicious azure.yaml the user already controls.
	ansiName := "\033[31mevil\033[0m"
	var buf bytes.Buffer
	tracker := newDeployProgressTracker(&buf, false, []string{ansiName})

	// Should not panic
	tracker.Update(ansiName, phasePackaging, "building")
	output := buf.String()
	assert.Contains(t, output, "Packaging")
}

func TestDeployProgressTracker_EmptyServiceList(t *testing.T) {
	// Edge case: no services should not panic.
	var buf bytes.Buffer
	tracker := newDeployProgressTracker(&buf, true, []string{})

	tracker.Render()
	tracker.RenderFinal()
	// No panic = pass
}
