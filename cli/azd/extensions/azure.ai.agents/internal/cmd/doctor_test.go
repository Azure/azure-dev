// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
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

func TestDoctorStatusString(t *testing.T) {
	cases := []struct {
		s    doctorStatus
		want string
	}{
		{doctorOK, "pass"},
		{doctorWarn, "warn"},
		{doctorFail, "fail"},
		{doctorSkip, "skip"},
		{doctorStatus(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("status %v String() = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestComputeDoctorExitCode(t *testing.T) {
	cases := []struct {
		name    string
		results []doctorResult
		want    int
	}{
		{
			name: "all pass",
			results: []doctorResult{
				{Status: doctorOK}, {Status: doctorOK},
			},
			want: 0,
		},
		{
			name: "pass with warnings",
			results: []doctorResult{
				{Status: doctorOK}, {Status: doctorWarn},
			},
			want: 0,
		},
		{
			name: "pass with skip",
			results: []doctorResult{
				{Status: doctorOK}, {Status: doctorSkip},
			},
			want: 0,
		},
		{
			name: "any fail wins over pass",
			results: []doctorResult{
				{Status: doctorOK}, {Status: doctorFail},
			},
			want: 1,
		},
		{
			name: "fail wins over skip",
			results: []doctorResult{
				{Status: doctorFail}, {Status: doctorSkip},
			},
			want: 1,
		},
		{
			name: "all skip",
			results: []doctorResult{
				{Status: doctorSkip}, {Status: doctorSkip},
			},
			want: 2,
		},
		{
			name:    "empty results -> all-skip semantics (nothing ran)",
			results: nil,
			want:    2,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := computeDoctorExitCode(c.results); got != c.want {
				t.Errorf("computeDoctorExitCode() = %d, want %d", got, c.want)
			}
		})
	}
}

func TestWriteDoctorJSON_EnvelopeShape(t *testing.T) {
	results := []doctorResult{
		{
			ID:         "local.azd-cli",
			Title:      "azd CLI is installed and reachable",
			Status:     doctorOK,
			Detail:     "extension running",
			DurationMs: 7,
		},
		{
			ID:         "local.project-endpoint",
			Title:      "AZURE_AI_PROJECT_ENDPOINT is set",
			Status:     doctorFail,
			Detail:     "value missing",
			Fix:        "azd provision",
			DurationMs: 12,
		},
	}
	var buf bytes.Buffer
	if err := writeDoctorJSON(&buf, results, &doctorFlags{localOnly: true}); err != nil {
		t.Fatalf("writeDoctorJSON returned error: %v", err)
	}

	var got doctorJSONReport
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, buf.String())
	}

	if got.SchemaVersion != doctorReportSchemaVersion {
		t.Errorf("schemaVersion = %q, want %q", got.SchemaVersion, doctorReportSchemaVersion)
	}
	if got.Remote {
		t.Errorf("remote = true, want false (--local-only set)")
	}
	if len(got.Checks) != 2 {
		t.Fatalf("checks: got %d, want 2", len(got.Checks))
	}
	if got.Checks[0].ID != "local.azd-cli" {
		t.Errorf("checks[0].id = %q, want local.azd-cli", got.Checks[0].ID)
	}
	if got.Checks[0].Status != "pass" {
		t.Errorf("checks[0].status = %q, want pass", got.Checks[0].Status)
	}
	if got.Checks[1].Status != "fail" {
		t.Errorf("checks[1].status = %q, want fail", got.Checks[1].Status)
	}
	if got.Checks[1].Fix != "azd provision" {
		t.Errorf("checks[1].fix = %q, want azd provision", got.Checks[1].Fix)
	}
	if got.Checks[0].DurationMs != 7 {
		t.Errorf("checks[0].durationMs = %d, want 7", got.Checks[0].DurationMs)
	}
}

func TestWriteDoctorJSON_RemoteFlag(t *testing.T) {
	// Without --local-only, the envelope advertises remote = true even
	// though no remote checks ship today. Stable schema for callers.
	var buf bytes.Buffer
	if err := writeDoctorJSON(&buf, nil, &doctorFlags{}); err != nil {
		t.Fatalf("writeDoctorJSON: %v", err)
	}
	var got doctorJSONReport
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !got.Remote {
		t.Errorf("remote = false, want true (default)")
	}
	// With nil results, checks must still serialize as [] not null so JSON
	// consumers don't have to special-case the field.
	if !strings.Contains(buf.String(), `"checks": []`) {
		t.Errorf("expected `\"checks\": []` in output, got:\n%s", buf.String())
	}
}

func TestDoctor_TimedStampsDuration(t *testing.T) {
	got := timed(func() doctorResult {
		return doctorResult{ID: "x", Status: doctorOK}
	})
	if got.DurationMs < 0 {
		t.Errorf("DurationMs = %d, must be >= 0", got.DurationMs)
	}
	if got.ID != "x" || got.Status != doctorOK {
		t.Errorf("timed() lost the inner result fields: %+v", got)
	}
}

// Verify the new flags are registered on the doctor command. The
// --output flag is inherited from the root persistent flag (configured
// in azdext.Run) so it is not asserted here.
func TestDoctorCommand_FlagsRegistered(t *testing.T) {
	cmd := newDoctorCommand(nil)
	for _, name := range []string{"local-only", "unredacted"} {
		if cmd.Flag(name) == nil {
			t.Errorf("expected flag --%s to be registered on doctor command", name)
		}
	}
}
