// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux

package exec

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	detachedHelperModeEnv = "AZD_DETACHED_HELPER_MODE"
	detachedHelperPIDEnv  = "AZD_DETACHED_HELPER_PID"
)

func TestCommandRunnerDetachedHelperProcess(t *testing.T) {
	mode := os.Getenv(detachedHelperModeEnv)
	if mode == "" {
		return
	}

	pidPath := os.Getenv(detachedHelperPIDEnv)
	switch mode {
	case "root-exit", "root-wait":
		child := osexec.Command( //nolint:gosec // test executable and fixed arguments
			os.Args[0],
			"-test.run=^TestCommandRunnerDetachedHelperProcess$",
		)
		child.Env = append(
			os.Environ(),
			detachedHelperModeEnv+"=child",
			detachedHelperPIDEnv+"="+pidPath,
		)
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		require.NoError(t, child.Start())
		_ = detachedHelperPID(t, pidPath)
		if mode == "root-wait" {
			waitForever()
		}
	case "child":
		//nolint:gosec // G703: path is supplied by the parent test process
		require.NoError(t, os.WriteFile(
			pidPath,
			[]byte(strconv.Itoa(os.Getpid())),
			0o600,
		))
		waitForever()
	default:
		t.Fatalf("unknown detached helper mode %q", mode)
	}
}

func TestCommandRunner_NormalCompletionTerminatesDetachedProcess(t *testing.T) {
	runner := NewCommandRunner(nil)
	pidPath := filepath.Join(t.TempDir(), "child.pid")

	_, err := runner.Run(t.Context(), detachedHelperRunArgs("root-exit", pidPath))

	childPID := detachedHelperPID(t, pidPath)
	cleanupDetachedHelper(t, childPID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return !posixProcessRunning(childPID)
	}, 5*time.Second, 10*time.Millisecond)
}

func TestCommandRunner_CancellationTerminatesDetachedProcess(t *testing.T) {
	runner := NewCommandRunner(nil)
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(t.Context())

	response := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, detachedHelperRunArgs("root-wait", pidPath))
		response <- err
	}()

	childPID := detachedHelperPID(t, pidPath)
	cleanupDetachedHelper(t, childPID)
	require.True(t, posixProcessRunning(childPID))

	cancel()

	require.ErrorIs(t, waitForError(t, response), context.Canceled)
	require.Eventually(t, func() bool {
		return !posixProcessRunning(childPID)
	}, 5*time.Second, 10*time.Millisecond)
}

func detachedHelperRunArgs(mode, pidPath string) RunArgs {
	return RunArgs{
		Cmd:  os.Args[0],
		Args: []string{"-test.run=^TestCommandRunnerDetachedHelperProcess$"},
		Env: []string{
			detachedHelperModeEnv + "=" + mode,
			detachedHelperPIDEnv + "=" + pidPath,
		},
	}
}

func detachedHelperPID(t *testing.T, path string) int {
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

func cleanupDetachedHelper(t *testing.T, pid int) {
	t.Helper()
	t.Cleanup(func() {
		if posixProcessRunning(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
}
