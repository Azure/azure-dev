// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import "strings"

// formatPromptMessage returns prompt message text with normalized trailing punctuation.
func formatPromptMessage(message string) string {
	trimmed := strings.TrimRight(message, " \t\r\n")
	if trimmed == "" {
		return ": "
	}

	last := trimmed[len(trimmed)-1]
	if strings.ContainsRune("?:.!;", rune(last)) {
		return trimmed + " "
	}

	return trimmed + ": "
}
