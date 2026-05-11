// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"azureaiagent/internal/cmd/doctor"
	"azureaiagent/internal/cmd/nextstep"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPrintDoctorReportJSON_Envelope locks the structured envelope
// shape against the design spec. Consumers of the JSON output (CI
// scripts, dashboards) depend on this contract.
func TestPrintDoctorReportJSON_Envelope(t *testing.T) {
	report := doctor.Report{
		SchemaVersion: doctor.CurrentSchemaVersion,
		Remote:        false,
		Redacted:      true,
		Checks: []doctor.Result{
			{
				ID:         "local.azure-yaml",
				Name:       "azure.yaml valid",
				Status:     doctor.StatusPass,
				Message:    "1 service: echo-agent",
				DurationMs: 4,
			},
			{
				ID:         "local.project-endpoint-set",
				Name:       "AZURE_AI_PROJECT_ENDPOINT set",
				Status:     doctor.StatusFail,
				Message:    "AZURE_AI_PROJECT_ENDPOINT is not set",
				Suggestion: "azd env set AZURE_AI_PROJECT_ENDPOINT <value>",
				Links:      []string{"https://aka.ms/azd-ai-agent-init"},
			},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, printDoctorReportJSON(&buf, report))

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))

	assert.Equal(t, "1.0", decoded["schemaVersion"])
	assert.Equal(t, false, decoded["remote"])
	assert.Equal(t, true, decoded["redacted"])

	checks, ok := decoded["checks"].([]any)
	require.True(t, ok, "checks must be a JSON array")
	require.Len(t, checks, 2)

	first := checks[0].(map[string]any)
	assert.Equal(t, "local.azure-yaml", first["id"])
	assert.Equal(t, "pass", first["status"])
	assert.Equal(t, "azure.yaml valid", first["name"])
	assert.Equal(t, "1 service: echo-agent", first["message"])

	second := checks[1].(map[string]any)
	assert.Equal(t, "fail", second["status"])
	assert.Equal(t, "azd env set AZURE_AI_PROJECT_ENDPOINT <value>", second["suggestion"])
	links, ok := second["links"].([]any)
	require.True(t, ok)
	require.Len(t, links, 1)
	assert.Equal(t, "https://aka.ms/azd-ai-agent-init", links[0])
}

