// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exec

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	commandRunnerHelperModeEnv    = "AZD_COMMAND_RUNNER_HELPER_MODE"
	commandRunnerHelperReadyEnv   = "AZD_COMMAND_RUNNER_HELPER_READY"
	commandRunnerHelperReleaseEnv = "AZD_COMMAND_RUNNER_HELPER_RELEASE"
	commandRunnerHelperAddressEnv = "AZD_COMMAND_RUNNER_HELPER_ADDRESS"
	commandRunnerHelperExeEnv     = "AZD_COMMAND_RUNNER_HELPER_EXE"
)

type commandRunResponse struct {
	result RunResult
	err    error
}

type manualDeadlineContext struct {
	context.Context
	done chan struct{}
	once sync.Once
}

func newManualDeadlineContext(parent context.Context) *manualDeadlineContext {
	return &manualDeadlineContext{
		Context: parent,
		done:    make(chan struct{}),
	}
}

func (c *manualDeadlineContext) Done() <-chan struct{} {
	return c.done
}

func (c *manualDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (c *manualDeadlineContext) expire() {
	c.once.Do(func() {
		close(c.done)
	})
}

func TestCmdTreeSupportsCommandWithoutContext(t *testing.T) {
	cmd := CmdTree{
		Cmd: osexec.Command(os.Args[0], "-test.run=^$"), //nolint:gosec
	}

	require.NoError(t, cmd.Start())
	require.NoError(t, cmd.Wait())
}

func TestCommandRunnerHelperProcess(t *testing.T) {
	mode := os.Getenv(commandRunnerHelperModeEnv)
	if mode == "" {
		return
	}

	switch mode {
	case "mark-start":
		//nolint:gosec // G703: path is supplied by the parent test process
		require.NoError(t, os.WriteFile(
			os.Getenv(commandRunnerHelperReadyEnv),
			[]byte("started"),
			0o600,
		))
	case "wait":
		_, err := io.WriteString(os.Stdout, "stdout before cancellation\n") //nolint:forbidigo
		require.NoError(t, err)
		_, err = io.WriteString(os.Stderr, "stderr before cancellation\n") //nolint:forbidigo
		require.NoError(t, err)
		writeHelperReady(t)
		waitForever()
	case "success-after-release":
		writeHelperReady(t)
		releasePath := os.Getenv(commandRunnerHelperReleaseEnv)
		require.Eventually(t, func() bool {
			//nolint:gosec // G703: path is supplied by the parent test process
			_, err := os.Stat(releasePath)
			return err == nil
		}, 10*time.Second, 10*time.Millisecond)
	case "exit-42":
		os.Exit(42)
	case "spawn-listener",
		"spawn-listener-and-exit",
		"spawn-listener-and-exit-42",
		"spawn-listener-without-pipes-and-exit":
		childMode := "listen"
		if mode == "spawn-listener-and-exit" || mode == "spawn-listener-and-exit-42" {
			childMode = "listen-after-parent-exit"
		}
		child := osexec.Command(os.Args[0], "-test.run=TestCommandRunnerHelperProcess") //nolint:gosec
		child.Env = append(os.Environ(),
			commandRunnerHelperModeEnv+"="+childMode,
			commandRunnerHelperAddressEnv+"="+os.Getenv(commandRunnerHelperAddressEnv),
		)
		if mode == "spawn-listener-and-exit" || mode == "spawn-listener-and-exit-42" {
			child.Stdout = os.Stdout //nolint:forbidigo
			child.Stderr = os.Stderr //nolint:forbidigo
		}
		require.NoError(t, child.Start())
		if mode == "spawn-listener-without-pipes-and-exit" {
			waitForPath(t, os.Getenv(commandRunnerHelperAddressEnv))
			os.Exit(0)
		}
		if mode == "spawn-listener" {
			waitForever()
		}
		if mode == "spawn-listener-and-exit-42" {
			os.Exit(42)
		}
		os.Exit(0)
	case "listen", "listen-after-parent-exit":
		if mode == "listen-after-parent-exit" {
			time.Sleep(250 * time.Millisecond)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer listener.Close()
		//nolint:gosec // G703: path is supplied by the parent test process
		require.NoError(t, os.WriteFile(
			os.Getenv(commandRunnerHelperAddressEnv),
			[]byte(listener.Addr().String()),
			0o600,
		))
		waitForever()
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func TestCommandRunner_PreCanceledContextPreventsStart(t *testing.T) {
	runner := NewCommandRunner(nil)

	for _, test := range []struct {
		name     string
		useShell bool
	}{
		{name: "default execution"},
		{name: "explicit shell", useShell: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			readyPath := filepath.Join(t.TempDir(), "started")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_, err := runner.Run(ctx, helperRunArgs("mark-start", readyPath, "").
				WithShell(test.useShell))

			require.ErrorIs(t, err, context.Canceled)
			_, statErr := os.Stat(readyPath)
			require.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}

func TestCommandRunner_RunListPreCanceledContextPreventsStart(t *testing.T) {
	runner := NewCommandRunner(nil)
	readyPath := filepath.Join(t.TempDir(), "started")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := runner.RunList(ctx, []string{helperListCommand()}, RunArgs{
		Env: []string{
			helperExecutableEnv(os.Args[0]),
			commandRunnerHelperModeEnv + "=mark-start",
			commandRunnerHelperReadyEnv + "=" + readyPath,
		},
	})

	require.ErrorIs(t, err, context.Canceled)
	_, statErr := os.Stat(readyPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestCommandRunner_RunListQuotesWindowsExecutablePath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cmd.exe quoting is Windows-specific")
	}

	tempDir := filepath.Join(t.TempDir(), "path with spaces")
	require.NoError(t, os.MkdirAll(tempDir, 0o700))
	helperPath := filepath.Join(tempDir, "exec.test.exe")
	contents, err := os.ReadFile(os.Args[0]) //nolint:gosec // current test executable
	require.NoError(t, err)
	//nolint:gosec // G703: helperPath is under the test-controlled temporary directory.
	require.NoError(t, os.WriteFile(helperPath, contents, 0o600))
	readyPath := filepath.Join(t.TempDir(), "started")

	_, err = NewCommandRunner(nil).RunList(
		t.Context(),
		[]string{helperListCommand()},
		RunArgs{Env: []string{
			helperExecutableEnv(helperPath),
			commandRunnerHelperModeEnv + "=mark-start",
			commandRunnerHelperReadyEnv + "=" + readyPath,
		}},
	)

	require.NoError(t, err)
	require.FileExists(t, readyPath)
}

func TestCommandRunner_PreCanceledContextPreservesCause(t *testing.T) {
	runner := NewCommandRunner(nil)
	cause := errors.New("custom cancellation cause")

	for _, test := range []struct {
		name string
		run  func(context.Context) error
	}{
		{
			name: "run",
			run: func(ctx context.Context) error {
				_, err := runner.Run(ctx, helperRunArgs("mark-start", "", ""))
				return err
			},
		},
		{
			name: "run list",
			run: func(ctx context.Context) error {
				_, err := runner.RunList(ctx, []string{helperListCommand()}, RunArgs{})
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(cause)

			err := test.run(ctx)

			require.ErrorIs(t, err, cause)
			require.NotErrorIs(t, err, context.Canceled)
		})
	}
}

func TestCommandRunner_CancellationPreservesOutputAndExitDiagnostics(t *testing.T) {
	runner := NewCommandRunner(nil)

	for _, test := range []struct {
		name     string
		useShell bool
	}{
		{name: "default execution"},
		{name: "explicit shell", useShell: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			readyPath := filepath.Join(t.TempDir(), "ready")
			ctx, cancel := context.WithCancel(t.Context())

			response := make(chan commandRunResponse, 1)
			go func() {
				result, err := runner.Run(ctx, helperRunArgs("wait", readyPath, "").
					WithShell(test.useShell))
				response <- commandRunResponse{result: result, err: err}
			}()

			waitForPath(t, readyPath)
			cancel()

			got := waitForRunResponse(t, response)
			require.ErrorIs(t, got.err, context.Canceled)
			require.NotZero(t, got.result.ExitCode)
			require.Contains(t, got.result.Stdout, "stdout before cancellation")
			require.Contains(t, got.result.Stderr, "stderr before cancellation")

			exitErr, ok := errors.AsType[*ExitError](got.err)
			require.True(t, ok)
			require.Equal(t, got.result.ExitCode, exitErr.ExitCode)
		})
	}
}

func TestCommandRunner_InteractiveCancellationTerminatesManagedProcess(t *testing.T) {
	runner := NewCommandRunner(&RunnerOptions{
		Stdin:  strings.NewReader(""),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	readyPath := filepath.Join(t.TempDir(), "ready")
	ctx, cancel := context.WithCancel(t.Context())

	response := make(chan commandRunResponse, 1)
	go func() {
		args := helperRunArgs("wait", readyPath, "")
		args.Interactive = true
		result, err := runner.Run(ctx, args)
		response <- commandRunResponse{result: result, err: err}
	}()

	waitForPath(t, readyPath)
	cancel()

	got := waitForRunResponse(t, response)
	require.ErrorIs(t, got.err, context.Canceled)
	require.NotZero(t, got.result.ExitCode)
}

func TestCommandRunner_CancellationPreservesCustomCause(t *testing.T) {
	runner := NewCommandRunner(nil)
	readyPath := filepath.Join(t.TempDir(), "ready")
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("custom cancellation cause")

	response := make(chan commandRunResponse, 1)
	go func() {
		result, err := runner.Run(ctx, helperRunArgs("wait", readyPath, ""))
		response <- commandRunResponse{result: result, err: err}
	}()

	waitForPath(t, readyPath)
	cancel(cause)

	got := waitForRunResponse(t, response)
	require.ErrorIs(t, got.err, cause)
	require.NotErrorIs(t, got.err, context.Canceled)
	require.NotZero(t, got.result.ExitCode)
}

func TestCommandRunner_RunListCancellation(t *testing.T) {
	runner := NewCommandRunner(nil)
	readyPath := filepath.Join(t.TempDir(), "ready")
	ctx, cancel := context.WithCancel(t.Context())

	response := make(chan commandRunResponse, 1)
	go func() {
		result, err := runner.RunList(ctx, []string{helperListCommand()}, RunArgs{
			Env: []string{
				helperExecutableEnv(os.Args[0]),
				commandRunnerHelperModeEnv + "=wait",
				commandRunnerHelperReadyEnv + "=" + readyPath,
			},
		})
		response <- commandRunResponse{result: result, err: err}
	}()

	if early, completed := waitForPathOrRunResponse(t, readyPath, response); completed {
		t.Fatalf(
			"command completed before signaling readiness: err=%v, stdout=%q, stderr=%q",
			early.err,
			early.result.Stdout,
			early.result.Stderr,
		)
	}
	cancel()

	got := waitForRunResponse(t, response)
	require.ErrorIs(t, got.err, context.Canceled)
	require.NotZero(t, got.result.ExitCode)
	require.Contains(t, got.result.Stdout, "stdout before cancellation")
	require.Contains(t, got.result.Stderr, "stderr before cancellation")
}

func TestCommandRunner_DeadlineExceeded(t *testing.T) {
	runner := NewCommandRunner(nil)
	readyPath := filepath.Join(t.TempDir(), "ready")
	ctx := newManualDeadlineContext(t.Context())

	response := make(chan commandRunResponse, 1)
	go func() {
		result, err := runner.Run(ctx, helperRunArgs("wait", readyPath, ""))
		response <- commandRunResponse{result: result, err: err}
	}()

	waitForPath(t, readyPath)
	ctx.expire()

	got := waitForRunResponse(t, response)
	require.ErrorIs(t, got.err, context.DeadlineExceeded)
	require.NotZero(t, got.result.ExitCode)
}

func TestCommandRunner_OrdinaryExitErrorUnchanged(t *testing.T) {
	runner := NewCommandRunner(nil)

	result, err := runner.Run(t.Context(), helperRunArgs("exit-42", "", ""))

	require.Error(t, err)
	require.NotErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 42, result.ExitCode)
	exitErr, ok := errors.AsType[*ExitError](err)
	require.True(t, ok)
	require.Equal(t, 42, exitErr.ExitCode)
}

func TestCommandRunner_CompletionBeforeCancellationWins(t *testing.T) {
	runner := NewCommandRunner(nil)
	tempDir := t.TempDir()
	readyPath := filepath.Join(tempDir, "ready")
	releasePath := filepath.Join(tempDir, "release")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	response := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, helperRunArgs("success-after-release", readyPath, releasePath))
		response <- err
	}()

	waitForPath(t, readyPath)
	require.NoError(t, os.WriteFile(releasePath, []byte("release"), 0o600))
	require.NoError(t, waitForError(t, response))

	cancel()
}

func TestCommandRunner_CancellationTerminatesChildProcess(t *testing.T) {
	runner := NewCommandRunner(nil)
	addressPath := filepath.Join(t.TempDir(), "address")
	ctx, cancel := context.WithCancel(t.Context())

	response := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, helperRunArgs("spawn-listener", "", addressPath))
		response <- err
	}()

	waitForPath(t, addressPath)
	address := strings.TrimSpace(string(mustReadFile(t, addressPath)))
	cancel()

	require.ErrorIs(t, waitForError(t, response), context.Canceled)
	require.Eventually(t, func() bool {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return false
		}
		_ = listener.Close()
		return true
	}, 10*time.Second, 20*time.Millisecond)
}

func TestCommandRunner_InheritedPipesDoNotHang(t *testing.T) {
	runner := NewCommandRunner(nil)
	addressPath := filepath.Join(t.TempDir(), "address")

	response := make(chan error, 1)
	go func() {
		_, err := runner.Run(t.Context(), helperRunArgs("spawn-listener-and-exit", "", addressPath))
		response <- err
	}()

	waitForPath(t, addressPath)
	address := strings.TrimSpace(string(mustReadFile(t, addressPath)))

	require.ErrorIs(t, waitForError(t, response), osexec.ErrWaitDelay)
	require.Eventually(t, func() bool {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return false
		}
		_ = listener.Close()
		return true
	}, 10*time.Second, 20*time.Millisecond)
}

