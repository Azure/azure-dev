// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"fmt"
	"strings"
	"time"

	"azd.ai.start/internal/session"
)

// TruncateString truncates a string to a maximum length
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// FormatActionsForValidation formats actions for the validation prompt
func FormatActionsForValidation(actions []session.ActionLog) string {
	if len(actions) == 0 {
		return "No actions executed"
	}

	var formatted strings.Builder
	for i, action := range actions {
		status := "SUCCESS"
		if !action.Success {
			status = "FAILED"
		}
		formatted.WriteString(fmt.Sprintf("%d. Tool: %s | Input: %s | Status: %s | Duration: %v\n",
			i+1, action.Tool, TruncateString(action.Input, 100), status, action.Duration.Round(time.Millisecond)))
		if action.Output != "" {
			formatted.WriteString(fmt.Sprintf("   Output: %s\n", TruncateString(action.Output, 200)))
		}
	}
	return formatted.String()
}
