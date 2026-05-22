// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"azureaiagent/internal/cmd/doctor"
	"azureaiagent/internal/cmd/nextstep"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renderConcise / renderVerbose are tiny wrappers to keep the test bodies
// readable; both flow through printDoctorReportText so streaming parity is
// implicitly exercised by TestPrintDoctorReportText_StreamingPiecesMatch
// BufferedReport below.

func renderConcise(t *testing.T, r doctor.Report, trailing []nextstep.Suggestion, showNext bool) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, printDoctorReportText(&buf, r, trailing, showNext, false))
	return buf.String()
}

func renderVerbose(t *testing.T, r doctor.Report, trailing []nextstep.Suggestion, showNext bool) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, printDoctorReportText(&buf, r, trailing, showNext, true))
	return buf.String()
}

// TestPrintDoctorReportText_ConciseDefaults locks the default (non-debug)
// rendering contract: PASS shows just the check name, FAIL shows a one-line
// Message + Suggestion, SKIP inlines the skip reason after "-- skipped".
func TestPrintDoctorReportText_ConciseDefaults(t *testing.T) {
	report := doctor.Report{
		Checks: []doctor.Result{
			{ID: "local.grpc-extension", Name: "azd extension reachable",
				Status: doctor.StatusPass, Message: "running"},
			{ID: "local.azure-yaml", Name: "azure.yaml valid",
				Status:     doctor.StatusFail,
				Message:    "no azure.yaml in current directory",
				Suggestion: "azd ai agent init",
				Links:      []string{"https://aka.ms/azd-ai-agent-init"}},
			{ID: "local.environment-selected", Name: "azd environment selected",
				Status: doctor.StatusSkip, Message: "skipped: upstream check blocked"},
		},
		Summary: doctor.Summary{Pass: 1, Fail: 1, Skip: 1},
	}

	got := renderConcise(t, report, nil, false)

	assert.True(t, strings.HasPrefix(got, "azd ai agent doctor\n"), "header line")
	assert.Contains(t, got, "\nLocal\n", "Local section header emitted")
	assert.Contains(t, got, "   (✓) azd extension reachable\n", "PASS: name only")
	assert.NotContains(t, got, "       running", "PASS suppresses Message in concise mode")
	assert.Contains(t, got, "   (x) azure.yaml valid\n", "FAIL glyph")
	assert.Contains(t, got, "       No azure.yaml in current directory", "Message capitalized + included")
	assert.Contains(t, got, "       fix: azd ai agent init",
		"Suggestion keeps lowercase 'azd' brand-name prefix")
	assert.NotContains(t, got, "https://aka.ms/azd-ai-agent-init", "Links suppressed in concise mode")
	assert.Contains(t, got, "   (-) azd environment selected -- skipped (upstream check blocked)\n",
		"SKIP inlines reason after '-- skipped'")
	assert.Contains(t, got, "1 passed, 1 failed, 1 skipped", "summary line")
	assert.NotContains(t, got, "0 warned", "warn count must be hidden when zero")
	assert.NotContains(t, got, "0 info", "info count must be hidden when zero")
}

// TestPrintDoctorReportText_VerboseDebug locks the --debug rendering: full
// Message + full Suggestion + Links are emitted, with first-letter
// capitalization applied to Message/Suggestion.
func TestPrintDoctorReportText_VerboseDebug(t *testing.T) {
	report := doctor.Report{
		Checks: []doctor.Result{
			{ID: "local.grpc-extension", Name: "azd extension reachable",
				Status: doctor.StatusPass, Message: "azd extension reachable (version 0.1.32-preview)"},
			{ID: "local.azure-yaml", Name: "azure.yaml valid",
				Status:     doctor.StatusFail,
				Message:    "no azure.yaml in current directory\nrun init from your project root",
				Suggestion: "azd ai agent init",
				Links:      []string{"https://aka.ms/azd-ai-agent-init"}},
		},
		Summary: doctor.Summary{Pass: 1, Fail: 1},
	}

	got := renderVerbose(t, report, nil, false)

	assert.Contains(t, got, "   (✓) azd extension reachable\n")
	assert.Contains(t, got, "       azd extension reachable (version 0.1.32-preview)",
		"verbose mode keeps full Message; 'azd' lead stays lowercase")
	assert.Contains(t, got, "       No azure.yaml in current directory\n")
	assert.Contains(t, got, "       run init from your project root\n",
		"continuation line preserved at same indent")
	assert.Contains(t, got, "       fix: azd ai agent init",
		"Suggestion 'azd' prefix stays lowercase in verbose mode")
	assert.Contains(t, got, "       https://aka.ms/azd-ai-agent-init", "Links rendered in verbose mode")
}

