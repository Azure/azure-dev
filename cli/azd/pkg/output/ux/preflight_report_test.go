// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
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

// indexOf returns the byte offset of substr in s, or -1 if not found.
func indexOf(s, substr string) int {
	for i := range len(s) - len(substr) + 1 {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
