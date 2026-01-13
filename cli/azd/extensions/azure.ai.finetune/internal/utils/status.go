// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import "azure.ai.finetune/pkg/models"

// getStatusSymbol returns a symbol representation for job status
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

func IsTerminalStatus(s models.JobStatus) bool {
	return s == models.StatusSucceeded || s == models.StatusFailed || s == models.StatusCancelled
}
