// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package templates

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Absolute(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expected  string
		expectErr bool
		setupDir  bool // if true, create a temp dir and use its path as input
	}{
		{
			name:     "GitURI",
			input:    "git@github.com:Azure-Samples/my-template.git",
			expected: "git@github.com:Azure-Samples/my-template.git",
		},
		{
			name:     "GitProtocolURI",
			input:    "git://github.com/Azure-Samples/my-template.git",
			expected: "git://github.com/Azure-Samples/my-template.git",
		},
		{
			name:     "HttpsURL",
			input:    "https://github.com/Azure-Samples/my-template",
			expected: "https://github.com/Azure-Samples/my-template",
		},
		{
			name:     "HttpURL",
			input:    "http://github.com/Azure-Samples/my-template",
			expected: "http://github.com/Azure-Samples/my-template",
		},
		{
			name:     "AzureSamplesShorthand",
			input:    "my-template",
			expected: "https://github.com/Azure-Samples/my-template",
		},
		{
			name:     "OwnerRepo",
			input:    "owner/repo",
			expected: "https://github.com/owner/repo",
		},
		{
			name:     "AzureSamplesTrailingSlash",
			input:    "my-template/",
			expected: "https://github.com/Azure-Samples/my-template",
		},
		{
			name:      "InvalidMultiSlash",
			input:     "a/b/c",
			expectErr: true,
		},
		{
			name:     "LocalDirectoryAbsolute",
			setupDir: true,
			// input and expected are set dynamically in the test
		},
		{
			name:      "NonExistentPathFallsThrough",
			input:     "nonexistent-template",
			expected:  "https://github.com/Azure-Samples/nonexistent-template",
			expectErr: false,
		},
		{
			name:      "ExplicitRelativePathNotFound",
			input:     "./nonexistent-template",
			expectErr: true,
		},
		{
			name:      "ExplicitParentPathNotFound",
			input:     "../nonexistent-template",
			expectErr: true,
		},
		{
			name:     "SshURL",
			input:    "ssh://git@github.com/Azure-Samples/my-template.git",
			expected: "ssh://git@github.com/Azure-Samples/my-template.git",
		},
		{
			name:     "FileURL",
			input:    "file:///home/user/my-template",
			expected: "file:///home/user/my-template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := tt.input
			expected := tt.expected

			if tt.setupDir {
				dir := t.TempDir()
				input = dir
				expected = dir
			}

			result, err := Absolute(input)

			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, expected, result)
			}
		})
	}
}

func Test_Absolute_FileNotDirectory(t *testing.T) {
	// A regular file (not a directory) should NOT be treated as a local template path.
	// It should fall through to GitHub resolution logic.
	// We use a simple filename to verify it falls through (gets treated as Azure Samples shorthand).
	dir := t.TempDir()
	filePath := filepath.Join(dir, "myfile")
	require.NoError(t, os.WriteFile(filePath, []byte("content"), 0600))

	// Even though a file exists at this path, since it's not a directory,
	// Absolute() should fall through and NOT use it as a local template.
	// Using just the filename "myfile" to test this cleanly.
	result, err := Absolute("myfile")
	require.NoError(t, err)
	// Falls through to Azure Samples resolution
	require.Equal(t, "https://github.com/Azure-Samples/myfile", result)
}

func Test_Absolute_LocalRelativePath(t *testing.T) {
	// Create a temp dir and use a relative path to it
	dir := t.TempDir()
	subDir := filepath.Join(dir, "my-local-template")
	require.NoError(t, os.MkdirAll(subDir, 0755))

	// Resolve symlinks so the expected path matches what filepath.Abs returns.
	// On macOS, /var is a symlink to /private/var, causing mismatches otherwise.
	subDir, err := filepath.EvalSymlinks(subDir)
	require.NoError(t, err)

	// Change to parent directory so relative path works
	t.Chdir(dir)

	result, err := Absolute("my-local-template")
	require.NoError(t, err)
	require.Equal(t, subDir, result)
}

func Test_Absolute_RejectsSymlink(t *testing.T) {
	if os.Getenv("OS") == "Windows_NT" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}

	dir := t.TempDir()
	realDir := filepath.Join(dir, "real-template")
	require.NoError(t, os.MkdirAll(realDir, 0755))

	linkDir := filepath.Join(dir, "link-template")
	require.NoError(t, os.Symlink(realDir, linkDir))

	_, err := Absolute(linkDir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "symlink")
}

func Test_IsLocalPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"HttpsURL", "https://github.com/Azure-Samples/my-template", false},
		{"HttpURL", "http://github.com/Azure-Samples/my-template", false},
		{"GitURI", "git@github.com:Azure-Samples/my-template.git", false},
		{"GitProtocolURI", "git://github.com/Azure-Samples/my-template.git", false},
		{"SshURL", "ssh://git@github.com/Azure-Samples/my-template.git", false},
		{"FileURL", "file:///home/user/my-template", false},
		{"WindowsAbsPath", `C:\code\my-template`, true},
		{"UnixAbsPath", "/home/user/my-template", true},
		{"RelativePath", "my-template", true},
		// Edge cases: names starting with "git" or "http" that are local paths
		{"GitPrefixedLocalDir", "gitignore-templates", true},
		{"HttpPrefixedLocalDir", "http-server", true},
		{"GiftLocalDir", "gift/template", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsLocalPath(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}
