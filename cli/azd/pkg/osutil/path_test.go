// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ResolveContainedPath(t *testing.T) {
	// Use t.TempDir() for unique per-test roots that auto-cleanup
	root1 := t.TempDir()
	root2 := t.TempDir()
	err := os.MkdirAll(filepath.Join(root1, "subdir"), 0755)
	require.NoError(t, err)

	// Create test files
	file1 := filepath.Join(root1, "file.txt")
	err = os.WriteFile(file1, []byte("hello"), 0600)
	require.NoError(t, err)

	file2 := filepath.Join(root2, "other.txt")
	err = os.WriteFile(file2, []byte("world"), 0600)
	require.NoError(t, err)

	subFile := filepath.Join(root1, "subdir", "nested.txt")
	err = os.WriteFile(subFile, []byte("nested"), 0600)
	require.NoError(t, err)

	tests := []struct {
		name      string
		roots     []string
		filePath  string
		wantPath  string // empty means expect error
		wantErr   bool
		errSubstr string
	}{
		{
			name:     "valid file in first root",
			roots:    []string{root1, root2},
			filePath: "file.txt",
			wantPath: file1,
			wantErr:  false,
		},
		{
			name:     "valid file in second root",
			roots:    []string{root1, root2},
			filePath: "other.txt",
			wantPath: file2,
			wantErr:  false,
		},
		{
			name:     "nested file in subdirectory",
			roots:    []string{root1},
			filePath: filepath.Join("subdir", "nested.txt"),
			wantPath: subFile,
			wantErr:  false,
		},
		{
			name:      "path traversal attempt",
			roots:     []string{root1},
			filePath:  "../../../etc/passwd",
			wantErr:   true,
			errSubstr: "resolves outside all root directories",
		},
		{
			name:      "empty roots list",
			roots:     []string{},
			filePath:  "file.txt",
			wantErr:   true,
			errSubstr: "was not found",
		},
		{
			name:      "file not found in any root",
			roots:     []string{root1, root2},
			filePath:  "nonexistent.txt",
			wantErr:   true,
			errSubstr: "was not found",
		},
		{
			name:      "backslash path traversal",
			roots:     []string{root1},
			filePath:  `..\..\evil`,
			wantErr:   true,
			errSubstr: "resolves outside all root directories",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolveContainedPath(tt.roots, tt.filePath)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errSubstr != "" {
					require.Contains(t, err.Error(), tt.errSubstr)
				}
				require.Empty(t, result)
			} else {
				require.NoError(t, err)
				// Clean both paths for comparison to handle OS-specific separators
				require.Equal(t, filepath.Clean(tt.wantPath), filepath.Clean(result))
			}
		})
	}
}

func Test_IsPathContained(t *testing.T) {
	base := filepath.Join(os.TempDir(), "testbase")
	// Construct OS-appropriate filesystem root for root path tests
	root := filepath.VolumeName(os.TempDir()) + string(os.PathSeparator)

	tests := []struct {
		name     string
		base     string
		target   string
		expected bool
	}{
		{
			name:     "child path is contained",
			base:     base,
			target:   filepath.Join(base, "child", "file.txt"),
			expected: true,
		},
		{
			name:     "exact base path is contained",
			base:     base,
			target:   base,
			expected: true,
		},
		{
			name:     "traversal escapes base",
			base:     base,
			target:   filepath.Join(base, "..", "escape"),
			expected: false,
		},
		{
			name:     "sibling directory not contained",
			base:     base,
			target:   base + "-sibling",
			expected: false,
		},
		{
			name:     "double traversal escapes base",
			base:     base,
			target:   filepath.Join(base, "sub", "..", "..", "escape"),
			expected: false,
		},
		{
			name:     "backslash traversal escapes base on all platforms",
			base:     base,
			target:   base + `\..\..\escape`,
			expected: false,
		},
		{
			name:     "root path contains child",
			base:     root,
			target:   filepath.Join(root, "somechild"),
			expected: true,
		},
		{
			name:     "root path exact match",
			base:     root,
			target:   root,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsPathContained(tt.base, tt.target)
			require.Equal(t, tt.expected, result)
		})
	}
}
