// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetFileNameWithoutExt(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		expected string
	}{
		{
			name:     "Windows executable",
			filePath: "microsoft-azd-extensions-windows-amd64.exe",
			expected: "microsoft-azd-extensions-windows-amd64",
		},
		{
			name:     "Linux tar.gz",
			filePath: "microsoft-azd-extensions-linux-amd64.tar.gz",
			expected: "microsoft-azd-extensions-linux-amd64",
		},
		{
			name:     "Linux arm64 tar.gz",
			filePath: "microsoft-azd-extensions-linux-arm64.tar.gz",
			expected: "microsoft-azd-extensions-linux-arm64",
		},
		{
			name:     "Darwin zip",
			filePath: "microsoft-azd-extensions-darwin-amd64.zip",
			expected: "microsoft-azd-extensions-darwin-amd64",
		},
		{
			name:     "File with path",
			filePath: "/path/to/microsoft-azd-extensions-linux-amd64.tar.gz",
			expected: "microsoft-azd-extensions-linux-amd64",
		},
		{
			name:     "No extension",
			filePath: "binary-file",
			expected: "binary-file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getFileNameWithoutExt(tt.filePath)
			require.Equal(t, tt.expected, result)
		})
	}
}
