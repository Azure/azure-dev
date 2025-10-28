// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"os"
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
			expectedPlain: "Example Site (https://example.com)",
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
