package ostest

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
)

// TempDirWithDiagnostics creates a temp directory with cleanup that also provides additional
// diagnostic logging and retries.
func TempDirWithDiagnostics(t *testing.T) string {
	temp := t.TempDir()

	t.Cleanup(func() {
		err := removeAllWithDiagnostics(t, temp)
		if err != nil && errors.Is(err, os.ErrNotExist) {
			t.Fatalf("TempDirWithDiagnostics: %s", err)
		}
	})

	return temp
}

// removeAllWithDiagnostics performs os.RemoveAll with additional
// diagnostic logging and retries.
func removeAllWithDiagnostics(t *testing.T, path string) error {
	if runtime.GOOS != "windows" {
		return nil
	}

	err := removeAll(path)
	if err != nil {
		logHandles(t, path)
	}

	return err
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

	t.Logf("handle.exe output\n:%s%s\n", rr.Stdout, rr.Stderr)
}

func removeAll(path string) error {
	const arbitraryTimeout = 2 * time.Second
	var (
		start     time.Time
		nextSleep = 1 * time.Millisecond
	)
	for {
		err := os.RemoveAll(path)

		if start.IsZero() {
			start = time.Now()
		} else if d := time.Since(start) + nextSleep; d >= arbitraryTimeout {
			return fmt.Errorf("timeout: %w", err)
		}
		time.Sleep(nextSleep)
		nextSleep += time.Duration(rand.Int63n(int64(nextSleep)))
	}
}
