// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package python

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVenvNameForDir(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		expected string
	}{
		{
			name:     "SimpleName",
			dir:      filepath.Join("path", "to", "myproject"),
			expected: "myproject_env",
		},
		{
			name: "TrailingPathSeparator",
			dir: filepath.Join(
				"path", "to", "myproject",
			) + string(os.PathSeparator),
			expected: "myproject_env",
		},
		{
			name:     "LeadingAndTrailingSpaces",
			dir:      "  myproject  ",
			expected: "myproject_env",
		},
		{
			name:     "SingleSegment",
			dir:      "api",
			expected: "api_env",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := VenvNameForDir(tt.dir)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestVenvPythonPath(t *testing.T) {
	venvDir := filepath.Join("project", ".venv")
	result := VenvPythonPath(venvDir)

	if runtime.GOOS == "windows" {
		expected := filepath.Join(
			venvDir, "Scripts", "python.exe",
		)
		require.Equal(t, expected, result)
	} else {
		expected := filepath.Join(
			venvDir, "bin", "python",
		)
		require.Equal(t, expected, result)
	}
}

func TestVenvActivateCmd(t *testing.T) {
	venvDir := filepath.Join("project", ".venv")
	result := VenvActivateCmd(venvDir)

	if runtime.GOOS == "windows" {
		expected := filepath.Join(
			venvDir, "Scripts", "activate",
		)
		require.Equal(t, expected, result)
	} else {
		expected := ". " + filepath.Join(
			venvDir, "bin", "activate",
		)
		require.Equal(t, expected, result)
	}
}
