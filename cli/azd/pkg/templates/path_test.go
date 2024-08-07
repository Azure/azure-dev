package templates

import (
	"os"
	"path/filepath"
	"runtime"
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
		{"./local/repo", "file://" + filepath.ToSlash(filepath.Join(cwd, "local/repo"))},
		{"../local/repo", "file://" + filepath.ToSlash(filepath.Join(cwd, "../local/repo"))},

		// Absolute paths
		{filepath.Join(string(filepath.Separator), "absolute", "path", "to", "repo"), "file://" +
			filepath.ToSlash(filepath.Join(string(filepath.Separator), "absolute", "path", "to", "repo"))},
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

	// Windows-specific tests
	if runtime.GOOS == "windows" {
		t.Run(`C:\absolute\path\to\repo`, func(t *testing.T) {
			input := `C:\absolute\path\to\repo`
			expected := `file:///C:/absolute/path/to/repo`
			actual, err := Absolute(input)
			require.NoError(t, err)
			require.Equal(t, expected, actual)
		})
	}
}
