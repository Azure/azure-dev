// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package proctest starts real, long-lived processes whose executable image lives in a
// directory the test chooses.
//
// Process and file-lock behavior cannot be faked meaningfully. Windows refuses to delete
// the image file of a running executable, process enumeration reads live operating system
// tables, and termination is observed by the kernel rather than by a mock. Tests covering
// any of that need a genuine process running from a genuine path.
//
// The technique used throughout is to copy the running test binary into the target
// directory and re-execute it in a helper mode, which yields a real executable at an
// arbitrary path without needing a build step or a checked-in fixture binary.
package proctest

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ModeEnvVar names the environment variable that puts a re-executed test binary into
// helper mode instead of running tests.
const ModeEnvVar = "AZD_TEST_PROC_MODE"

// Mode selects what a helper does instead of running tests.
type Mode string

// ModeIdle keeps the helper alive and otherwise does nothing.
const ModeIdle Mode = "idle"

const (
	// Lifetime bounds a helper that somehow outlives its test, so a leaked process cannot
	// linger on a developer machine or a build agent.
	Lifetime = 10 * time.Minute

	// exitTimeout bounds how long a test waits to observe an exit the operating system
	// has already performed.
	exitTimeout = 10 * time.Second

	// pollInterval is how often a wait re-checks its condition.
	pollInterval = 100 * time.Millisecond

	// executablePermissions makes the copied binary runnable on Unix. Windows ignores it.
	executablePermissions = 0755
)

// RequestedMode returns the helper mode this process was started in, or an empty Mode
// during an ordinary test run.
func RequestedMode() Mode {
	return Mode(os.Getenv(ModeEnvVar))
}

// RunRequestedHelper runs the built-in behavior for the requested mode, reporting whether
// it handled the request. A TestMain that only needs idle helpers can delegate entirely:
//
//	func TestMain(m *testing.M) {
//		if proctest.RunRequestedHelper() {
//			return
//		}
//
//		os.Exit(m.Run())
//	}
//
// A package needing its own helper behavior inspects RequestedMode first and falls through
// to this function for the shared modes.
func RunRequestedHelper() bool {
	if RequestedMode() != ModeIdle {
		return false
	}

	Idle()

	return true
}

// Idle keeps the current process alive without doing anything.
//
// It sleeps rather than blocking forever on an empty select, because the Go runtime treats
// a permanently blocked program as a deadlock and would abort the helper rather than keep
// it running.
func Idle() {
	time.Sleep(Lifetime)
}

// Handle is a started helper process.
type Handle struct {
	// PID is the operating system process id.
	PID int

	// Path is the executable image the process is running from.
	Path string

	command *exec.Cmd
	exited  chan struct{}
}

// HasExited reports whether the process has terminated.
//
// A process id lookup is not a liveness test. On Windows the id stays resolvable while the
// exec package still holds a handle to the terminated process, so os.FindProcess reports a
// dead process as alive. Waiting on the process is the only authoritative answer, so that
// is what this reports.
func (h *Handle) HasExited() bool {
	select {
	case <-h.exited:
		return true
	default:
		return false
	}
}

// Stop terminates the process and waits for it to be reaped. It is safe to call more than
// once, and safe to call on a process that already exited.
func (h *Handle) Stop() {
	_ = h.command.Process.Kill()
	<-h.exited
}

// RequireStopped asserts the process terminated, allowing a short window for the exit to
// be observed. The operating system reports termination before the waiting goroutine sees
// it, so an instantaneous check would be flaky.
func (h *Handle) RequireStopped(t *testing.T) {
	t.Helper()

	require.Eventually(t, h.HasExited, exitTimeout, pollInterval,
		"process %d (%s) was expected to be stopped", h.PID, h.Path)
}

// RequireRunning asserts the process is still running.
func (h *Handle) RequireRunning(t *testing.T) {
	t.Helper()

	require.False(t, h.HasExited(), "process %d (%s) was expected to still be running", h.PID, h.Path)
}

// RequireImageLocked waits until the operating system refuses write access to the
// executable image, which is the state Windows puts a running executable's file into.
//
// The lock is taken by the image loader rather than by process creation, so it is not in
// place the instant the process starts. Callers that depend on the lock must wait for it.
//
// The probe opens for write rather than attempting a delete. A delete probe destroys the
// fixture on the very run where the lock has not been taken yet, and then reports success
// forever after because the follow-up probe fails with "file not found".
func (h *Handle) RequireImageLocked(t *testing.T) {
	t.Helper()

	require.Eventually(t, func() bool {
		file, err := os.OpenFile(h.Path, os.O_RDWR, 0)
		if err != nil {
			return true
		}

		_ = file.Close()

		return false
	}, exitTimeout, pollInterval, "process never took an image lock on %s", h.Path)
}

// ExecutableName returns base with the platform's executable extension applied.
func ExecutableName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}

	return base
}

// StartIn copies the running test binary into dir as name and starts it in the given mode.
// The name is adjusted for the platform's executable extension.
//
// The process is stopped during test cleanup.
func StartIn(t *testing.T, dir string, name string, mode Mode) *Handle {
	t.Helper()

	return StartAt(t, filepath.Join(dir, ExecutableName(name)), mode)
}

// StartAt copies the running test binary to path and starts it in the given mode, so the
// caller controls the exact executable path the process runs from.
//
// The process is stopped during test cleanup.
func StartAt(t *testing.T, path string, mode Mode) *Handle {
	t.Helper()

	self, err := os.Executable()
	require.NoError(t, err, "the test binary must be locatable to copy itself")

	source, err := os.Open(self)
	require.NoError(t, err)
	defer func() { _ = source.Close() }()

	// A real copy rather than a hard link. A link would share the underlying file with
	// the test binary itself, which would change the very locking behavior these tests
	// exist to verify.
	//
	// G703: the path is chosen by the test that calls this helper, normally from
	// t.TempDir. No external or user-supplied input reaches it.
	//nolint:gosec // test-controlled destination path
	destination, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, executablePermissions)
	require.NoError(t, err)

	_, err = io.Copy(destination, source)
	require.NoError(t, err)
	require.NoError(t, destination.Close())

	// The helper never runs a test. Naming a test that cannot exist keeps the testing
	// package quiet and turns a dropped mode variable into a fast exit rather than a hang.
	command := exec.Command(path, "-test.run=NoSuchTestExists")
	command.Env = append(os.Environ(), ModeEnvVar+"="+string(mode))
	command.Stdout = io.Discard
	command.Stderr = io.Discard

	require.NoError(t, command.Start(), "failed to start helper at %s", path)

	handle := &Handle{
		PID:     command.Process.Pid,
		Path:    path,
		command: command,
		exited:  make(chan struct{}),
	}

	// A single owner of Wait, so the exit is observed exactly once and cleanup cannot race
	// with an assertion about the process having stopped.
	go func() {
		_ = command.Wait()
		close(handle.exited)
	}()

	t.Cleanup(handle.Stop)

	return handle
}
