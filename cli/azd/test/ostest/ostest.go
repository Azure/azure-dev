// Package ostest contains test helpers for os package.
package ostest

import (
	"os"
	"testing"
)

// SetTempEnv sets the value of the environment variable named by the key.
// Any set values are automatically restored during test Cleanup.
func SetTempEnv(t *testing.T, key string, value string) {
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

// UnsetTempEnv unsets the environment variable, which is later restored during test Cleanup.
func UnsetTempEnv(t *testing.T, key string) {
	orig, present := os.LookupEnv(key)
	os.Unsetenv(key)

	t.Cleanup(func() {
		if present {
			os.Setenv(key, orig)
		}
	})
}

// SetTempEnvs sets the provided environment variables keys with their corresponding values.
// Any set values are automatically restored during test Cleanup.
func SetTempEnvs(t *testing.T, envContext map[string]string) {
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
