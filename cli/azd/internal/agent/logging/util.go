// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package logging

// TruncateString truncates a string to maxLen characters and adds "..." if truncated.
func TruncateString(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}
