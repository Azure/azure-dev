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
	scheme := "file://"
	if runtime.GOOS == "windows" {
		scheme += "/"
	}
	tests := []struct {
		input    string
		expected string
	}{
		// Valid Git URLs
		{"https://github.com/owner/repo", "https://github.com/owner/repo"},
		{"http://github.com/owner/repo", "http://github.com/owner/repo"},
		{"ssh://github.com/owner/repo", "ssh://github.com/owner/repo"},
		{"git://github.com/owner/repo", "git://github.com/owner/repo"},
		{"file:///path/to/repo", "file:///path/to/repo"},

		// Relative paths
		{"./local/repo", scheme + filepath.ToSlash(filepath.Join(cwd, "local/repo"))},
		{"../local/repo", scheme + filepath.ToSlash(filepath.Join(cwd, "../local/repo"))},

		// GitHub formats
		{"repo", "https://github.com/Azure-Samples/repo"},
		{"owner/repo", "https://github.com/owner/repo"},
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

	// POSIX-specific tests
	if runtime.GOOS != "windows" {
		t.Run("/absolute/path/to/repo", func(t *testing.T) {
			input := "/absolute/path/to/repo"
			expected := "file:///absolute/path/to/repo"
			actual, err := Absolute(input)
			require.NoError(t, err)
			require.Equal(t, expected, actual)
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

func TestIsGitURL(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		// Valid Git URLs
		{"https://github.com/owner/repo", true},
		{"http://github.com/owner/repo", true},
		{"ssh://github.com/owner/repo", true},
		{"git://github.com/owner/repo", true},
		{"file:///path/to/repo", true},

		// Invalid URLs
		{"ftp://github.com/owner/repo", false},
		{"mailto://owner@github.com", false},
		{"data:text/plain;base64,SGVsbG8sIFdvcmxkIQ==", false},
		{"", false},

		// Valid URLs without schemes
		{"github.com/owner/repo", false},
		{"owner/repo", false},
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
		{"/absolute/path/to/repo", false},
		{"C:\\absolute\\path\\to\\repo", false},
		{"relative/path/to/repo", false},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			actual := isRelativePath(test.input)
			require.Equal(t, test.expected, actual)
		})
	}
}

func TestIsAbsolutePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Run("Windows", func(t *testing.T) {
			tests := []struct {
				input    string
				expected bool
			}{
				{"C:\\absolute\\path\\to\\repo", true},
				{"C:/absolute/path/to/repo", true},
				{"./relative/path/to/repo", false},
				{"../relative/path/to/repo", false},
			}

			for _, test := range tests {
				t.Run(test.input, func(t *testing.T) {
					actual := isAbsolutePath(test.input)
					require.Equal(t, test.expected, actual)
				})
			}
		})
	} else {
		t.Run("POSIX", func(t *testing.T) {
			tests := []struct {
				input    string
				expected bool
			}{
				{"/absolute/path/to/repo", true},
				{"./relative/path/to/repo", false},
				{"../relative/path/to/repo", false},
			}

			for _, test := range tests {
				t.Run(test.input, func(t *testing.T) {
					actual := isAbsolutePath(test.input)
					require.Equal(t, test.expected, actual)
				})
			}
		})
	}
}

func TestIsWindowsPath(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"C:\\absolute\\path\\to\\repo", true},
		{"D:/absolute/path/to/repo", true},
		{"/absolute/path/to/repo", false},
		{"relative/path/to/repo", false},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			actual := isWindowsPath(test.input)
			require.Equal(t, test.expected, actual)
		})
	}
}
