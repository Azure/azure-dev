// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsLocalFilePath(t *testing.T) {
	tests := []struct {
		name     string
		fileID   string
		expected bool
	}{
		{
			name:     "LocalFilePathWithPrefix",
			fileID:   "local:/path/to/file.jsonl",
			expected: true,
		},
		{
			name:     "LocalFilePathWindowsStyle",
			fileID:   "local:C:\\Users\\test\\data.jsonl",
			expected: true,
		},
		{
			name:     "LocalFilePathRelative",
			fileID:   "local:./data/training.jsonl",
			expected: true,
		},
		{
			name:     "LocalPrefixOnly",
			fileID:   "local:",
			expected: true,
		},
		{
			name:     "RemoteFileID",
			fileID:   "file-abc123",
			expected: false,
		},
		{
			name:     "URLPath",
			fileID:   "https://example.com/file.jsonl",
			expected: false,
		},
		{
			name:     "EmptyString",
			fileID:   "",
			expected: false,
		},
		{
			name:     "LocalInMiddle",
			fileID:   "path/local:/file.jsonl",
			expected: false,
		},
		{
			name:     "UppercaseLocal",
			fileID:   "LOCAL:/path/to/file.jsonl",
			expected: false,
		},
		{
			name:     "MixedCaseLocal",
			fileID:   "Local:/path/to/file.jsonl",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsLocalFilePath(tt.fileID)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestGetLocalFilePath(t *testing.T) {
	tests := []struct {
		name     string
		fileID   string
		expected string
	}{
		{
			name:     "LocalFilePathUnix",
			fileID:   "local:/path/to/file.jsonl",
			expected: "/path/to/file.jsonl",
		},
		{
			name:     "LocalFilePathWindows",
			fileID:   "local:C:\\Users\\test\\data.jsonl",
			expected: "C:\\Users\\test\\data.jsonl",
		},
		{
			name:     "LocalFilePathRelative",
			fileID:   "local:./data/training.jsonl",
			expected: "./data/training.jsonl",
		},
		{
			name:     "LocalPrefixOnly",
			fileID:   "local:",
			expected: "",
		},
		{
			name:     "NoLocalPrefix",
			fileID:   "file-abc123",
			expected: "file-abc123",
		},
		{
			name:     "EmptyString",
			fileID:   "",
			expected: "",
		},
		{
			name:     "PathWithSpaces",
			fileID:   "local:/path/to/my file.jsonl",
			expected: "/path/to/my file.jsonl",
		},
		{
			name:     "PathWithSpecialChars",
			fileID:   "local:/path/to/file-2024_01_01.jsonl",
			expected: "/path/to/file-2024_01_01.jsonl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetLocalFilePath(tt.fileID)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestIsLocalFilePath_EdgeCases(t *testing.T) {
	t.Run("WhitespaceBeforeLocal", func(t *testing.T) {
		result := IsLocalFilePath(" local:/path/file.jsonl")
		require.False(t, result)
	})

	t.Run("LocalWithDoubleColon", func(t *testing.T) {
		result := IsLocalFilePath("local::/path/file.jsonl")
		require.True(t, result)
	})

	t.Run("JustLocalWord", func(t *testing.T) {
		result := IsLocalFilePath("local")
		require.False(t, result)
	})
}

func TestGetLocalFilePath_PreservesOriginalIfNotLocal(t *testing.T) {
	// When the path doesn't have local: prefix, it should return as-is
	testCases := []string{
		"file-abc123",
		"https://example.com/file.jsonl",
		"/absolute/path/file.jsonl",
		"relative/path/file.jsonl",
	}

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			result := GetLocalFilePath(tc)
			require.Equal(t, tc, result)
		})
	}
}
