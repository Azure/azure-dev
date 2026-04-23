// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package logging

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "EmptyString",
			input:    "",
			maxLen:   10,
			expected: "",
		},
		{
			name:     "ShorterThanMax",
			input:    "hello",
			maxLen:   10,
			expected: "hello",
		},
		{
			name:     "ExactlyMaxLen",
			input:    "hello",
			maxLen:   5,
			expected: "hello",
		},
		{
			name:     "LongerThanMax",
			input:    "hello world",
			maxLen:   8,
			expected: "hello...",
		},
		{
			name:     "MinTruncation",
			input:    "abcdef",
			maxLen:   4,
			expected: "a...",
		},
		{
			name:     "SingleCharOverflow",
			input:    "abcdef",
			maxLen:   5,
			expected: "ab...",
		},
		{
			name:     "LongString",
			input:    "the quick brown fox jumps over the lazy dog",
			maxLen:   20,
			expected: "the quick brown f...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TruncateString(tt.input, tt.maxLen)
			require.Equal(t, tt.expected, result)
			require.LessOrEqual(t, len(result), tt.maxLen)
		})
	}
}
