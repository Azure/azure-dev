// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package processutil

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/test/proctest"
	"github.com/stretchr/testify/require"
)

// Windows file-lock and process-image behavior cannot be faked meaningfully, so these
// tests run against real processes started by proctest.

// modeIgnoreTerm swallows termination requests so escalation to a forced kill can be
// observed. It is specific to this package, so it is handled here rather than in proctest.
const modeIgnoreTerm proctest.Mode = "ignore-term"

func TestMain(m *testing.M) {
	if proctest.RequestedMode() == modeIgnoreTerm {
		notifications := make(chan os.Signal, 1)
		signal.Notify(notifications, ignorableSignals()...)
		proctest.Idle()

		return
	}

	if proctest.RunRequestedHelper() {
		return
	}

	os.Exit(m.Run())
}

// spawnHelperIn starts a real process out of dir in the given helper mode, so the running
// executable is genuinely contained within dir.
func spawnHelperIn(t *testing.T, dir string, mode proctest.Mode) *proctest.Handle {
	t.Helper()

	handle := proctest.StartIn(t, dir, "azd-processutil-helper", mode)
	requireProcessDiscoverable(t, dir, handle.PID)

	return handle
}

// processIn resolves the discovered ProcessInfo for pid inside dir, so tests terminate
// through the same discover-then-terminate path that the manager uses.
func processIn(t *testing.T, dir string, pid int) ProcessInfo {
	t.Helper()

	found, err := FindByExecutableDir(t.Context(), dir)
	require.NoError(t, err)

	for _, process := range found {
		if process.PID == pid {
			return process
		}
	}

	t.Fatalf("pid %d is not present in %s", pid, dir)

	return ProcessInfo{}
}

// requireProcessDiscoverable waits for the spawned process to become visible to the OS
// process tables, which are not updated synchronously with process creation.
func requireProcessDiscoverable(t *testing.T, dir string, pid int) {
	t.Helper()

	require.Eventually(t, func() bool {
		found, err := FindByExecutableDir(t.Context(), dir)
		if err != nil {
			return false
		}

		for _, process := range found {
			if process.PID == pid {
				return true
			}
		}

		return false
	}, 30*time.Second, 100*time.Millisecond, "spawned helper never became discoverable in %s", dir)
}

// T11: a real process running out of the directory is discovered, with the identifying
// details the error and status messages depend on.
func TestFindByExecutableDir_FindsSpawnedProcess(t *testing.T) {
	dir := t.TempDir()
	helper := spawnHelperIn(t, dir, proctest.ModeIdle)

	found, err := FindByExecutableDir(t.Context(), dir)
	require.NoError(t, err)

	var match *ProcessInfo

	for index := range found {
		if found[index].PID == helper.PID {
			match = &found[index]

			break
		}
	}

	require.NotNil(t, match, "spawned helper was not discovered")
	require.Equal(t, filepath.Base(helper.Path), match.Name)
	require.NotEmpty(t, match.Executable)

	normalized, err := normalizeScope(dir)
	require.NoError(t, err)
	require.True(t, executableInScope(normalized, match.Executable),
		"reported executable %q should be contained in %q", match.Executable, normalized)
}

// T12: the containment guarantee only means something if a process just outside the
// directory is genuinely excluded.
func TestFindByExecutableDir_IgnoresOutsideProcess(t *testing.T) {
	base := t.TempDir()

	inside := filepath.Join(base, "extension")
	outside := filepath.Join(base, "elsewhere")
	require.NoError(t, os.MkdirAll(inside, 0755))
	require.NoError(t, os.MkdirAll(outside, 0755))

	insideHelper := spawnHelperIn(t, inside, proctest.ModeIdle)
	outsideHelper := spawnHelperIn(t, outside, proctest.ModeIdle)

	found, err := FindByExecutableDir(t.Context(), inside)
	require.NoError(t, err)

	pids := make([]int, 0, len(found))
	for _, process := range found {
		pids = append(pids, process.PID)
	}

	require.Contains(t, pids, insideHelper.PID)
	require.NotContains(t, pids, outsideHelper.PID)
}

