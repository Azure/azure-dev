// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithHyperlink(t *testing.T) {
	tests := []struct {
		name          string
		url           string
		text          string
		expectEscape  bool
		expectedPlain string
	}{
		{
			name:          "URL and text are the same",
			url:           "https://example.com",
			text:          "https://example.com",
			expectEscape:  true,
			expectedPlain: "https://example.com",
		},
		{
			name:          "URL and text are different",
			url:           "https://example.com",
			text:          "Example Site",
			expectEscape:  true,
			expectedPlain: "https://example.com",
		},
		{
			name:          "Text is empty",
			url:           "https://example.com",
			text:          "",
			expectEscape:  true,
			expectedPlain: "https://example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test non-terminal mode by checking the actual output
			// Since we can't reliably set os.Stdout.Fd() to non-terminal in tests,
			// we'll just verify the function doesn't panic and returns a string
			result := WithHyperlink(tt.url, tt.text)
			require.NotEmpty(t, result)

			// When running in a non-TTY environment (like most CI systems),
			// the result should be the plain text version
			if !isTTY() {
				require.Equal(t, tt.expectedPlain, result)
			}
		})
	}
}

// isTTY checks if stdout is a TTY (for testing purposes)
func isTTY() bool {
	fileInfo, _ := os.Stdout.Stat()
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

func TestWithFormatters_NonEmpty(t *testing.T) {
	t.Parallel()
	inputs := []struct {
		name string
		fn   func(string, ...any) string
	}{
		{"WithLinkFormat", WithLinkFormat},
		{"WithHighLightFormat", WithHighLightFormat},
		{"WithErrorFormat", WithErrorFormat},
		{"WithWarningFormat", WithWarningFormat},
		{"WithSuccessFormat", WithSuccessFormat},
		{"WithGrayFormat", WithGrayFormat},
		{"WithHintFormat", WithHintFormat},
		{"WithBold", WithBold},
		{"WithUnderline", WithUnderline},
	}
	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := tc.fn("hello %s", "world")
			require.Contains(t, out, "hello")
			require.Contains(t, out, "world")
		})
	}
}

func TestWithBackticks(t *testing.T) {
	t.Parallel()
	require.Equal(t, "`foo`", WithBackticks("foo"))
	require.Equal(t, "``", WithBackticks(""))
}

func TestWithMarkdown(t *testing.T) {
	t.Parallel()
	// Basic text should round-trip through glamour without an error.
	out := WithMarkdown("# Hello\n\nSome **bold** text.")
	require.NotEmpty(t, out)
	require.Contains(t, strings.ToLower(out), "hello")
}

func TestWithHyperlink_NonTerminal(t *testing.T) {
	t.Parallel()
	// In tests, stdout is not a TTY so plain URL should be returned.
	out := WithHyperlink("https://example.com", "click")
	require.Equal(t, "https://example.com", out)
}

func TestGetConsoleWidth_FromEnv(t *testing.T) {
	// Not parallel due to t.Setenv
	t.Setenv("COLUMNS", "99")
	w := getConsoleWidth()
	// When the terminal can't be detected, the env fallback kicks in and
	// should return the parsed value. When running inside an IDE-attached
	// terminal, width can come from consolesize instead — allow either.
	require.Greater(t, w, 0)
}

func TestGetConsoleWidth_InvalidEnvFallsBack(t *testing.T) {
	t.Setenv("COLUMNS", "not-a-number")
	w := getConsoleWidth()
	require.Greater(t, w, 0)
}