func TestCommandRunner_NormalCompletionTerminatesRemainingChildProcess(t *testing.T) {
	runner := NewCommandRunner(nil)

	for _, test := range []struct {
		name string
		run  func(context.Context, string) (RunResult, error)
	}{
		{
			name: "run",
			run: func(ctx context.Context, addressPath string) (RunResult, error) {
				return runner.Run(
					ctx,
					helperRunArgs("spawn-listener-without-pipes-and-exit", "", addressPath),
				)
			},
		},
		{
			name: "run list",
			run: func(ctx context.Context, addressPath string) (RunResult, error) {
				return runner.RunList(ctx, []string{helperListCommand()}, RunArgs{
					Env: []string{
						helperExecutableEnv(os.Args[0]),
						commandRunnerHelperModeEnv + "=spawn-listener-without-pipes-and-exit",
						commandRunnerHelperAddressEnv + "=" + addressPath,
					},
				})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			addressPath := filepath.Join(t.TempDir(), "address")

			_, err := test.run(t.Context(), addressPath)

			require.NoError(t, err)
			address := strings.TrimSpace(string(mustReadFile(t, addressPath)))
			require.Eventually(t, func() bool {
				listener, err := net.Listen("tcp", address)
				if err != nil {
					return false
				}
				_ = listener.Close()
				return true
			}, 10*time.Second, 20*time.Millisecond)
		})
	}
}

