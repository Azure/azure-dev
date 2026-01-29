// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import "azure.ai.finetune/pkg/models"

// IsTerminalStatus returns true if the job status represents a terminal state
// (succeeded, failed, or cancelled).
func IsTerminalStatus(status models.JobStatus) bool {
	return status == models.StatusSucceeded || status == models.StatusFailed || status == models.StatusCancelled
}