// TestPrintDoctorReportText_Sections verifies that checks are grouped by
// category (Local / Authentication / Remote) with one blank line + header
// between groups and that remote.auth is broken out as its own section.
func TestPrintDoctorReportText_Sections(t *testing.T) {
	report := doctor.Report{
		Checks: []doctor.Result{
			{ID: "local.grpc-extension", Name: "azd extension reachable", Status: doctor.StatusPass},
			{ID: "remote.auth", Name: "authentication", Status: doctor.StatusPass},
			{ID: "remote.foundry-endpoint", Name: "Foundry project endpoint reachable",
				Status: doctor.StatusPass},
		},
		Summary: doctor.Summary{Pass: 3},
	}

	got := renderConcise(t, report, nil, false)

	localIdx := strings.Index(got, "\nLocal\n")
	authIdx := strings.Index(got, "\nAuthentication\n")
	remoteIdx := strings.Index(got, "\nRemote\n")

	require.GreaterOrEqual(t, localIdx, 0, "Local section header present")
	require.GreaterOrEqual(t, authIdx, 0, "Authentication section header present")
	require.GreaterOrEqual(t, remoteIdx, 0, "Remote section header present")
	assert.Less(t, localIdx, authIdx, "Local precedes Authentication")
	assert.Less(t, authIdx, remoteIdx, "Authentication precedes Remote")
}

// TestPrintDoctorReportText_StreamingPiecesMatchBufferedReport locks the
// parity contract between the streaming path (writeHeader/writeCheck/
// writeFooter called per result) and the buffered path
// (printDoctorReportText with a fully-assembled Report).
// TestPrintDoctorReportText_StreamingPiecesMatchBufferedReport verifies that
// the streaming render path (header → per-check writes → footer) produces
// byte-identical output to the buffered `printDoctorReportText`. The matrix
// covers both concise (`debug=false`) and verbose (`debug=true`) modes so a
// future change that diverges either branch is caught immediately. The
// fixture exercises the verbose-only branches: a PASS with Message detail,
// a multi-line Suggestion (so `writeIndentedBlock` runs), and Links.
func TestPrintDoctorReportText_StreamingPiecesMatchBufferedReport(t *testing.T) {
	report := doctor.Report{
		Checks: []doctor.Result{
			{ID: "local.grpc-extension", Name: "azd extension reachable",
				Status:  doctor.StatusPass,
				Message: "running"},
			{ID: "local.azure-yaml", Name: "azure.yaml valid",
				Status:     doctor.StatusFail,
				Message:    "no azure.yaml in current directory",
				Suggestion: "azd ai agent init\nthen re-run doctor",
				Links:      []string{"https://aka.ms/azd-ai-agent-init"}},
			{ID: "remote.auth", Name: "authentication",
				Status:  doctor.StatusSkip,
				Message: "skipped: local-only"},
		},
		Summary: doctor.Summary{Pass: 1, Fail: 1, Skip: 1},
	}
	trailing := []nextstep.Suggestion{
		{Command: "azd ai agent run", Description: "start the agent locally", Priority: 10},
	}

	cases := []struct {
		name  string
		debug bool
	}{
		{name: "concise", debug: false},
		{name: "verbose", debug: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buffered bytes.Buffer
			require.NoError(t, printDoctorReportText(&buffered, report, trailing, true, tc.debug))

			var streamed bytes.Buffer
			streamRenderer := newDoctorRenderer(&streamed, tc.debug)
			require.NoError(t, streamRenderer.writeHeader())
			for _, result := range report.Checks {
				require.NoError(t, streamRenderer.writeCheck(result))
			}
			require.NoError(t, streamRenderer.writeFooter(report, trailing, true))

			assert.Equal(t, buffered.String(), streamed.String())
		})
	}
}