func TestCommandRunner_CancellationAfterRootExitTerminatesChildProcess(t *testing.T) {
	runner := NewCommandRunner(nil)
	addressPath := filepath.Join(t.TempDir(), "address")
	ctx, cancel := context.WithCancel(t.Context())

	response := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, helperRunArgs("spawn-listener-and-exit", "", addressPath))
		response <- err
	}()

	waitForPath(t, addressPath)
	address := strings.TrimSpace(string(mustReadFile(t, addressPath)))
	cancel()

	err := waitForError(t, response)
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, osexec.ErrWaitDelay)
	require.Eventually(t, func() bool {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return false
		}
		_ = listener.Close()
		return true
	}, 10*time.Second, 20*time.Millisecond)
}

func TestCommandRunner_RunListRootFailureBeforeCancellationIsPreserved(t *testing.T) {
	runner := NewCommandRunner(nil)
	addressPath := filepath.Join(t.TempDir(), "address")
	ctx, cancel := context.WithCancel(t.Context())

	response := make(chan commandRunResponse, 1)
	go func() {
		result, err := runner.RunList(ctx, []string{helperListCommand()}, RunArgs{
			Env: []string{
				helperExecutableEnv(os.Args[0]),
				commandRunnerHelperModeEnv + "=spawn-listener-and-exit-42",
				commandRunnerHelperAddressEnv + "=" + addressPath,
			},
		})
		response <- commandRunResponse{result: result, err: err}
	}()

	if early, completed := waitForPathOrRunResponse(t, addressPath, response); completed {
		t.Fatalf(
			"command completed before cancellation: err=%v, stdout=%q, stderr=%q",
			early.err,
			early.result.Stdout,
			early.result.Stderr,
		)
	}
	cancel()

	got := waitForRunResponse(t, response)
	require.ErrorIs(t, got.err, context.Canceled)
	require.NotErrorIs(t, got.err, osexec.ErrWaitDelay)
	require.Equal(t, 42, got.result.ExitCode)
	require.ErrorContains(t, got.err, "exit code: 42")

	exitErr, ok := errors.AsType[*ExitError](got.err)
	require.True(t, ok)
	require.Equal(t, 42, exitErr.ExitCode)
}

