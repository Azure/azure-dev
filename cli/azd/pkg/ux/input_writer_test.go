// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ansiShowCursor is the ANSI control sequence that makes the cursor visible;
// components emit it to restore the cursor after prompting.
const ansiShowCursor = "\033[?25h"

func TestInputCursorUsesComponentWriter(t *testing.T) {
	// Select and MultiSelect also restore their component cursor when Ask returns.
	tests := []struct {
		name                    string
		expectedShowCursorCount int
		ask                     func(context.Context, *bytes.Buffer) error
	}{
		{
			name:                    "Confirm",
			expectedShowCursorCount: 1,
			ask: func(ctx context.Context, writer *bytes.Buffer) error {
				confirm := NewConfirm(&ConfirmOptions{Writer: writer})
				confirm.WithCanvas(NewCanvas().WithWriter(writer))
				_, err := confirm.Ask(ctx)
				return err
			},
		},
		{
			name:                    "Prompt",
			expectedShowCursorCount: 1,
			ask: func(ctx context.Context, writer *bytes.Buffer) error {
				prompt := NewPrompt(&PromptOptions{Writer: writer})
				prompt.WithCanvas(NewCanvas().WithWriter(writer))
				_, err := prompt.Ask(ctx)
				return err
			},
		},
		{
			name:                    "Select",
			expectedShowCursorCount: 2,
			ask: func(ctx context.Context, writer *bytes.Buffer) error {
				selectPrompt := NewSelect(&SelectOptions{
					Writer:  writer,
					Choices: []*SelectChoice{{Value: "one", Label: "One"}},
				})
				selectPrompt.WithCanvas(NewCanvas().WithWriter(writer))
				_, err := selectPrompt.Ask(ctx)
				return err
			},
		},
		{
			name:                    "MultiSelect",
			expectedShowCursorCount: 2,
			ask: func(ctx context.Context, writer *bytes.Buffer) error {
				multiSelect := NewMultiSelect(&MultiSelectOptions{
					Writer:  writer,
					Choices: []*MultiSelectChoice{{Value: "one", Label: "One"}},
				})
				multiSelect.WithCanvas(NewCanvas().WithWriter(writer))
				_, err := multiSelect.Ask(ctx)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			var writer bytes.Buffer
			err := test.ask(ctx, &writer)

			require.Error(t, err)
			assert.Equal(t, test.expectedShowCursorCount, strings.Count(writer.String(), ansiShowCursor))
		})
	}
}
