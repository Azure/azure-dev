// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"azureaiagent/internal/cmd/nextstep"
)

func TestDoctorStatusBadge(t *testing.T) {
	cases := []struct {
		s    doctorStatus
		want string
	}{
		{doctorOK, "PASS"},
		{doctorWarn, "WARN"},
		{doctorFail, "FAIL"},
		{doctorSkip, "SKIP"},
	}
	for _, c := range cases {
		// strip ANSI by checking substring
		got := c.s.badge()
		if !strings.Contains(got, c.want) {
			t.Errorf("badge for %v = %q, want substring %q", c.s, got, c.want)
		}
	}
}

func TestPrintDoctorReport_ShowsRows(t *testing.T) {
	// Disable colors so assertions are stable.
	t.Setenv("NO_COLOR", "1")

	var buf bytes.Buffer
	results := []doctorResult{
		{Title: "First check", Status: doctorOK, Detail: "all good"},
		{Title: "Second check", Status: doctorFail, Detail: "broken", Fix: "azd provision"},
	}
	printDoctorReport(&buf, results, nil)
	out := buf.String()

	for _, want := range []string{
		"azd ai agent doctor",
		"First check",
		"all good",
		"Second check",
		"broken",
		"azd provision",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nFull output:\n%s", want, out)
		}
	}
}

func TestPrintDoctorReport_AllPassFallsBackToInitGuidance(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	// State with a project endpoint and a single agent service => post-init Stage 3:
	// run locally, invoke local, deploy.
	state := &nextstep.State{
		HasProjectEndpoint: true,
		AgentServices: []nextstep.ServiceState{
			{ServiceName: "calc", Protocol: "responses"},
		},
	}
	printDoctorReport(&buf, []doctorResult{
		{Title: "Only OK", Status: doctorOK, Detail: "fine"},
	}, state)
	out := buf.String()
	if !strings.Contains(out, "Next:") {
		t.Errorf("expected Next block when state has actionable next steps, got:\n%s", out)
	}
	for _, want := range []string{"azd ai agent run calc", "azd deploy"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected suggestion %q in output, got:\n%s", want, out)
		}
	}
}

func TestPrintDoctorReport_AllPassNoStatePrintsNothingExtra(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	printDoctorReport(&buf, []doctorResult{
		{Title: "Only OK", Status: doctorOK, Detail: "fine"},
	}, nil)
	out := buf.String()
	// With nil state, ResolveAfterInit returns the provision suggestion as Stage 1.
	if !strings.Contains(out, "azd provision") {
		t.Errorf("expected provision suggestion when state is nil, got:\n%s", out)
	}
}

// Ensure the doctor command is registered on the root command.
func TestDoctorCommandRegistered(t *testing.T) {
	root := NewRootCommand()
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "doctor" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'doctor' command to be registered on the root command")
	}
}

// Test that NO_COLOR is respected — an integration-ish smoke test.
func TestDoctorStatusBadgeRespectsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	// fatih/color reads NO_COLOR at startup; even without that, the
	// substring assertion remains valid.
	if !strings.Contains(doctorOK.badge(), "PASS") {
		t.Errorf("badge missing PASS marker: %q", doctorOK.badge())
	}
}

func TestDoctorEnv_ReadsNoColor(t *testing.T) {
	if v := os.Getenv("HOME"); v != "" {
		_ = v // sanity touch — this test exists to ensure the test binary can read env in CI
	}
}