// T14: enumeration walks every process on the machine, most of which azd cannot inspect.
// Those denials must be skipped rather than failing the scan.
func TestFindByExecutableDir_TolerantOfDeniedPids(t *testing.T) {
	dir := t.TempDir()
	helper := spawnHelperIn(t, dir, proctest.ModeIdle)

	// System processes are unreadable for a non-elevated user on every supported
	// platform. Finding the helper anyway proves those denials did not abort the scan.
	found, err := FindByExecutableDir(t.Context(), dir)
	require.NoError(t, err)
	require.NotEmpty(t, found)

	pids := make([]int, 0, len(found))
	for _, process := range found {
		pids = append(pids, process.PID)
	}

	require.Contains(t, pids, helper.PID)
}

// T15: a cooperating process is stopped, and Terminate confirms it is gone.
func TestTerminate_GracefulStop(t *testing.T) {
	dir := t.TempDir()
	helper := spawnHelperIn(t, dir, proctest.ModeIdle)
	process := processIn(t, dir, helper.PID)

	stopped, err := Terminate(t.Context(), process, dir, DefaultGracePeriod)
	require.NoError(t, err)
	require.True(t, stopped, "Terminate should report that it stopped a running process")
	require.False(t, processRunning(helper.PID), "process should be gone after Terminate")

	found, err := FindByExecutableDir(t.Context(), dir)
	require.NoError(t, err)

	for _, process := range found {
		require.NotEqual(t, helper.PID, process.PID)
	}
}

// T16: a process that refuses to stop gracefully must still be stopped once the grace
// period expires, otherwise --force would not deliver on its name.
func TestTerminate_EscalatesToForce(t *testing.T) {
	dir := t.TempDir()
	helper := spawnHelperIn(t, dir, modeIgnoreTerm)
	process := processIn(t, dir, helper.PID)

	// A short grace keeps the test quick while still exercising the escalation path.
	// Windows has no graceful step at all and escalates immediately by design.
	start := time.Now()
	stopped, err := Terminate(t.Context(), process, dir, 500*time.Millisecond)
	require.NoError(t, err)
	require.True(t, stopped)
	require.False(t, processRunning(helper.PID))
	require.Less(t, time.Since(start), 30*time.Second, "escalation should be bounded")
}

// T17: losing the race against a process that already exited is a success, not a failure.
func TestTerminate_AlreadyExited(t *testing.T) {
	dir := t.TempDir()
	helper := spawnHelperIn(t, dir, proctest.ModeIdle)

	pid := helper.PID
	process := processIn(t, dir, pid)

	stopped, err := Terminate(t.Context(), process, dir, DefaultGracePeriod)
	require.NoError(t, err)
	require.True(t, stopped)

	// Terminating the same process again must be a no-op rather than an error, and it
	// must not claim to have stopped anything the second time.
	stopped, err = Terminate(t.Context(), process, dir, DefaultGracePeriod)
	require.NoError(t, err)
	require.False(t, stopped, "a process that had already exited was not stopped by azd")
}

// T18: a cancelled context must not extend the wait, so --force stays bounded and
// scriptable rather than hanging a CI job.
func TestTerminate_BoundedByContext(t *testing.T) {
	dir := t.TempDir()
	helper := spawnHelperIn(t, dir, modeIgnoreTerm)
	process := processIn(t, dir, helper.PID)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// The outcome is allowed to be either "stopped" or "not confirmed", but it must
	// never sit on the hour-long grace period after the context has been cancelled.
	start := time.Now()
	_, _ = Terminate(ctx, process, dir, time.Hour)
	elapsed := time.Since(start)

	require.Less(t, elapsed, 30*time.Second, "a cancelled context must not wait out the grace period")
}

// waitForExit reports on an already-dead PID without waiting for the timeout.
func TestWaitForExit_ZeroTimeout(t *testing.T) {
	dir := t.TempDir()
	helper := spawnHelperIn(t, dir, proctest.ModeIdle)
	process := processIn(t, dir, helper.PID)

	require.False(t, waitForExit(t.Context(), helper.PID, 0))

	_, err := Terminate(t.Context(), process, dir, DefaultGracePeriod)
	require.NoError(t, err)
	require.True(t, waitForExit(t.Context(), helper.PID, 0))
}
