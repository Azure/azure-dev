// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const movedProcessGroupReadyEnv = "AZD_MOVED_PROCESS_GROUP_READY"

func TestTreeRootMovesProcessGroupHelper(t *testing.T) {
	readyPath := os.Getenv(movedProcessGroupReadyEnv)
	if readyPath == "" {
		return
	}

	parentProcessGroup, err := unix.Getpgid(os.Getppid())
	require.NoError(t, err)
	require.NoError(t, unix.Setpgid(0, parentProcessGroup))
	//nolint:gosec // G703: path is supplied by the parent test process
	require.NoError(t, os.WriteFile(readyPath, []byte("ready"), 0o600))
	for {
		time.Sleep(time.Hour)
	}
}

func TestTreeCancellationKillsRootAfterProcessGroupChange(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("custom cancellation cause")
	readyPath := filepath.Join(t.TempDir(), "ready")

	cmd := exec.CommandContext( //nolint:gosec // current test executable and fixed arguments
		ctx,
		os.Args[0],
		"-test.run=^TestTreeRootMovesProcessGroupHelper$",
	)
	cmd.Env = append(os.Environ(), movedProcessGroupReadyEnv+"="+readyPath)
	tree := New(ctx, cmd, Options{})
	require.NoError(t, tree.Start())
	t.Cleanup(func() {
		_ = tree.Cmd.Process.Kill()
	})

	require.Eventually(t, func() bool {
		_, err := os.Stat(readyPath)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	processGroup, err := unix.Getpgid(tree.Cmd.Process.Pid)
	require.NoError(t, err)
	require.NotEqual(t, tree.Cmd.Process.Pid, processGroup)

	cancel(cause)
	err = tree.Wait()

	require.ErrorIs(t, err, cause)
	require.NotErrorIs(t, err, context.Canceled)
}

func TestTreeWaitUsesCustomCauseAfterSuccessfulRootExit(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("custom cancellation cause")
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")

	cmd := exec.CommandContext( //nolint:gosec // fixed shell with a test-controlled temporary path
		ctx,
		"/bin/sh",
		"-c",
		`sleep 30 & echo $! > "$1"`,
		"sh",
		childPIDPath,
	)
	tree := New(ctx, cmd, Options{})
	require.NoError(t, tree.Start())

	childPID := waitForProcessPID(t, childPIDPath)
	t.Cleanup(func() {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
	})

	var processInfo unix.Siginfo
	require.NoError(t, unix.Waitid(
		unix.P_PID,
		tree.Cmd.Process.Pid,
		&processInfo,
		unix.WEXITED|unix.WNOWAIT,
		nil,
	))

	cancel(cause)
	require.Eventually(t, func() bool {
		tree.cancelMu.Lock()
		defer tree.cancelMu.Unlock()
		return tree.cancelOutcome == cancelInterrupted
	}, 5*time.Second, time.Millisecond)

	err := tree.Wait()

	require.ErrorIs(t, err, cause)
	require.NotErrorIs(t, err, context.Canceled)
}

func TestTerminateOwnedTreeDiscoversMarkerBeforeStalledAncestry(t *testing.T) {
	configureStalledProcessDiscovery(t)
	const processTreeID = "marker-before-ancestry"

	owned := exec.Command("/bin/sleep", "30") //nolint:gosec // fixed test executable and argument
	owned.Env = append(os.Environ(), processTreeEnv+"="+processTreeID)
	require.NoError(t, owned.Start())
	waitDone := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = owned.Wait()
		close(waitDone)
	}()
	t.Cleanup(func() {
		_ = owned.Process.Kill()
		<-waitDone
	})

	tree := &Tree{
		Cmd: &exec.Cmd{
			Process: &os.Process{Pid: os.Getpid()},
		},
		platform: platformState{
			processTreeID: processTreeID,
		},
	}

	require.NoError(t, terminateOwnedTree(tree, func() error {
		return nil
	}))
	select {
	case <-waitDone:
		require.Error(t, waitErr)
	case <-time.After(2 * time.Second):
		t.Fatal("marker-owned process survived stalled ancestry discovery")
	}
}

func waitForProcessPID(t *testing.T, path string) int {
	t.Helper()

	var pid int
	require.Eventually(t, func() bool {
		//nolint:gosec // G703: path is supplied by the parent test process
		content, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(content)))
		return err == nil && pid > 0
	}, 5*time.Second, 10*time.Millisecond)
	return pid
}
