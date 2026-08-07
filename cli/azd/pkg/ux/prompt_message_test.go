// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"bytes"
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
)

func TestFormatPromptMessage(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{name: "plain", message: "Continue", want: "Continue: "},
		{name: "question", message: "Continue?", want: "Continue? "},
		{name: "colon", message: "Select a deployment:", want: "Select a deployment: "},
		{name: "period", message: "Continue.", want: "Continue. "},
		{name: "exclamation", message: "Continue!", want: "Continue! "},
		{name: "semicolon", message: "Continue;", want: "Continue; "},
		{name: "trim trailing whitespace before punctuation", message: "Continue?   \t", want: "Continue? "},
		{name: "trim trailing whitespace for plain message", message: "Continue   \t", want: "Continue: "},
		// Localized and extension-supplied text can carry non-breaking spaces,
		// which must trim so the punctuation check sees the real last rune.
		{name: "trim trailing non-breaking space", message: "Continue?\u00a0", want: "Continue? "},
		{name: "empty", message: "", want: ""},
		{name: "whitespace only", message: "  \t\n", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatPromptMessage(tt.message))
		})
	}
}

// renderPromptMessage is the integration point shared by every prompt
// primitive, so these cases pin the full rendered header rather than just the
// separator. Color is forced on because the styling sequences are part of what
// is being asserted.
func TestRenderPromptMessage(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{name: "plain gets colon", message: "Continue", want: "\x1b[94m? \x1b[0m\x1b[1mContinue: \x1b[22m"},
		{name: "question keeps mark", message: "Continue?", want: "\x1b[94m? \x1b[0m\x1b[1mContinue? \x1b[22m"},
		// A blank message writes the marker alone, with no empty bold pair.
		{name: "empty writes marker only", message: "", want: "\x1b[94m? \x1b[0m"},
		{name: "whitespace writes marker only", message: "  \t", want: "\x1b[94m? \x1b[0m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previous := color.NoColor
			color.NoColor = false
			t.Cleanup(func() { color.NoColor = previous })

			var buf bytes.Buffer
			renderPromptMessage(NewPrinter(&buf), tt.message)

			assert.Equal(t, tt.want, buf.String())
		})
	}
}