func helperRunArgs(mode, readyPath, additionalPath string) RunArgs {
	env := []string{commandRunnerHelperModeEnv + "=" + mode}
	if readyPath != "" {
		env = append(env, commandRunnerHelperReadyEnv+"="+readyPath)
	}
	switch mode {
	case "success-after-release":
		env = append(env, commandRunnerHelperReleaseEnv+"="+additionalPath)
	case "spawn-listener",
		"spawn-listener-and-exit",
		"spawn-listener-and-exit-42",
		"spawn-listener-without-pipes-and-exit":
		env = append(env, commandRunnerHelperAddressEnv+"="+additionalPath)
	}

	return RunArgs{
		Cmd:  os.Args[0],
		Args: []string{"-test.run=TestCommandRunnerHelperProcess"},
		Env:  env,
	}
}

func helperListCommand() string {
	if runtime.GOOS == "windows" {
		return `%` + commandRunnerHelperExeEnv + `% -test.run=TestCommandRunnerHelperProcess`
	}
	return `"$` + commandRunnerHelperExeEnv + `" -test.run=TestCommandRunnerHelperProcess`
}

func helperExecutableEnv(path string) string {
	if runtime.GOOS == "windows" {
		path = `"` + path + `"`
	}
	return commandRunnerHelperExeEnv + "=" + path
}