// TestPrintDoctorReportText_TrailingNextOnAllGreen verifies the "Next:"
// footer block follows the summary on a clean report.
func TestPrintDoctorReportText_TrailingNextOnAllGreen(t *testing.T) {
	report := doctor.Report{
		Checks:  []doctor.Result{{ID: "local.grpc-extension", Name: "azd extension reachable", Status: doctor.StatusPass}},
		Summary: doctor.Summary{Pass: 1},
	}
	trailing := []nextstep.Suggestion{
		{Command: "azd ai agent run", Description: "start the agent locally", Priority: 10},
	}

	got := renderConcise(t, report, trailing, true)

	assert.Contains(t, got, "Next:")
	assert.Contains(t, got, "azd ai agent run")
	sumIdx := strings.Index(got, "1 passed")
	nextIdx := strings.Index(got, "Next:")
	require.GreaterOrEqual(t, sumIdx, 0)
	require.GreaterOrEqual(t, nextIdx, 0)
	assert.Less(t, sumIdx, nextIdx)
}

// TestPrintDoctorReportText_TrailingSuppressedWhenShowNextFalse verifies
// that showNext=false hides the "Next:" block even when trailing is
// non-empty (e.g., non-TTY caller).
func TestPrintDoctorReportText_TrailingSuppressedWhenShowNextFalse(t *testing.T) {
	report := doctor.Report{
		Checks:  []doctor.Result{{ID: "local.grpc-extension", Name: "azd extension reachable", Status: doctor.StatusPass}},
		Summary: doctor.Summary{Pass: 1},
	}
	trailing := []nextstep.Suggestion{
		{Command: "azd ai agent run", Description: "start the agent locally", Priority: 10},
	}

	got := renderConcise(t, report, trailing, false)
	assert.NotContains(t, got, "Next:")
	assert.NotContains(t, got, "azd ai agent run")
}

// TestPrintDoctorReportText_ToFixBlockOnFailure verifies the actionable
// "To fix" footer is emitted on failure, with commands in the canonical
// remediation order (login → provision → deploy) and deduplicated across
// multiple failed checks that map to the same command.
func TestPrintDoctorReportText_ToFixBlockOnFailure(t *testing.T) {
	report := doctor.Report{
		Checks: []doctor.Result{
			{ID: "remote.auth", Name: "authentication", Status: doctor.StatusPass},
			{ID: "remote.foundry-endpoint", Name: "Foundry endpoint",
				Status: doctor.StatusFail, Message: "endpoint unreachable"},
			{ID: "remote.model-deployments", Name: "model deployments",
				Status: doctor.StatusFail, Message: "model missing"},
			{ID: "remote.agent-status", Name: "agents active",
				Status: doctor.StatusFail, Message: "1 of 1 agents have not been deployed"},
		},
		Summary: doctor.Summary{Pass: 1, Fail: 3},
	}

	got := renderConcise(t, report, nil, false)

	assert.Contains(t, got, "To fix, run these commands in order:")
	assert.Contains(t, got, "1. azd provision")
	assert.Contains(t, got, "2. azd deploy")
	assert.NotRegexp(t, "(?m)3\\. azd provision",
		"azd provision must be deduplicated even when multiple checks request it")
	assert.Contains(t, got, "Then re-run `azd ai agent doctor` to verify.")
}

// TestPrintDoctorReportText_ToFixBlockSuppressesNextOnFailure verifies the
// trailing "Next:" block is never emitted on a failed report (it would
// compete with the actionable "To fix" footer).
func TestPrintDoctorReportText_ToFixBlockSuppressesNextOnFailure(t *testing.T) {
	report := doctor.Report{
		Checks: []doctor.Result{
			{ID: "remote.agent-status", Name: "agents active",
				Status: doctor.StatusFail, Message: "1 of 1 agents have not been deployed"},
		},
		Summary: doctor.Summary{Fail: 1},
	}
	trailing := []nextstep.Suggestion{
		{Command: "azd ai agent run", Description: "should not render", Priority: 10},
	}

	got := renderConcise(t, report, trailing, true)
	assert.Contains(t, got, "To fix, run these commands in order:")
	assert.NotContains(t, got, "Next:")
	assert.NotContains(t, got, "should not render")
}

