// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"testing"

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
		{name: "empty", message: "", want: ""},
		{name: "whitespace only", message: "  \t\n", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatPromptMessage(tt.message))
		})
	}
}
