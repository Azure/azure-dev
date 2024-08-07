package templates

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAbsolute(t *testing.T) {
	// Determine the expected absolute path based on the current working directory
	cwd, err := os.Getwd()
	require.NoError(t, err)

	tests := []struct {
		input    string
		expected string
	}{
		// Valid Git URLs
		{"https://github.com/owner/repo", "https://github.com/owner/repo"},
		{"ssh://github.com/owner/repo", "ssh://github.com/owner/repo"},
		{"git://github.com/owner/repo", "git://github.com/owner/repo"},
		{"file:///path/to/repo", "file:///path/to/repo"},

		// Relative paths
		{"./local/repo", "file://" + filepath.Join(cwd, "local/repo")},
		{"../local/repo", "file://" + filepath.Join(cwd, "../local/repo")},

		// Absolute paths
		{filepath.Join(string(filepath.Separator), "absolute", "path", "to", "repo"), "file://" +
			filepath.Join(string(filepath.Separator), "absolute", "path", "to", "repo")},

		// GitHub formats
		{"repo", "https://github.com/Azure-Samples/repo"},
		{"owner/repo", "https://github.com/owner/repo"},

		// Invalid relative paths (without . prefix)
		{"some/owner/repo", ""},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			actual, err := Absolute(test.input)
			if test.expected == "" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, test.expected, actual)
			}
		})
	}
}

func TestIsGitURL(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"https://github.com/owner/repo", true},
		{"ssh://github.com/owner/repo", true},
		{"git://github.com/owner/repo", true},
		{"file:///path/to/repo", true},
		{"ftp://github.com/owner/repo", false},
		{"not-a-url", false},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			actual := isGitURL(test.input)
			require.Equal(t, test.expected, actual)
		})
	}
}

func TestIsRelativePath(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"./local/repo", true},
		{"../local/repo", true},
		{"local/repo", false},
		{"/absolute/path/to/repo", false},
		{"https://github.com/owner/repo", false},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			actual := isRelativePath(test.input)
			require.Equal(t, test.expected, actual)
		})
	}
}