// TestPrintDoctorReportJSON_NoNextStep ensures the JSON envelope never
// carries a human Next: block — that is the output-discipline contract
// from the design spec ("Exit codes & JSON output").
func TestPrintDoctorReportJSON_NoNextStep(t *testing.T) {
	report := doctor.Report{
		SchemaVersion: doctor.CurrentSchemaVersion,
		Checks: []doctor.Result{
			{ID: "local.azure-yaml", Name: "azure.yaml valid", Status: doctor.StatusPass},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, printDoctorReportJSON(&buf, report))

	got := buf.String()
	assert.NotContains(t, got, "Next:")
	assert.NotContains(t, got, "nextStep")
	assert.NotContains(t, got, "next_step")
}

func TestPrintDoctorReportText_PassFailSkip(t *testing.T) {
	report := doctor.Report{
		Checks: []doctor.Result{
			{ID: "local.grpc", Name: "azd extension", Status: doctor.StatusPass, Message: "running"},
			{ID: "local.azure-yaml", Name: "azure.yaml valid", Status: doctor.StatusFail,
				Message:    "no azure.yaml in current directory",
				Suggestion: "azd ai agent init",
				Links:      []string{"https://aka.ms/azd-ai-agent-init"},
			},
			{ID: "local.env-selected", Name: "azd environment selected", Status: doctor.StatusSkip,
				Message: "skipped: upstream check blocked"},
		},
		Summary: doctor.Summary{Pass: 1, Fail: 1, Skip: 1},
	}

	var buf bytes.Buffer
	require.NoError(t, printDoctorReportText(&buf, report, nil, false))

	got := buf.String()
	assert.True(t, strings.HasPrefix(got, "azd ai agent doctor\n"), "header line")
	assert.Contains(t, got, "✓ PASS  azd extension")
	assert.Contains(t, got, "✗ FAIL  azure.yaml valid")
	assert.Contains(t, got, "- SKIP  azd environment selected")
	assert.Contains(t, got, "          running")
	assert.Contains(t, got, "          fix:  azd ai agent init")
	assert.Contains(t, got, "          https://aka.ms/azd-ai-agent-init")
	assert.Contains(t, got, "Summary: 1 passed, 1 failed, 1 skipped, 0 warned")
}

func TestPrintDoctorReportText_AllSkippedReport(t *testing.T) {
	report := doctor.Report{
		Checks: []doctor.Result{
			{ID: "local.grpc", Name: "azd extension", Status: doctor.StatusSkip,
				Message: "azd extension not reachable"},
		},
		Summary: doctor.Summary{Skip: 1},
	}

	var buf bytes.Buffer
	require.NoError(t, printDoctorReportText(&buf, report, nil, false))

	got := buf.String()
	assert.Contains(t, got, "- SKIP  azd extension")
	assert.Contains(t, got, "Summary: 0 passed, 0 failed, 1 skipped, 0 warned")
	// No trailing Next: block when checks did not all pass
	assert.NotContains(t, got, "Next:")
}

func TestPrintDoctorReportText_EmptyReport(t *testing.T) {
	// Defensive: caller synthesizes a Report with no checks. The
	// formatter should not crash and should produce a clear message.
	var buf bytes.Buffer
	require.NoError(t, printDoctorReportText(&buf, doctor.Report{}, nil, false))

	got := buf.String()
	assert.Contains(t, got, "azd ai agent doctor")
	assert.Contains(t, got, "Summary: no checks executed")
}

func TestPrintDoctorReportText_TrailingNextWhenAllowed(t *testing.T) {
	// All-pass report with a trailing Next: block; showNext=true
	// (caller has TTY-checked already). We assert the block follows
	// the summary and uses the canonical "Next:" prefix.
	report := doctor.Report{
		Checks: []doctor.Result{
			{ID: "local.grpc", Name: "azd extension", Status: doctor.StatusPass},
		},
		Summary: doctor.Summary{Pass: 1},
	}
	trailing := []nextstep.Suggestion{
		{Command: "azd ai agent run", Description: "start the agent locally", Priority: 10},
	}

	var buf bytes.Buffer
	require.NoError(t, printDoctorReportText(&buf, report, trailing, true))

	got := buf.String()
	assert.Contains(t, got, "Next:")
	assert.Contains(t, got, "azd ai agent run")
	// Order: summary line before Next: header.
	sumIdx := strings.Index(got, "Summary:")
	nextIdx := strings.Index(got, "Next:")
	require.GreaterOrEqual(t, sumIdx, 0)
	require.GreaterOrEqual(t, nextIdx, 0)
	assert.Less(t, sumIdx, nextIdx)
}

func TestPrintDoctorReportText_TrailingSuppressedWhenShowNextFalse(t *testing.T) {
	report := doctor.Report{
		Checks:  []doctor.Result{{ID: "local.grpc", Name: "azd extension", Status: doctor.StatusPass}},
		Summary: doctor.Summary{Pass: 1},
	}
	trailing := []nextstep.Suggestion{
		{Command: "azd ai agent run", Description: "start the agent locally", Priority: 10},
	}

	var buf bytes.Buffer
	require.NoError(t, printDoctorReportText(&buf, report, trailing, false))

	got := buf.String()
	assert.NotContains(t, got, "Next:")
	assert.NotContains(t, got, "azd ai agent run")
}

func TestRenderDoctorReport_RoutesByOutputFlag(t *testing.T) {
	report := doctor.Report{
		SchemaVersion: doctor.CurrentSchemaVersion,
		Checks:        []doctor.Result{{ID: "local.grpc", Name: "azd extension", Status: doctor.StatusPass}},
		Summary:       doctor.Summary{Pass: 1},
	}

	t.Run("json output emits envelope", func(t *testing.T) {
		var buf bytes.Buffer
		require.NoError(t, renderDoctorReport(&buf, "json", report, nil))
		assert.Contains(t, buf.String(), `"schemaVersion": "1.0"`)
	})

	t.Run("text output emits header line", func(t *testing.T) {
		var buf bytes.Buffer
		require.NoError(t, renderDoctorReport(&buf, "text", report, nil))
		assert.Contains(t, buf.String(), "azd ai agent doctor")
	})

	t.Run("non-stdout writer suppresses trailing Next:", func(t *testing.T) {
		// writerIsTerminal returns false for any writer that isn't
		// os.Stdout, so the renderer with non-stdout w should never
		// emit Next: even when trailing is non-empty.
		var buf bytes.Buffer
		trailing := []nextstep.Suggestion{
			{Command: "azd ai agent run", Description: "start the agent locally", Priority: 10},
		}
		require.NoError(t, renderDoctorReport(&buf, "text", report, trailing))
		assert.NotContains(t, buf.String(), "Next:")
	})
}

func TestStatusGlyphAndLabel(t *testing.T) {
	tests := []struct {
		status   doctor.Status
		glyph    string
		label    string
		dataName string
	}{
		{doctor.StatusPass, "✓", "PASS", "pass"},
		{doctor.StatusWarn, "!", "WARN", "warn"},
		{doctor.StatusFail, "✗", "FAIL", "fail"},
		{doctor.StatusSkip, "-", "SKIP", "skip"},
		{doctor.Status("bogus"), "?", "UNKN", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.dataName, func(t *testing.T) {
			g, l := statusGlyphAndLabel(tt.status)
			assert.Equal(t, tt.glyph, g)
			assert.Equal(t, tt.label, l)
		})
	}
}

func TestValidateDoctorFlags(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantErr bool
	}{
		{"text is valid", "text", false},
		{"json is valid", "json", false},
		{"yaml is rejected", "yaml", true},
		{"empty is rejected", "", true},
		{"uppercase JSON is rejected (closed enum)", "JSON", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDoctorFlags(&doctorFlags{output: tt.output})
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAnyServiceDeployed(t *testing.T) {
	assert.False(t, anyServiceDeployed(nil))
	assert.False(t, anyServiceDeployed([]nextstep.ServiceState{}))
	assert.False(t, anyServiceDeployed([]nextstep.ServiceState{
		{Name: "a", IsDeployed: false},
		{Name: "b", IsDeployed: false},
	}))
	assert.True(t, anyServiceDeployed([]nextstep.ServiceState{
		{Name: "a", IsDeployed: false},
		{Name: "b", IsDeployed: true},
	}))
	assert.True(t, anyServiceDeployed([]nextstep.ServiceState{
		{Name: "a", IsDeployed: true},
	}))
}

func TestFilterDeployedServices(t *testing.T) {
	t.Run("nil state returns nil", func(t *testing.T) {
		assert.Nil(t, filterDeployedServices(nil))
	})
	t.Run("filters out undeployed services", func(t *testing.T) {
		state := &nextstep.State{
			Services: []nextstep.ServiceState{
				{Name: "a", IsDeployed: true},
				{Name: "b", IsDeployed: false},
				{Name: "c", IsDeployed: true},
			},
		}
		got := filterDeployedServices(state)
		require.NotNil(t, got)
		require.Len(t, got.Services, 2)
		assert.Equal(t, "a", got.Services[0].Name)
		assert.Equal(t, "c", got.Services[1].Name)
	})
	t.Run("returns empty slice when none deployed", func(t *testing.T) {
		state := &nextstep.State{
			Services: []nextstep.ServiceState{{Name: "a", IsDeployed: false}},
		}
		got := filterDeployedServices(state)
		require.NotNil(t, got)
		assert.Empty(t, got.Services)
	})
	t.Run("does not mutate input state", func(t *testing.T) {
		state := &nextstep.State{
			Services: []nextstep.ServiceState{
				{Name: "a", IsDeployed: true},
				{Name: "b", IsDeployed: false},
			},
		}
		_ = filterDeployedServices(state)
		assert.Len(t, state.Services, 2, "clone must not modify input")
	})
}
