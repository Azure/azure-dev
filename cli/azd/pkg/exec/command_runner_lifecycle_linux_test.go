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

	internalprocess "github.com/azure/azure-dev/cli/azd/pkg/exec/internal/process"
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
	case "nested-root-wait":
		inner := osexec.Command( //nolint:gosec // test executable and fixed arguments
			os.Args[0],
			"-test.run=^TestCommandRunnerDetachedHelperProcess$",
		)
		inner.Env = append(
			os.Environ(),
			detachedHelperModeEnv+"=root-exit",
			detachedHelperPIDEnv+"="+pidPath,
		)
		tree := internalprocess.New(context.Background(), inner, internalprocess.Options{})
		require.NoError(t, tree.Start())
		require.NoError(t, tree.Wait())
		_ = detachedHelperPID(t, pidPath)
		waitForever()
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

func TestCommandRunner_NormalCompletionPreservesDetachedProcess(t *testing.T) {
	runner := NewCommandRunner(nil)

	for _, test := range []struct {
		name string
		run  func(context.Context, string) (RunResult, error)
	}{
		{
			name: "run",
			run: func(ctx context.Context, pidPath string) (RunResult, error) {
				return runner.Run(ctx, detachedHelperRunArgs("root-exit", pidPath))
			},
		},
		{
			name: "run list",
			run: func(ctx context.Context, pidPath string) (RunResult, error) {
				return runner.RunList(ctx, []string{detachedHelperListCommand()}, RunArgs{
					Env: []string{
						helperExecutableEnv(os.Args[0]),
						detachedHelperModeEnv + "=root-exit",
						detachedHelperPIDEnv + "=" + pidPath,
					},
				})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			pidPath := filepath.Join(t.TempDir(), "child.pid")

			_, err := test.run(t.Context(), pidPath)

			childPID := detachedHelperPID(t, pidPath)
			cleanupDetachedHelper(t, childPID)
			require.NoError(t, err)
			require.True(t, posixProcessRunning(childPID))
		})
	}
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

func TestCommandRunner_CancellationTerminatesNestedDetachedProcess(t *testing.T) {
	runner := NewCommandRunner(nil)
	tempDir := t.TempDir()
	nestedPIDPath := filepath.Join(tempDir, "nested-child.pid")
	siblingPIDPath := filepath.Join(tempDir, "sibling-child.pid")
	outerCtx, cancelOuter := context.WithCancel(t.Context())
	siblingCtx, cancelSibling := context.WithCancel(t.Context())
	t.Cleanup(cancelOuter)
	t.Cleanup(cancelSibling)

	outerResponse := make(chan error, 1)
	go func() {
		_, err := runner.Run(
			outerCtx,
			detachedHelperRunArgs("nested-root-wait", nestedPIDPath),
		)
		outerResponse <- err
	}()
	siblingResponse := make(chan error, 1)
	go func() {
		_, err := runner.Run(
			siblingCtx,
			detachedHelperRunArgs("root-wait", siblingPIDPath),
		)
		siblingResponse <- err
	}()

	nestedPID := detachedHelperPID(t, nestedPIDPath)
	siblingPID := detachedHelperPID(t, siblingPIDPath)
	cleanupDetachedHelper(t, nestedPID)
	cleanupDetachedHelper(t, siblingPID)
	require.True(t, posixProcessRunning(nestedPID))
	require.True(t, posixProcessRunning(siblingPID))

	cancelOuter()

	require.ErrorIs(t, waitForError(t, outerResponse), context.Canceled)
	require.Eventually(t, func() bool {
		return !posixProcessRunning(nestedPID)
	}, 5*time.Second, 10*time.Millisecond)
	require.True(t, posixProcessRunning(siblingPID))

	cancelSibling()

	require.ErrorIs(t, waitForError(t, siblingResponse), context.Canceled)
	require.Eventually(t, func() bool {
		return !posixProcessRunning(siblingPID)
	}, 5*time.Second, 10*time.Millisecond)
}

func detachedHelperListCommand() string {
	return `"$` + commandRunnerHelperExeEnv + `" -test.run=^TestCommandRunnerDetachedHelperProcess$`
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
