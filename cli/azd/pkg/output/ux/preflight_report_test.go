// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreflightReport_EmptyItems(t *testing.T) {
	report := &PreflightReport{}
	require.Empty(t, report.ToString(""))
	require.False(t, report.HasErrors())
	require.False(t, report.HasWarnings())
}

func TestPreflightReport_WarningsOnly(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{IsError: false, Message: "first warning"},
			{IsError: false, Message: "second warning"},
		},
	}

	result := report.ToString("")
	require.Contains(t, result, "first warning")
	require.Contains(t, result, "second warning")
	require.NotContains(t, result, "Failed")

	require.False(t, report.HasErrors())
	require.True(t, report.HasWarnings())
}

func TestPreflightReport_ErrorsOnly(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{IsError: true, Message: "critical error"},
		},
	}

	result := report.ToString("")
	require.Contains(t, result, "critical error")
	require.NotContains(t, result, "Warning")

	require.True(t, report.HasErrors())
	require.False(t, report.HasWarnings())
}

func TestPreflightReport_WarningsBeforeErrors(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{IsError: true, Message: "an error"},
			{IsError: false, Message: "a warning"},
		},
	}

	result := report.ToString("")
	// Warning should appear before error in the output
	warnIdx := indexOf(result, "a warning")
	errIdx := indexOf(result, "an error")
	require.Greater(t, errIdx, warnIdx, "warnings should appear before errors")

	require.True(t, report.HasErrors())
	require.True(t, report.HasWarnings())
}

func TestPreflightReport_MarshalJSON(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{IsError: false, Message: "w1"},
			{IsError: true, Message: "e1"},
		},
	}

	data, err := json.Marshal(report)
	require.NoError(t, err)
	require.Contains(t, string(data), "1 warning(s)")
	require.Contains(t, string(data), "1 error(s)")
}

func TestPreflightReport_WarningWithSuggestion(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{
				IsError:    false,
				Message:    "insufficient quota for model gpt-4o",
				Suggestion: "Reduce capacity to 140 or change location.",
			},
		},
	}

	result := report.ToString("  ")
	require.Contains(t, result, "insufficient quota for model gpt-4o")
	require.Contains(t, result, "Suggestion:")
	require.Contains(t, result, "Reduce capacity to 140 or change location.")

	// Suggestion should appear after the warning message
	warnIdx := indexOf(result, "insufficient quota")
	suggIdx := indexOf(result, "Reduce capacity")
	require.Greater(t, suggIdx, warnIdx, "suggestion should appear after warning message")
}

func TestPreflightReport_WarningWithLinks(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{
				IsError:    false,
				Message:    "model not found",
				Suggestion: "Verify the model name.",
				Links: []PreflightReportLink{
					{URL: "https://example.com/models", Title: "Supported models"},
					{URL: "https://example.com/raw-link"},
				},
			},
		},
	}

	result := report.ToString("  ")
	require.Contains(t, result, "model not found")
	require.Contains(t, result, "Suggestion:")
	require.Contains(t, result, "Verify the model name.")
	// In non-terminal mode, WithHyperlink falls back to plain URL
	require.Contains(t, result, "https://example.com/models")
	require.Contains(t, result, "https://example.com/raw-link")
}

func TestPreflightReport_NoSuggestion(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{IsError: false, Message: "simple warning"},
		},
	}

	result := report.ToString("")
	require.Contains(t, result, "simple warning")
	require.NotContains(t, result, "Suggestion:")
}

func TestPreflightReport_MarshalJSON_WithSuggestions(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{
				IsError:      false,
				DiagnosticID: "ai_model_quota_exceeded",
				Message:      "insufficient quota",
				Suggestion:   "Reduce capacity to 140.",
				Links: []PreflightReportLink{
					{URL: "https://example.com/quotas", Title: "Quota docs"},
				},
			},
			{IsError: true, DiagnosticID: "role_error", Message: "role missing"},
		},
	}

	data, err := json.Marshal(report)
	require.NoError(t, err)

	var parsed struct {
		Type    string `json:"type"`
		Summary string `json:"summary"`
		Items   []struct {
			Severity     string `json:"severity"`
			DiagnosticID string `json:"diagnosticId"`
			Message      string `json:"message"`
			Suggestion   string `json:"suggestion"`
			Links        []struct {
				URL   string `json:"url"`
				Title string `json:"title"`
			} `json:"links"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(data, &parsed))

	require.Equal(t, "preflight", parsed.Type)
	require.Contains(t, parsed.Summary, "1 warning(s)")
	require.Contains(t, parsed.Summary, "1 error(s)")
	require.Len(t, parsed.Items, 2)

	// First item: warning with suggestion and links
	require.Equal(t, "warning", parsed.Items[0].Severity)
	require.Equal(t, "ai_model_quota_exceeded", parsed.Items[0].DiagnosticID)
	require.Equal(t, "Reduce capacity to 140.", parsed.Items[0].Suggestion)
	require.Len(t, parsed.Items[0].Links, 1)
	require.Equal(t, "https://example.com/quotas", parsed.Items[0].Links[0].URL)
	require.Equal(t, "Quota docs", parsed.Items[0].Links[0].Title)

	// Second item: error without suggestion
	require.Equal(t, "error", parsed.Items[1].Severity)
	require.Empty(t, parsed.Items[1].Suggestion)
	require.Empty(t, parsed.Items[1].Links)
}

func TestPreflightReport_Indentation(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{IsError: false, Message: "indented warning"},
		},
	}

	result := report.ToString("  ")
	require.Contains(t, result, "  ")
	require.Contains(t, result, "indented warning")
}

func TestPreflightReport_MultiLineMessageIndentation(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{
				IsError: false,
				Message: "Model \"gpt-4o\" not found in eastus2\n" +
					"Model not found in AI model catalog.",
			},
		},
	}

	result := report.ToString("  ")
	lines := strings.Split(result, "\n")
	require.Len(t, lines, 2)
	// First line has the warning prefix
	require.Contains(t, lines[0], "(!) Warning:")
	require.Contains(t, lines[0], "Model \"gpt-4o\" not found")
	// Second line is indented at the same level
	require.True(t, strings.HasPrefix(lines[1], "  "),
		"continuation line should be indented")
	require.Contains(t, lines[1], "Model not found in AI model catalog.")
}

func TestPreflightReport_MultiLineWithSuggestion(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{
				IsError: false,
				Message: "Insufficient quota for model \"gpt-4o\" in eastus2\n" +
					"Requested: 99999 · Available: 140",
				Suggestion: "Reduce capacity to 140.",
			},
		},
	}

	result := report.ToString("  ")
	// All three parts should be present in order
	msgIdx := indexOf(result, "Insufficient quota")
	detailIdx := indexOf(result, "Requested: 99999")
	suggIdx := indexOf(result, "Reduce capacity")
	require.Greater(t, detailIdx, msgIdx,
		"detail should appear after title")
	require.Greater(t, suggIdx, detailIdx,
		"suggestion should appear after detail")
}

// indexOf returns the byte offset of substr in s, or -1 if not found.
func indexOf(s, substr string) int {
	for i := range len(s) - len(substr) + 1 {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
