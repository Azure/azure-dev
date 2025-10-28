// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"os"
	"testing"
)

func TestHyperlink(t *testing.T) {
	tests := []struct {
		name          string
		url           string
		text          []string
		expectEscape  bool
		expectedPlain string
	}{
		{
			name:          "URL only",
			url:           "https://example.com",
			text:          nil,
			expectEscape:  true,
			expectedPlain: "https://example.com",
		},
		{
			name:          "URL and text are the same",
			url:           "https://example.com",
			text:          []string{"https://example.com"},
			expectEscape:  true,
			expectedPlain: "https://example.com",
		},
		{
			name:          "URL and text are different",
			url:           "https://example.com",
			text:          []string{"Example Site"},
			expectEscape:  true,
			expectedPlain: "Example Site (https://example.com)",
		},
		{
			name:          "Text is empty string",
			url:           "https://example.com",
			text:          []string{""},
			expectEscape:  true,
			expectedPlain: "https://example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test non-terminal mode by checking the actual output
			result := Hyperlink(tt.url, tt.text...)
			if len(result) == 0 {
				t.Errorf("expected non-empty result")
			}

			// When running in a non-TTY environment (like most CI systems),
			// the result should be the plain text version
			if !isTTY() {
				if result != tt.expectedPlain {
					t.Errorf("expected %q, got %q", tt.expectedPlain, result)
				}
			}
		})
	}
}

// isTTY checks if stdout is a TTY (for testing purposes)
func isTTY() bool {
	fileInfo, _ := os.Stdout.Stat()
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

func Test_CountLineBreaks(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		width    int
		expected int
	}{
		// Basic cases
		{"Empty string", "", 0, 0},
		{"New Line", "\n", 1, 1},
		{"Short Text", "Hello World", 100, 0},
		{"Multiple Lines", "Hello\nWorld", 100, 1},

		// Edge cases
		{"Multiple Consecutive Newlines", "\n\n\n", 100, 3},
		{"String Ending with Newline", "Hello\n", 100, 1}, // Still counts newline, but no extra
		{"String Starting with Newline", "\nHello", 100, 1},
		{"String with Spaces and Newlines", "   \n   ", 100, 1},

		// Wrapping cases
		{"Exact Width", "1234567890", 10, 0},          // Should not wrap
		{"Slightly Over Width", "12345678901", 10, 1}, // Wraps once
		{"Long Line Wrapping", "This is a very long line that should be wrapped into multiple lines when printed.", 50, 1},
		{"Mixed Short and Long Lines", "Short\nThis is a very long line that wraps.\nAnother short one", 30, 3},

		// Unicode & special characters
		{"Emoji Characters", "🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥", 10, 0},             // Should be 1 line
		{"Emoji Line Wrap", "🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥", 10, 1},             // Should wrap to 2 lines
		{"Mixing Emoji and Text", "🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥 Hello!", 10, 1}, // Wraps text correctly

		// Trailing newlines shouldn't overcount
		{"Two Printf calls (simulated)", "line 1\nline 2\n", 100, 2}, // Should be exactly 2 lines
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := CountLineBreaks(tc.input, tc.width)
			if result != tc.expected {
				t.Errorf("expected %d, got %d", tc.expected, result)
			}
		})
	}
}

func Test_VisibleLength(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected int
	}{
		// Basic cases
		{"Empty String", "", 0},
		{"Plain Text", "Hello World", 11},
		{"Multiple Spaces", "Hello   World", 13},

		// ANSI escape sequence cases
		{"ANSI Color Code", "\x1b[31mHello\x1b[0m", 5},
		{"Multiple ANSI Codes", "\x1b[31mHello\x1b[0m \x1b[32mWorld\x1b[0m", 11},
		{"Mixed ANSI + Spaces", "\x1b[31mHello\x1b[0m   World", 13},
		{"Only ANSI Codes", "\x1b[31m\x1b[0m", 0},
		{"Non-Color ANSI Sequences", "\x1b[1mBold\x1b[22m", 4},
		{"Long ANSI Sequence", "\x1b[38;5;82mGreen Text\x1b[0m", 10},

		// Unicode & special characters
		{"Unicode Characters", "🔥🔥🔥", 3},
		{"Mix of ANSI and Unicode", "\x1b[31m🔥🔥🔥\x1b[0m", 3},

		// Edge Cases
		{"Edge Case: Leading ANSI Code", "\x1b[31mRed\x1b[0mText", 7},
		{"Edge Case: Trailing ANSI Code", "Text\x1b[31mRed\x1b[0m", 7},
		{"Edge Case: ANSI Wrapping Empty String", "\x1b[31m\x1b[0m", 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := VisibleLength(tc.input)
			if result != tc.expected {
				t.Errorf("expected %d, got %d", tc.expected, result)
			}
		})
	}
}
