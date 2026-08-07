// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// promptTerminalPunctuation lists the characters that end a prompt message on
// their own. A message ending in one of these keeps it instead of receiving a
// colon, so callers can phrase prompts as questions without producing "?:".
const promptTerminalPunctuation = "?:.!;"

// renderPromptMessage writes the leading "? " marker followed by the bolded
// prompt message. Every prompt primitive opens with this, so the marker, the
// bolding, and the trailing separator all live here.
func renderPromptMessage(printer Printer, message string) {
	printer.Fprintf("%s", output.WithHighLightFormat("? "))

	// Skip the bold write for a blank message so it does not emit an empty
	// pair of styling sequences wrapping no visible text.
	if formatted := formatPromptMessage(message); formatted != "" {
		printer.Fprintf("%s", BoldString("%s", formatted))
	}
}

// formatPromptMessage returns the prompt message with the separator that
// precedes the hint, value, or selection. A message that already ends in
// terminal punctuation gets only a trailing space; anything else gets ": ".
func formatPromptMessage(message string) string {
	trimmed := strings.TrimRightFunc(message, unicode.IsSpace)
	if trimmed == "" {
		return ""
	}

	last, _ := utf8.DecodeLastRuneInString(trimmed)
	if strings.ContainsRune(promptTerminalPunctuation, last) {
		return trimmed + " "
	}

	return trimmed + ": "
}
