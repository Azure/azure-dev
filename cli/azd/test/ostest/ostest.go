package ostest

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/sethvargo/go-retry"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

// TempDirWithDiagnostics creates a temp directory with cleanup that also provides additional
// diagnostic logging and retries.
func TempDirWithDiagnostics(t *testing.T) string {
	temp := t.TempDir()

	if runtime.GOOS == "windows" {
		// Enable our additional custom remove logic for Windows where we see locked files.
		t.Cleanup(func() {
			err := removeAllWithDiagnostics(t, temp)
			if err != nil {
				logHandles(t, temp)
				t.Fatalf("TempDirWithDiagnostics: %s", err)
			}
		})
	}

	return temp
}

func logHandles(t *testing.T, path string) {
	handle, err := exec.LookPath("handle")
	if err != nil && errors.Is(err, exec.ErrNotFound) {
		t.Logf("handle.exe not present. Skipping handle detection. PATH: %s", os.Getenv("PATH"))
		return
	}

	if err != nil {
		t.Logf("failed to find handle.exe: %s", err)
		return
	}

	args := azdexec.NewRunArgs(handle, path, "-nobanner")
	cmd := azdexec.NewCommandRunner()
	rr, err := cmd.Run(context.Background(), args)
	if err != nil {
		t.Logf("handle.exe failed. stdout: %s, stderr: %s\n", rr.Stdout, rr.Stderr)
		return
	}

	t.Logf("handle.exe output:\n%s\n", rr.Stdout)

	// Ensure telemetry is initialized since we're running in a CI environment
	_ = telemetry.GetTelemetrySystem()

	// Log this to telemetry for ease of correlation
	tracer := telemetry.GetTracer()
	_, span := tracer.Start(context.Background(), "test.file_cleanup_failure")
	span.SetAttributes(attribute.String("handle.stdout", rr.Stdout))
	span.SetAttributes(attribute.String("ci.build.number", os.Getenv("BUILD_BUILDNUMBER")))
	span.End()
}

func removeAllWithDiagnostics(t *testing.T, path string) error {
	retryCount := 0
	loggedOnce := false
	return retry.Do(context.Background(), retry.WithMaxRetries(10, retry.NewConstant(1*time.Second)), func(_ context.Context) error {
		removeErr := os.RemoveAll(path)
		if removeErr == nil {
			return nil
		}
		t.Logf("failed to clean up %s with error: %v", path, removeErr)

		if retryCount >= 2 && !loggedOnce {
			// Only log once after 2 seconds - logHandles is pretty expensive and slow
			logHandles(t, path)
			loggedOnce = true
		}

		retryCount++
		return retry.RetryableError(removeErr)
	})
}

// Setenv sets the value of the environment variable named by the key.
// Any set values are automatically restored during test Cleanup.
func Setenv(t *testing.T, key string, value string) {
	t.Setenv(key, value)
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
