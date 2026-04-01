// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewCursor(t *testing.T) {
	var buf bytes.Buffer
	c := NewCursor(&buf)
	require.NotNil(t, c)
}

func TestCursor_MoveCursorUp(t *testing.T) {
	tests := []struct {
		name     string
		lines    int
		expected string
	}{
		{"one line", 1, "\033[1A"},
		{"multiple lines", 5, "\033[5A"},
		{"ten lines", 10, "\033[10A"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			c := NewCursor(&buf)
			c.MoveCursorUp(tt.lines)
			require.Equal(t, tt.expected, buf.String())
		})
	}
}

func TestCursor_MoveCursorDown(t *testing.T) {
	tests := []struct {
		name     string
		lines    int
		expected string
	}{
		{"one line", 1, "\033[1B"},
		{"multiple lines", 3, "\033[3B"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			c := NewCursor(&buf)
			c.MoveCursorDown(tt.lines)
			require.Equal(t, tt.expected, buf.String())
		})
	}
}

func TestCursor_MoveCursorLeft(t *testing.T) {
	tests := []struct {
		name     string
		columns  int
		expected string
	}{
		{"one column", 1, "\033[1D"},
		{"multiple columns", 7, "\033[7D"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			c := NewCursor(&buf)
			c.MoveCursorLeft(tt.columns)
			require.Equal(t, tt.expected, buf.String())
		})
	}
}

func TestCursor_MoveCursorRight(t *testing.T) {
	tests := []struct {
		name     string
		columns  int
		expected string
	}{
		{"one column", 1, "\033[1C"},
		{"multiple columns", 4, "\033[4C"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			c := NewCursor(&buf)
			c.MoveCursorRight(tt.columns)
			require.Equal(t, tt.expected, buf.String())
		})
	}
}

func TestCursor_MoveCursorToStartOfLine(t *testing.T) {
	var buf bytes.Buffer
	c := NewCursor(&buf)
	c.MoveCursorToStartOfLine()
	require.Equal(t, "\r", buf.String())
}

func TestCursor_HideCursor(t *testing.T) {
	var buf bytes.Buffer
	c := NewCursor(&buf)
	c.HideCursor()
	require.Equal(t, "\033[?25l", buf.String())
}

func TestCursor_ShowCursor(t *testing.T) {
	var buf bytes.Buffer
	c := NewCursor(&buf)
	c.ShowCursor()
	require.Equal(t, "\033[?25h", buf.String())
}

func TestCursor_MultipleOperations(t *testing.T) {
	var buf bytes.Buffer
	c := NewCursor(&buf)

	c.MoveCursorUp(2)
	c.MoveCursorRight(5)
	c.MoveCursorDown(1)
	c.MoveCursorLeft(3)

	expected := "\033[2A\033[5C\033[1B\033[3D"
	require.Equal(t, expected, buf.String())
}

func TestCursor_WritesExpectedANSIFormat(t *testing.T) {
	// Verify all directional moves use the standard ANSI format: ESC[{n}{code}
	tests := []struct {
		name   string
		action func(c Cursor)
		format string
		arg    int
	}{
		{"up", func(c Cursor) { c.MoveCursorUp(3) }, "\033[%dA", 3},
		{"down", func(c Cursor) { c.MoveCursorDown(3) }, "\033[%dB", 3},
		{"right", func(c Cursor) { c.MoveCursorRight(3) }, "\033[%dC", 3},
		{"left", func(c Cursor) { c.MoveCursorLeft(3) }, "\033[%dD", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			c := NewCursor(&buf)
			tt.action(c)
			require.Equal(t, fmt.Sprintf(tt.format, tt.arg), buf.String())
		})
	}
}

func TestInput_ResetValue(t *testing.T) {
	var buf bytes.Buffer
	input := &Input{
		cursor: NewCursor(&buf),
		value:  []rune("hello world"),
	}

	require.Equal(t, []rune("hello world"), input.value)
	input.ResetValue()
	require.Empty(t, input.value)
}

func TestInput_ResetValueAlreadyEmpty(t *testing.T) {
	var buf bytes.Buffer
	input := &Input{
		cursor: NewCursor(&buf),
		value:  []rune{},
	}

	input.ResetValue()
	require.Empty(t, input.value)
}

func TestInput_ResetValueUnicode(t *testing.T) {
	var buf bytes.Buffer
	input := &Input{
		cursor: NewCursor(&buf),
		value:  []rune("こんにちは🌍"),
	}

	require.Len(t, input.value, 6) // 5 Japanese chars + 1 emoji
	input.ResetValue()
	require.Empty(t, input.value)
}