// TestWriteSummaryLine_WithWarnAndInfo pins the inverse contract: when
// warn/info counts are non-zero they MUST appear in the summary line. This
// guards against a regression that silently drops the segments.
func TestWriteSummaryLine_WithWarnAndInfo(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeSummaryLine(&buf, doctor.Summary{Pass: 2, Warn: 1, Info: 1}))
	assert.Equal(t, "2 passed, 0 failed, 0 skipped, 1 warned, 1 info\n", buf.String())
}

// TestPrintDoctorReportText_ToFixBlockAllUnmappedFailures verifies the
// fallback footer when every failed check lacks a canonical remediation
// command in `remediationForCheckID`. The footer must still render a
// "To fix:" block (deferring to per-check `fix:` notes) and the re-run
// instruction so the user is never left without an actionable next step.
func TestPrintDoctorReportText_ToFixBlockAllUnmappedFailures(t *testing.T) {
	report := doctor.Report{
		Checks: []doctor.Result{
			{ID: "local.toolboxes", Name: "toolboxes resolvable",
				Status:     doctor.StatusFail,
				Message:    "failed to assemble agent state",
				Suggestion: "Re-run `azd ai agent doctor` after fixing upstream errors."},
		},
		Summary: doctor.Summary{Fail: 1},
	}

	got := renderConcise(t, report, nil, false)

	assert.Contains(t, got, "To fix:", "unmapped-only failure still emits a footer header")
	assert.NotContains(t, got, "To fix, run these commands in order:",
		"unmapped-only failure must NOT promise a command list it cannot deliver")
	assert.Contains(t, got, "Review the fix: notes above for each failed check.",
		"unmapped-only failure points the user back to the per-check fix: lines")
	assert.Contains(t, got, "Then re-run `azd ai agent doctor` to verify.",
		"re-run instruction must always close the footer on failure")
}

// TestPrintDoctorReportText_ToFixBlockMixedMappedAndUnmapped verifies that
// when at least one failure has a canonical remediation AND another failure
// is unmapped, the numbered command list is rendered AND a pointer to the
// per-check `fix:` notes is appended so the user knows the canonical commands
// are not exhaustive.
func TestPrintDoctorReportText_ToFixBlockMixedMappedAndUnmapped(t *testing.T) {
	report := doctor.Report{
		Checks: []doctor.Result{
			{ID: "remote.foundry-endpoint", Name: "Foundry endpoint",
				Status: doctor.StatusFail, Message: "endpoint unreachable"},
			{ID: "local.toolboxes", Name: "toolboxes resolvable",
				Status:     doctor.StatusFail,
				Message:    "failed to assemble agent state",
				Suggestion: "Re-run `azd ai agent doctor` after fixing upstream errors."},
		},
		Summary: doctor.Summary{Fail: 2},
	}

	got := renderConcise(t, report, nil, false)

	assert.Contains(t, got, "To fix, run these commands in order:",
		"mapped failure produces the numbered command list")
	assert.Contains(t, got, "1. azd provision", "canonical command for foundry-endpoint")
	assert.Contains(t, got, "Also review the fix: notes above for any remaining failed checks.",
		"unmapped failure alongside mapped one appends a pointer to per-check fix: lines")
	assert.Contains(t, got, "Then re-run `azd ai agent doctor` to verify.")
}

// TestPrintDoctorReportText_EmptyReport verifies defensive behavior: a
// Report with no checks does not crash and surfaces a clear summary line.
func TestPrintDoctorReportText_EmptyReport(t *testing.T) {
	got := renderConcise(t, doctor.Report{}, nil, false)
	assert.Contains(t, got, "azd ai agent doctor")
	assert.Contains(t, got, "No checks executed")
}

// TestStatusGlyphAndLabel pins the per-status glyph contract; new format
// uses parenthesized indicators which are also exposed via statusGlyph.
func TestStatusGlyphAndLabel(t *testing.T) {
	tests := []struct {
		status   doctor.Status
		glyph    string
		label    string
		dataName string
	}{
		{doctor.StatusPass, "(✓)", "PASS", "pass"},
		{doctor.StatusWarn, "(!)", "WARN", "warn"},
		{doctor.StatusFail, "(x)", "FAIL", "fail"},
		{doctor.StatusSkip, "(-)", "SKIP", "skip"},
		{doctor.StatusInfo, "(ⓘ)", "INFO", "info"},
		{doctor.Status("bogus"), "(?)", "UNKN", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.dataName, func(t *testing.T) {
			g, l := statusGlyphAndLabel(tt.status)
			assert.Equal(t, tt.glyph, g)
			assert.Equal(t, tt.label, l)
		})
	}
}

