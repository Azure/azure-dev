// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"fmt"
	"strings"
)

// FailOnPromptError returns a consistent error for when --fail-on-prompt blocks a simple prompt.
func FailOnPromptError(promptMessage string) error {
	return fmt.Errorf(
		"interactive prompt not allowed in strict mode (--fail-on-prompt): %q"+
			" -- provide the value via command-line flags or environment variables",
		promptMessage,
	)
}

// FailOnPromptSelectError returns a consistent error for when --fail-on-prompt blocks a selection prompt,
// including the available options in the message.
func FailOnPromptSelectError(promptMessage string, options []string) error {
	return fmt.Errorf(
		"interactive prompt not allowed in strict mode (--fail-on-prompt): %q"+
			" (available options: %s)"+
			" -- specify via command-line flags or environment variables",
		promptMessage,
		strings.Join(options, ", "),
	)
}
