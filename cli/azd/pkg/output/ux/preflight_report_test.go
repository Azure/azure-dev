// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/test/snapshot"
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

func TestPreflightReport_MarshalJSON_Envelope(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{IsError: false, Message: "w1", Suggestion: "fix it"},
			{IsError: true, Message: "e1"},
		},
	}

	data, err := json.Marshal(report)
	require.NoError(t, err)

	// MarshalJSON wraps output in EventEnvelope
	var parsed struct {
		Type string `json:"type"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(data, &parsed))
	require.Equal(t, "consoleMessage", string(parsed.Type))
	require.Contains(t, parsed.Data.Message, "1 warning(s)")
	require.Contains(t, parsed.Data.Message, "1 error(s)")
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

func TestPreflightReport_WriteItem_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		item     PreflightReportItem
		contains []string
		excludes []string
	}{
		{
			name:     "empty message",
			item:     PreflightReportItem{Message: ""},
			excludes: []string{"Warning"},
		},
		{
			name: "trailing newline in message",
			item: PreflightReportItem{
				Message: "title line\n",
			},
			contains: []string{"title line"},
		},
		{
			name: "consecutive newlines in message",
			item: PreflightReportItem{
				Message: "first\n\nthird",
			},
			contains: []string{"first", "third"},
		},
		{
			name: "nil links slice",
			item: PreflightReportItem{
				Message: "msg",
				Links:   nil,
			},
			contains: []string{"msg"},
			excludes: []string{"•"},
		},
		{
			name: "empty links slice",
			item: PreflightReportItem{
				Message: "msg",
				Links:   []PreflightReportLink{},
			},
			contains: []string{"msg"},
			excludes: []string{"•"},
		},
		{
			name: "empty suggestion string",
			item: PreflightReportItem{
				Message:    "msg",
				Suggestion: "",
			},
			contains: []string{"msg"},
			excludes: []string{"Suggestion:"},
		},
		{
			name: "message with leading newline",
			item: PreflightReportItem{
				Message: "\nleading newline",
			},
			contains: []string{"leading newline"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := &PreflightReport{
				Items: []PreflightReportItem{tt.item},
			}
			result := report.ToString("  ")
			for _, s := range tt.contains {
				require.Contains(t, result, s)
			}
			for _, s := range tt.excludes {
				require.NotContains(t, result, s)
			}
		})
	}
}

// Snapshot tests — one per diagnostic type, plus one combined report.
// Update snapshots with: UPDATE_SNAPSHOTS=true go test ./pkg/output/ux/...

func TestPreflightReport_Snapshot_RoleAssignmentMissing(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{
				IsError:      false,
				DiagnosticID: "role_assignment_missing",
				Message: "Principal (5a3acce7-bcc4-4ebc-b4b3-c3b9f17535cb)" +
					" lacks role assignment permissions on" +
					" subscription 3819cb9d-0f7c-4284-9e93-220e7fb2367a\n" +
					"The deployment includes role assignments" +
					" and will fail without" +
					" Microsoft.Authorization/roleAssignments/write" +
					" permission.",
				Suggestion: "Ensure you have the" +
					" 'Role Based Access Control Administrator'," +
					" 'User Access Administrator'," +
					" 'Owner', or a custom role with" +
					" 'Microsoft.Authorization/roleAssignments/write'" +
					" assigned to your account.",
			},
		},
	}
	snapshot.SnapshotT(t, report.ToString("  "))
}

func TestPreflightReport_Snapshot_RoleAssignmentConditional(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{
				IsError:      false,
				DiagnosticID: "role_assignment_conditional",
				Message: "Principal (5a3acce7-bcc4-4ebc-b4b3-c3b9f17535cb)" +
					" has conditional role assignment permissions on" +
					" subscription 3819cb9d-0f7c-4284-9e93-220e7fb2367a\n" +
					"An ABAC condition may restrict which roles can be assigned." +
					" The deployment may fail if the condition does not permit" +
					" the specific role assignments in the template.",
			},
		},
	}
	snapshot.SnapshotT(t, report.ToString("  "))
}

func TestPreflightReport_Snapshot_ReservedResourceName(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{
				IsError:      false,
				DiagnosticID: "reserved_resource_name",
				Message: "Resource \"login-server\"" +
					" (Microsoft.Web/sites)" +
					" contains the reserved word \"login\"\n" +
					"Azure does not allow reserved words in" +
					" resource names. The deployment will fail.",
				Links: []PreflightReportLink{
					{
						URL: "https://learn.microsoft.com/azure/" +
							"azure-resource-manager/templates/" +
							"error-reserved-resource-name",
						Title: "Reserved resource name errors",
					},
				},
			},
		},
	}
	snapshot.SnapshotT(t, report.ToString("  "))
}

func TestPreflightReport_Snapshot_AiModelNotFound(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{
				IsError:      false,
				DiagnosticID: "ai_model_not_found",
				Message: "Model \"no-model\" (SKU: GlobalStandard)" +
					" not found in eastus2\n" +
					"Model not found in AI model catalog." +
					" Provisioning will likely fail.",
				Suggestion: "Verify the model name, SKU," +
					" and version are correct.",
				Links: []PreflightReportLink{
					{
						URL:   "https://learn.microsoft.com/azure/ai-services/openai/concepts/models",
						Title: "Azure OpenAI supported models and regions",
					},
				},
			},
		},
	}
	snapshot.SnapshotT(t, report.ToString("  "))
}

func TestPreflightReport_Snapshot_AiModelQuotaExceeded(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{
				IsError:      false,
				DiagnosticID: "ai_model_quota_exceeded",
				Message: "Insufficient quota for model \"gpt-4o\"" +
					" (SKU: GlobalStandard) in eastus2\n" +
					"Requested: 99999 · Available: 140",
				Suggestion: "Reduce the requested capacity to 140" +
					" or change your deployment location via" +
					" azd env set AZURE_LOCATION <location>." +
					" You can also request a quota increase" +
					" in the Azure portal.",
				Links: []PreflightReportLink{
					{
						URL:   "https://learn.microsoft.com/azure/quotas/quickstart-increase-quota-portal",
						Title: "Increase Azure subscription quotas",
					},
				},
			},
		},
	}
	snapshot.SnapshotT(t, report.ToString("  "))
}

func TestPreflightReport_Snapshot_AllWarningsCombined(t *testing.T) {
	report := &PreflightReport{
		Items: []PreflightReportItem{
			{
				IsError:      false,
				DiagnosticID: "role_assignment_missing",
				Message: "Principal (5a3acce7-bcc4-4ebc-b4b3-c3b9f17535cb)" +
					" lacks role assignment permissions on" +
					" subscription 3819cb9d-0f7c-4284-9e93-220e7fb2367a\n" +
					"The deployment includes role assignments" +
					" and will fail without" +
					" Microsoft.Authorization/roleAssignments/write" +
					" permission.",
				Suggestion: "Ensure you have the" +
					" 'Role Based Access Control Administrator'," +
					" 'User Access Administrator'," +
					" 'Owner', or a custom role with" +
					" 'Microsoft.Authorization/roleAssignments/write'" +
					" assigned to your account.",
			},
			{
				IsError:      false,
				DiagnosticID: "ai_model_not_found",
				Message: "Model \"no-model\" (SKU: GlobalStandard)" +
					" not found in eastus2\n" +
					"Model not found in AI model catalog." +
					" Provisioning will likely fail.",
				Suggestion: "Verify the model name, SKU," +
					" and version are correct.",
				Links: []PreflightReportLink{
					{
						URL:   "https://learn.microsoft.com/azure/ai-services/openai/concepts/models",
						Title: "Azure OpenAI supported models and regions",
					},
				},
			},
			{
				IsError:      false,
				DiagnosticID: "ai_model_quota_exceeded",
				Message: "Insufficient quota for model \"gpt-4o\"" +
					" (SKU: GlobalStandard) in eastus2\n" +
					"Requested: 99999 · Available: 140",
				Suggestion: "Reduce the requested capacity to 140" +
					" or change your deployment location via" +
					" azd env set AZURE_LOCATION <location>." +
					" You can also request a quota increase" +
					" in the Azure portal.",
				Links: []PreflightReportLink{
					{
						URL:   "https://learn.microsoft.com/azure/quotas/quickstart-increase-quota-portal",
						Title: "Increase Azure subscription quotas",
					},
				},
			},
		},
	}
	snapshot.SnapshotT(t, report.ToString("  "))
}