func writeHelperReady(t *testing.T) {
	t.Helper()
	//nolint:gosec // G703: path is supplied by the parent test process
	require.NoError(t, os.WriteFile(
		os.Getenv(commandRunnerHelperReadyEnv),
		[]byte("ready"),
		0o600,
	))
}

func waitForever() {
	for {
		time.Sleep(time.Hour)
	}
}

func waitForPath(t *testing.T, path string) {
	t.Helper()
	require.Eventually(t, func() bool {
		//nolint:gosec // G703: path is supplied by the parent test process
		_, err := os.Stat(path)
		return err == nil
	}, 10*time.Second, 10*time.Millisecond)
}

func waitForPathOrRunResponse(
	t *testing.T,
	path string,
	response <-chan commandRunResponse,
) (commandRunResponse, bool) {
	t.Helper()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case got := <-response:
			return got, true
		case <-ticker.C:
			if _, err := os.Stat(path); err == nil {
				return commandRunResponse{}, false
			}
		case <-timeout.C:
			t.Fatal("timed out waiting for command readiness")
			return commandRunResponse{}, false
		}
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return content
}

func waitForError(t *testing.T, response <-chan error) error {
	t.Helper()
	select {
	case err := <-response:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for command")
		return nil
	}
}

func waitForRunResponse(t *testing.T, response <-chan commandRunResponse) commandRunResponse {
	t.Helper()
	select {
	case got := <-response:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for command")
		return commandRunResponse{}
	}
}
