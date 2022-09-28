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
