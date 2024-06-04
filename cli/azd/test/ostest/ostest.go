package ostest

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Setenv calls t.Setenv with the provided key and value.
func Setenv(t *testing.T, key string, value string) {
	t.Setenv(key, value)
}

// Unsetenv unsets the environment variable, which is later restored during test Cleanup.
func Unsetenv(t *testing.T, key string) {
	// Call t.Setenv here so that if this test is being run in parallel we will fail.
	t.Setenv(key, "")
	// No need to for an explicit t.Cleanup, the call to Setenv above registered a Cleanup function
	// that will restore the value it had before Unsetenv was called.
	os.Unsetenv(key)
}

// Unsetenvs unsets the provided environment variables, which is later restored during test Cleanup.
func Unsetenvs(t *testing.T, keys []string) {
	for _, key := range keys {
		Unsetenv(t, key)
	}
}

// Setenvs sets the provided environment variables keys with their corresponding values.
// Any set values are automatically restored during test Cleanup.
func Setenvs(t *testing.T, envContext map[string]string) {
	for key, value := range envContext {
		t.Setenv(key, value)
	}
}

// Create creates or truncates the named file. If the file already exists,
// it is truncated. If the file does not exist, it is created with mode 0666
// (before umask).
// Files created are automatically removed during test Cleanup. Ignores errors
// due to the file already being deleted.
func Create(t *testing.T, name string) {
	CreateNoCleanup(t, name)

	t.Cleanup(func() {
		err := os.Remove(name)
		if !errors.Is(err, os.ErrNotExist) {
			require.NoError(t, err)
		}
	})
}

// Create creates or truncates the named file. If the file already exists,
// it is truncated. If the file does not exist, it is created with mode 0666
// (before umask).
func CreateNoCleanup(t *testing.T, name string) {
	f, err := os.Create(name)
	require.NoError(t, err)
	defer f.Close()
}

// Chdir changes the current working directory to the named directory.
// The working directory is automatically restored as part of Cleanup.
func Chdir(t *testing.T, dir string) {
	wd, err := os.Getwd()
	require.NoError(t, err)

	err = os.Chdir(dir)
	require.NoError(t, err)

	t.Cleanup(func() {
		err = os.Chdir(wd)
		require.NoError(t, err)
	})
}

// CombinedPaths collects all PATH strings from the provided environment variables, in the form of 'PATH=<path>',
// and combines them into a single PATH string, also in the form of 'PATH=<collected paths>'.
// It returns an empty string if no PATH strings are found.
func CombinedPaths(environ []string) string {
	pathStrings := []string{}
	for _, env := range environ {
		if strings.HasPrefix(env, "PATH=") {
			pathStrings = append(pathStrings, env[5:])
		}
	}

	if len(pathStrings) == 0 {
		return ""
	}

	return "PATH=" + strings.Join(pathStrings, string(os.PathListSeparator))
}
