// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import "azure.ai.finetune/pkg/models"

// GetStatusSymbol returns a symbol representation for job status.
func GetStatusSymbol(status models.JobStatus) string {
	switch status {
	case models.StatusPending:
		return "⌛"
	case models.StatusQueued:
		return "📚"
	case models.StatusRunning:
		return "🔄"
	case models.StatusSucceeded:
		return "✅"
	case models.StatusFailed:
		return "💥"
	case models.StatusCancelled:
		return "❌"
	default:
		return "❓"
	}
}

// IsTerminalStatus returns true if the job status represents a terminal state
// (succeeded, failed, or cancelled).
func IsTerminalStatus(status models.JobStatus) bool {
	return status == models.StatusSucceeded || status == models.StatusFailed || status == models.StatusCancelled
}