// TestCategoryForCheck pins the section-routing contract.
func TestCategoryForCheck(t *testing.T) {
	tests := map[string]string{
		"local.grpc-extension":    categoryLocal,
		"local.azure-yaml":        categoryLocal,
		"remote.auth":             categoryAuth,
		"remote.foundry-endpoint": categoryRemote,
		"remote.agent-status":     categoryRemote,
		"unknown.something":       categoryRemote, // fallback bucket
		"":                        categoryRemote,
	}
	for id, want := range tests {
		t.Run(id, func(t *testing.T) {
			assert.Equal(t, want, categoryForCheck(id))
		})
	}
}

// TestCapitalize pins the capitalization helper contract: skip non-letter
// leads (numbers, env vars, backticks), idempotent on already-capitalized
// strings, and skip brand-name prefixes ("azd", "azure.yaml", "skipped:")
// that are conventionally lowercase in the rendered report.
//
//nolint:gosec // gosec G101: false positive on the "token acquisition failed" test fixture string.
func TestCapitalize(t *testing.T) {
	tests := map[string]string{
		"":                          "",
		"endpoint reachable":        "Endpoint reachable",
		"Endpoint reachable":        "Endpoint reachable",
		"FOUNDRY_PROJECT_ENDPOINT": "FOUNDRY_PROJECT_ENDPOINT",
		"1 of 1 agents":             "1 of 1 agents",
		"`azure.ai.agent`":          "`azure.ai.agent`",
		// Brand-name leads stay lowercase.
		"skipped: upstream blocked":    "skipped: upstream blocked",
		"azd extension reachable":      "azd extension reachable",
		"azure.yaml parsed":            "azure.yaml parsed",
		"agent.yaml valid for service": "agent.yaml valid for service",
		// Generic lowercase leads do get capitalized.
		"cancelled by user":              "Cancelled by user",
		"no manual env vars are missing": "No manual env vars are missing",
		"failed to get project config":   "Failed to get project config",
		"token acquisition failed":       "Token acquisition failed",
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, capitalize(in))
		})
	}
}

// TestFirstLine pins the first-line helper used to collapse multi-line
// Message/Suggestion strings in concise mode.
func TestFirstLine(t *testing.T) {
	assert.Equal(t, "", firstLine(""))
	assert.Equal(t, "hello", firstLine("hello"))
	assert.Equal(t, "hello", firstLine("hello\nworld"))
	assert.Equal(t, "hello", firstLine("  hello  \nworld"))
	assert.Equal(t, "", firstLine("\n\n"))
}

// TestAnyServiceDeployed is unchanged from the previous suite; pinned for
// regression protection on the doctor's "Next:" trailing-block predicate.
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

// TestFilterDeployedServices is unchanged from the previous suite.
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

// TestFilterDeployedServices_ChainedIntoResolveAfterDeploy is the end-to-end
// contract for doctor's post-deploy guidance block; preserved verbatim.
func TestFilterDeployedServices_ChainedIntoResolveAfterDeploy(t *testing.T) {
	t.Parallel()

	state := &nextstep.State{
		Services: []nextstep.ServiceState{
			{Name: "alpha", IsDeployed: true, Protocol: nextstep.ProtocolResponses},
			{Name: "beta", IsDeployed: false, Protocol: nextstep.ProtocolResponses},
		},
	}

	out := nextstep.ResolveAfterDeploy(filterDeployedServices(state), nil, nil)

	require.Len(t, out, 2, "filtered state has one deployed service → show + invoke")
	assert.Equal(t, "azd ai agent show alpha", out[0].Command,
		"command must be service-qualified even when filtered list has len==1")
	assert.Equal(t, `azd ai agent invoke alpha "Hello!"`, out[1].Command,
		"invoke command must also be service-qualified")
}
