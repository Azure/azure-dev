package functional

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	osexec "os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/sethvargo/go-retry"
	"go.opentelemetry.io/otel/attribute"
)

func NewRandomNameEnvAndInitResponse(t *testing.T) (string, io.Reader) {
	return NewRandomNameEnvAndInitResponseWithPrefix(t, "")
}

func NewRandomNameEnvAndInitResponseWithPrefix(t *testing.T, prefix string) (string, io.Reader) {
	envName := randomEnvName()
	envName = prefix + envName
	interactionString := fmt.Sprintf("%s\n", envName) + // "enter environment name"
		"\n" + // "choose location" (we're choosing the default)
		"\n" // "choose subscription" (we're choosing the default)

	t.Logf("environment name: %s", envName)

	return envName, bytes.NewBufferString(interactionString)
}

func randomEnvName() string {
	bytes := make([]byte, 4)
	_, err := rand.Read(bytes)
	if err != nil {
		panic(fmt.Errorf("could not read random bytes: %w", err))
	}

	// Adding first letter initial of the OS for CI identification
	osName := os.Getenv("AZURE_DEV_CI_OS")
	if osName == "" {
		osName = runtime.GOOS
	}
	osInitial := osName[:1]

	return ("azdtest-" + osInitial + hex.EncodeToString(bytes))[0:15]
}

func getAzdLocation() string {
	_, b, _, _ := runtime.Caller(0)

	if runtime.GOOS == "windows" {
		return filepath.Join(filepath.Dir(b), "..", "..", "azd.exe")
	} else {
		return filepath.Join(filepath.Dir(b), "..", "..", "azd")
	}
}

//go:embed testdata/samples/*
var samples embed.FS

// CopySample copies the given sample to targetRoot.
func CopySample(targetRoot string, sampleName string) error {
	sampleRoot := path.Join("testdata", "samples", sampleName)

	return fs.WalkDir(samples, sampleRoot, func(name string, d fs.DirEntry, err error) error {
		// If there was some error that was preventing is from walking into the directory, just fail now,
		// not much we can do to recover.
		if err != nil {
			return err
		}
		targetPath := filepath.Join(targetRoot, name[len(sampleRoot):])

		if d.IsDir() {
			return os.MkdirAll(targetPath, osutil.PermissionDirectory)
		}

		contents, err := fs.ReadFile(samples, name)
		if err != nil {
			return fmt.Errorf("reading sample file: %w", err)
		}
		return os.WriteFile(targetPath, contents, osutil.PermissionFile)
	})
}

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
				t.Fatalf("functional.TempDirWithDiagnostics: %s", err)
			}
		})
	}

	t.Logf("tempDir: %s", temp)

	return temp
}

// NewTestContext returns a new empty context, suitable for use in tests. If a
// the provided `testing.T` has a deadline applied, the returned context
// respects the deadline.
func NewTestContext(t *testing.T) (context.Context, context.CancelFunc) {
	ctx := context.Background()

	if deadline, ok := t.Deadline(); ok {
		return context.WithDeadline(ctx, deadline)
	}

	return context.WithCancel(ctx)
}

func logHandles(t *testing.T, path string) {
	handle, err := osexec.LookPath("handle")
	if err != nil && errors.Is(err, osexec.ErrNotFound) {
		t.Logf("handle.exe not present. Skipping handle detection. PATH: %s", os.Getenv("PATH"))
		return
	}

	if err != nil {
		t.Logf("failed to find handle.exe: %s", err)
		return
	}

	args := exec.NewRunArgs(handle, path, "-nobanner")
	cmd := exec.NewCommandRunner(os.Stdin, os.Stdout, os.Stderr)
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
	return retry.Do(context.Background(), retry.WithMaxRetries(10, retry.NewConstant(1*time.Second)),
		func(_ context.Context) error {
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

func RunCliCommand(t *testing.T, ctx context.Context, args ...string) (stdout string, stderr string, err error) {
	inBuf := &bytes.Buffer{}

	return RunCliCommandWithStdIn(t, ctx, inBuf, args...)
}

func RunCliCommandWithStdIn(
	t *testing.T,
	ctx context.Context,
	in io.Reader,
	args ...string) (stdout string, stderr string, err error) {

	root := cmd.NewRootCmd()
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	root.SetIn(in)
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs(args)

	t.Logf("running azd %s", strings.Join(args, " "))

	err = root.ExecuteContext(ctx)
	stdout = outBuf.String()
	stderr = errBuf.String()
	return
}
