// Package ostest contains test helpers for os package.
package ostest

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// Setenv sets the value of the environment variable named by the key.
// Any set values are automatically restored during test Cleanup.
func Setenv(t *testing.T, key string, value string) {
	orig, present := os.LookupEnv(key)
	os.Setenv(key, value)

	t.Cleanup(func() {
		if present {
			os.Setenv(key, orig)
		} else {
			os.Unsetenv(key)
		}
	})
}

// Unsetenv unsets the environment variable, which is later restored during test Cleanup.
func Unsetenv(t *testing.T, key string) {
	orig, present := os.LookupEnv(key)
	os.Unsetenv(key)

	t.Cleanup(func() {
		if present {
			os.Setenv(key, orig)
		}
	})
}

// Unsetenvs unsets the provided environment variables, which is later restored during test Cleanup.
func Unsetenvs(t *testing.T, keys []string) {
	restoreContext := map[string]string{}

	for _, key := range keys {
		orig, present := os.LookupEnv(key)
		if present {
			restoreContext[key] = orig
			os.Unsetenv(key)
		}
	}

	if len(restoreContext) > 0 {
		t.Cleanup(func() {
			for _, key := range keys {
				if restoreValue, present := restoreContext[key]; present {
					os.Setenv(key, restoreValue)
				}
			}
		})
	}
}

// Setenvs sets the provided environment variables keys with their corresponding values.
// Any set values are automatically restored during test Cleanup.
func Setenvs(t *testing.T, envContext map[string]string) {
	restoreContext := map[string]string{}
	for key, value := range envContext {
		orig, present := os.LookupEnv(key)
		if present {
			restoreContext[key] = orig
		}

		os.Setenv(key, value)
	}

	t.Cleanup(func() {
		for key := range envContext {
			if restoreValue, present := restoreContext[key]; present {
				os.Setenv(key, restoreValue)
			} else {
				os.Unsetenv(key)
			}
		}
	})
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
