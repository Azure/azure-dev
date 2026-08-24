// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

func isTerminalResponseStatus(status string) bool {
	switch status {
	case "completed", "failed", "incomplete", "cancelled":
		return true
	default:
		return false
	}
}
