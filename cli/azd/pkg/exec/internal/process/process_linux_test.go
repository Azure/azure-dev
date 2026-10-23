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
