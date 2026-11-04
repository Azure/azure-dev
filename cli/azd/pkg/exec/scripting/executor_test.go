// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scripting

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	scriptingHelperModeEnv    = "AZD_SCRIPTING_HELPER_MODE"
	scriptingHelperReadyEnv   = "AZD_SCRIPTING_HELPER_READY"
	scriptingHelperExeEnv     = "AZD_SCRIPTING_HELPER_EXE"
	scriptingHelperAddressEnv = "AZD_SCRIPTING_HELPER_ADDRESS"
	scriptingHelperPIDEnv     = "AZD_SCRIPTING_HELPER_PID"
)

func TestNew_ValidConfigs(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{"empty config", Config{}},
		{"bash", Config{Shell: "bash"}},
		{"sh", Config{Shell: "sh"}},
		{"zsh", Config{Shell: "zsh"}},
		{"pwsh", Config{Shell: "pwsh"}},
		{"powershell", Config{Shell: "powershell"}},
		{"cmd", Config{Shell: "cmd"}},
		{"uppercase", Config{Shell: "BASH"}},
		{"with args", Config{Args: []string{"a", "b"}}},
		{"interactive", Config{Interactive: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := New(tt.config)
			require.NoError(t, err)
			require.NotNil(t, e)
		})
	}
}

func TestNew_InvalidShell(t *testing.T) {
	e, err := New(Config{Shell: "python"})
	require.Error(t, err)
	assert.Nil(t, e)

	_, ok := errors.AsType[*InvalidShellError](err)
	assert.True(t, ok, "expected InvalidShellError, got %T", err)
}

func TestExecute_EmptyPath(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	err = e.Execute(t.Context(), "")
	require.Error(t, err)

	valErr, ok := errors.AsType[*ValidationError](err)
	require.True(t, ok, "expected ValidationError, got %T", err)
	assert.Equal(t, "scriptPath", valErr.Field)
}

func TestExecute_NonexistentFile(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "no-such-file.sh")
	err = e.Execute(t.Context(), path)
	require.Error(t, err)

	_, ok := errors.AsType[*ScriptNotFoundError](err)
	assert.True(t, ok, "expected ScriptNotFoundError, got %T", err)
}

func TestExecute_DirectoryPath(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	err = e.Execute(t.Context(), t.TempDir())
	require.Error(t, err)

	valErr, ok := errors.AsType[*ValidationError](err)
	require.True(t, ok, "expected ValidationError, got %T", err)
	assert.True(t,
		strings.Contains(valErr.Reason, "directory"),
		"expected reason about directory, got %q", valErr.Reason,
	)
}

func TestExecuteDirect_EmptyCommand(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	err = e.ExecuteDirect(t.Context(), "", nil)
	require.Error(t, err)

	valErr, ok := errors.AsType[*ValidationError](err)
	require.True(t, ok, "expected ValidationError, got %T", err)
	assert.Equal(t, "command", valErr.Field)
}

func TestExecuteInline_EmptyContent(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	err = e.ExecuteInline(t.Context(), "")
	require.Error(t, err)

	_, ok := errors.AsType[*ValidationError](err)
	assert.True(t, ok, "expected ValidationError, got %T", err)
}

func TestExecuteInline_WhitespaceOnly(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	err = e.ExecuteInline(t.Context(), "   \t  ")
	require.Error(t, err)

	_, ok := errors.AsType[*ValidationError](err)
	assert.True(t, ok, "expected ValidationError, got %T", err)
}

func TestExecuteDirect_PreCanceledContextPreventsStart(t *testing.T) {
	readyPath := filepath.Join(t.TempDir(), "started")
	e, err := New(Config{Env: append(os.Environ(),
		scriptingHelperModeEnv+"=mark-start",
		scriptingHelperReadyEnv+"="+readyPath,
	)})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err = e.ExecuteDirect(ctx, os.Args[0], []string{"-test.run=TestScriptingHelperProcess"})

	require.ErrorIs(t, err, context.Canceled)
	_, statErr := os.Stat(readyPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestExecuteInline_PreCanceledContextPreventsStart(t *testing.T) {
	readyPath := filepath.Join(t.TempDir(), "started")
	e, err := New(Config{Shell: platformShell()})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var script string
	if runtime.GOOS == "windows" {
		script = fmt.Sprintf(`echo started>"%s"`, strings.ReplaceAll(readyPath, "%", "%%"))
	} else {
		script = fmt.Sprintf("printf started > '%s'", strings.ReplaceAll(readyPath, "'", "'\\''"))
	}
	err = e.ExecuteInline(ctx, script)

	require.ErrorIs(t, err, context.Canceled)
	_, statErr := os.Stat(readyPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestExecute_PreCanceledContextPreventsStart(t *testing.T) {
	tempDir := t.TempDir()
	readyPath := filepath.Join(tempDir, "started")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var scriptPath string
	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(tempDir, "mark.cmd")
		require.NoError(t, os.WriteFile(
			scriptPath,
			fmt.Appendf(nil, "@echo off\r\necho started>\"%s\"", strings.ReplaceAll(readyPath, "%", "%%")),
			0o600,
		))
	} else {
		scriptPath = filepath.Join(tempDir, "mark.sh")
		//nolint:gosec // G306: test script needs execute permission
		require.NoError(t, os.WriteFile(
			scriptPath,
			fmt.Appendf(nil, "#!/bin/sh\nprintf started > '%s'", strings.ReplaceAll(readyPath, "'", "'\\''")),
			0o700,
		))
	}

	e, err := New(Config{})
	require.NoError(t, err)
	err = e.Execute(ctx, scriptPath)

	require.ErrorIs(t, err, context.Canceled)
	_, statErr := os.Stat(readyPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

// --- Integration tests requiring real shells ---

func TestExecuteDirect_GoVersion(t *testing.T) {
	// "go" must be on PATH for this test
	if _, err := os.Stat(goExePath()); err != nil {
		t.Skip("go not available on PATH")
	}

	e, err := New(Config{})
	require.NoError(t, err)

	err = e.ExecuteDirect(t.Context(), "go", []string{"version"})
	require.NoError(t, err)
}

func TestExecute_ValidScript(t *testing.T) {
	dir := t.TempDir()
	var scriptPath string

	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(dir, "test.cmd")
		require.NoError(t, os.WriteFile(
			scriptPath,
			[]byte("@echo off\r\necho hello"),
			0o600,
		))
	} else {
		scriptPath = filepath.Join(dir, "test.sh")
		//nolint:gosec // G306 test script needs execute permission
		require.NoError(t, os.WriteFile(
			scriptPath,
			[]byte("#!/bin/bash\necho hello"),
			0o700,
		))
	}

	e, err := New(Config{})
	require.NoError(t, err)
	require.NoError(t, e.Execute(t.Context(), scriptPath))
}

func TestExecuteInline_Valid(t *testing.T) {
	shell := platformShell()
	e, err := New(Config{Shell: shell})
	require.NoError(t, err)

	require.NoError(t, e.ExecuteInline(t.Context(), "echo hello"))
}

func TestExecute_WithExplicitShell(t *testing.T) {
	dir := t.TempDir()
	var scriptPath, shell string

	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(dir, "test.ps1")
		require.NoError(t, os.WriteFile(
			scriptPath,
			[]byte("Write-Host 'hello'"),
			0o600,
		))
		shell = "powershell"
	} else {
		scriptPath = filepath.Join(dir, "test.sh")
		//nolint:gosec // G306 test script needs execute permission
		require.NoError(t, os.WriteFile(
			scriptPath,
			[]byte("#!/bin/bash\necho hello"),
			0o700,
		))
		shell = "bash"
	}

	e, err := New(Config{Shell: shell})
	require.NoError(t, err)
	require.NoError(t, e.Execute(t.Context(), scriptPath))
}

func TestExecute_ScriptWithArgs(t *testing.T) {
	dir := t.TempDir()
	var scriptPath string

	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(dir, "args.cmd")
		require.NoError(t, os.WriteFile(
			scriptPath,
			[]byte("@echo off\r\necho %1"),
			0o600,
		))
	} else {
		scriptPath = filepath.Join(dir, "args.sh")
		//nolint:gosec // G306 test script needs execute permission
		require.NoError(t, os.WriteFile(
			scriptPath,
			[]byte("#!/bin/bash\necho $1"),
			0o700,
		))
	}

	e, err := New(Config{Args: []string{"test-arg"}})
	require.NoError(t, err)
	require.NoError(t, e.Execute(t.Context(), scriptPath))
}

func TestExecuteDirect_ExitCodePropagation(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	var cmdErr error
	if runtime.GOOS == "windows" {
		cmdErr = e.ExecuteDirect(
			t.Context(), "cmd", []string{"/c", "exit 42"},
		)
	} else {
		cmdErr = e.ExecuteDirect(
			t.Context(), "bash", []string{"-c", "exit 42"},
		)
	}

	require.Error(t, cmdErr)
	execErr, ok := errors.AsType[*ExecutionError](cmdErr)
	require.True(t, ok, "expected ExecutionError, got %T", cmdErr)
	assert.Equal(t, 42, execErr.ExitCode)
}

func TestExecuteDirect_InterruptExit(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	var cmdErr error
	if runtime.GOOS == "windows" {
		cmdErr = e.ExecuteDirect(
			t.Context(), "cmd", []string{"/c", "exit -1073741510"},
		)
	} else {
		cmdErr = e.ExecuteDirect(
			t.Context(), "sh", []string{"-c", "kill -INT $$"},
		)
	}

	require.Error(t, cmdErr)
	require.IsType(t, &ExecutionError{}, cmdErr)
	execErr, ok := errors.AsType[*ExecutionError](cmdErr)
	require.True(t, ok)
	assert.True(t, execErr.Interrupted())
}

func TestExecuteInline_ExitCodePropagation(t *testing.T) {
	shell := platformShell()
	e, err := New(Config{Shell: shell})
	require.NoError(t, err)

	var script string
	if runtime.GOOS == "windows" {
		script = "exit 42"
	} else {
		script = "exit 42"
	}

	cmdErr := e.ExecuteInline(t.Context(), script)
	require.Error(t, cmdErr)

	execErr, ok := errors.AsType[*ExecutionError](cmdErr)
	require.True(t, ok, "expected ExecutionError, got %T", cmdErr)
	assert.Equal(t, 42, execErr.ExitCode)
}

func TestExecute_Cancellation(t *testing.T) {
	for _, mode := range scriptingExecutionModes() {
		t.Run(mode, func(t *testing.T) {
			readyPath := filepath.Join(t.TempDir(), "ready")
			execute := prepareScriptingHelperExecution(t, mode, []string{
				scriptingHelperModeEnv + "=wait",
				scriptingHelperReadyEnv + "=" + readyPath,
			})
			ctx, cancel := context.WithCancelCause(t.Context())
			cause := errors.New("custom cancellation cause")

			response := make(chan error, 1)
			go func() {
				response <- execute(ctx)
			}()

			waitForScriptingPath(t, readyPath)
			cancel(cause)

			err := waitForScriptingResponse(t, response)
			require.ErrorIs(t, err, cause)
			require.NotErrorIs(t, err, context.Canceled)
			execErr, ok := errors.AsType[*ExecutionError](err)
			require.True(t, ok)
			require.NotZero(t, execErr.ExitCode)
		})
	}
}

func TestExecute_DeadlineExceeded(t *testing.T) {
	for _, mode := range scriptingExecutionModes() {
		t.Run(mode, func(t *testing.T) {
			readyPath := filepath.Join(t.TempDir(), "ready")
			execute := prepareScriptingHelperExecution(t, mode, []string{
				scriptingHelperModeEnv + "=wait",
				scriptingHelperReadyEnv + "=" + readyPath,
			})
			ctx := newManualDeadlineContext(t.Context())

			response := make(chan error, 1)
			go func() {
				response <- execute(ctx)
			}()

			waitForScriptingPath(t, readyPath)
			ctx.expire()

			err := waitForScriptingResponse(t, response)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			_, ok := errors.AsType[*ExecutionError](err)
			require.True(t, ok)
		})
	}
}

func TestExecuteDirect_BackgroundChildLifecycle(t *testing.T) {
	t.Run("normal completion preserves child", func(t *testing.T) {
		tempDir := t.TempDir()
		addressPath := filepath.Join(tempDir, "address")
		pidPath := filepath.Join(tempDir, "pid")
		execute := prepareScriptingHelperExecution(t, "direct", []string{
			scriptingHelperModeEnv + "=spawn-listener-and-exit",
			scriptingHelperAddressEnv + "=" + addressPath,
			scriptingHelperPIDEnv + "=" + pidPath,
		})

		require.NoError(t, execute(t.Context()))
		waitForScriptingPath(t, addressPath)
		waitForScriptingPath(t, pidPath)

		address := strings.TrimSpace(string(mustReadScriptingFile(t, addressPath)))
		requireListenerRunning(t, address)
		killScriptingProcess(t, pidPath)
		requireListenerStopped(t, address)
	})

	t.Run("cancellation terminates child", func(t *testing.T) {
		tempDir := t.TempDir()
		addressPath := filepath.Join(tempDir, "address")
		pidPath := filepath.Join(tempDir, "pid")
		execute := prepareScriptingHelperExecution(t, "direct", []string{
			scriptingHelperModeEnv + "=spawn-listener-and-wait",
			scriptingHelperAddressEnv + "=" + addressPath,
			scriptingHelperPIDEnv + "=" + pidPath,
		})
		ctx, cancel := context.WithCancel(t.Context())

		response := make(chan error, 1)
		go func() {
			response <- execute(ctx)
		}()

		waitForScriptingPath(t, addressPath)
		cancel()

		require.ErrorIs(t, waitForScriptingResponse(t, response), context.Canceled)
		address := strings.TrimSpace(string(mustReadScriptingFile(t, addressPath)))
		requireListenerStopped(t, address)
	})

	t.Run("interrupt exit terminates child", func(t *testing.T) {
		tempDir := t.TempDir()
		addressPath := filepath.Join(tempDir, "address")
		pidPath := filepath.Join(tempDir, "pid")
		env := append(os.Environ(),
			scriptingHelperModeEnv+"=spawn-listener-and-interrupt",
			scriptingHelperAddressEnv+"="+addressPath,
			scriptingHelperPIDEnv+"="+pidPath,
		)
		executor, err := New(Config{Interactive: true, Env: env})
		require.NoError(t, err)

		cmdErr := executor.ExecuteDirect(
			t.Context(),
			os.Args[0],
			[]string{"-test.run=TestScriptingHelperProcess"},
		)

		require.Error(t, cmdErr)
		execErr, ok := errors.AsType[*ExecutionError](cmdErr)
		require.True(t, ok)
		require.True(t, execErr.Interrupted())

		address := strings.TrimSpace(string(mustReadScriptingFile(t, addressPath)))
		t.Cleanup(func() {
			conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				_ = stopScriptingProcess(pidPath)
			}
		})
		requireListenerStopped(t, address)
	})
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

func TestScriptingHelperProcess(t *testing.T) {
	switch os.Getenv(scriptingHelperModeEnv) {
	case "":
		return
	case "mark-start":
		//nolint:gosec // G703: path is supplied by the parent test process
		require.NoError(t, os.WriteFile(
			os.Getenv(scriptingHelperReadyEnv),
			[]byte("started"),
			0o600,
		))
	case "wait":
		//nolint:gosec // G703: path is supplied by the parent test process
		require.NoError(t, os.WriteFile(
			os.Getenv(scriptingHelperReadyEnv),
			[]byte("ready"),
			0o600,
		))
		for {
			time.Sleep(time.Hour)
		}
	case "spawn-listener-and-exit", "spawn-listener-and-wait", "spawn-listener-and-interrupt":
		child := osexec.Command(os.Args[0], "-test.run=TestScriptingHelperProcess") //nolint:gosec
		child.Env = append(os.Environ(),
			scriptingHelperModeEnv+"=listen",
			scriptingHelperAddressEnv+"="+os.Getenv(scriptingHelperAddressEnv),
		)
		require.NoError(t, child.Start())
		//nolint:gosec // G703: path is supplied by the parent test process
		require.NoError(t, os.WriteFile(
			os.Getenv(scriptingHelperPIDEnv),
			fmt.Appendf(nil, "%d", child.Process.Pid),
			0o600,
		))
		waitForScriptingPath(t, os.Getenv(scriptingHelperAddressEnv))
		if os.Getenv(scriptingHelperModeEnv) == "spawn-listener-and-wait" {
			for {
				time.Sleep(time.Hour)
			}
		}
		if os.Getenv(scriptingHelperModeEnv) == "spawn-listener-and-interrupt" {
			os.Exit(130)
		}
	case "listen":
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer listener.Close()
		//nolint:gosec // G703: path is supplied by the parent test process
		require.NoError(t, os.WriteFile(
			os.Getenv(scriptingHelperAddressEnv),
			[]byte(listener.Addr().String()),
			0o600,
		))
		for {
			time.Sleep(time.Hour)
		}
	default:
		t.Fatalf("unknown scripting helper mode")
	}
}

// --- helpers ---

func scriptingExecutionModes() []string {
	return []string{"direct", "inline", "file"}
}

func prepareScriptingHelperExecution(
	t *testing.T,
	mode string,
	additionalEnv []string,
) func(context.Context) error {
	t.Helper()

	env := append(os.Environ(), scriptingHelperExeEnv+"="+os.Args[0])
	env = append(env, additionalEnv...)
	config := Config{Env: env}

	switch mode {
	case "direct":
		executor, err := New(config)
		require.NoError(t, err)
		return func(ctx context.Context) error {
			return executor.ExecuteDirect(
				ctx,
				os.Args[0],
				[]string{"-test.run=TestScriptingHelperProcess"},
			)
		}
	case "inline":
		config.Shell = platformShell()
		executor, err := New(config)
		require.NoError(t, err)
		command := scriptingHelperCommand()
		return func(ctx context.Context) error {
			return executor.ExecuteInline(ctx, command)
		}
	case "file":
		config.Shell = platformShell()
		executor, err := New(config)
		require.NoError(t, err)
		scriptPath := filepath.Join(t.TempDir(), scriptingHelperScriptName())
		//nolint:gosec // G306: the POSIX test script needs execute permission
		require.NoError(t, os.WriteFile(
			scriptPath,
			[]byte(scriptingHelperScript()),
			0o700,
		))
		return func(ctx context.Context) error {
			return executor.Execute(ctx, scriptPath)
		}
	default:
		t.Fatalf("unknown scripting execution mode %q", mode)
		return nil
	}
}

func scriptingHelperCommand() string {
	if runtime.GOOS == "windows" {
		return `"%` + scriptingHelperExeEnv + `%" -test.run=TestScriptingHelperProcess`
	}
	return `"$` + scriptingHelperExeEnv + `" -test.run=TestScriptingHelperProcess`
}

func scriptingHelperScriptName() string {
	if runtime.GOOS == "windows" {
		return "helper.cmd"
	}
	return "helper.sh"
}

func scriptingHelperScript() string {
	command := scriptingHelperCommand()
	if runtime.GOOS == "windows" {
		return "@echo off\r\n" + command + "\r\n"
	}
	return "#!/bin/sh\n" + command + "\n"
}

func waitForScriptingPath(t *testing.T, path string) {
	t.Helper()
	require.Eventually(t, func() bool {
		//nolint:gosec // G703: path is supplied by the parent test process
		_, err := os.Stat(path)
		return err == nil
	}, 10*time.Second, 10*time.Millisecond)
}

func waitForScriptingResponse(t *testing.T, response <-chan error) error {
	t.Helper()
	select {
	case err := <-response:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for script execution")
		return nil
	}
}

func mustReadScriptingFile(t *testing.T, path string) []byte {
	t.Helper()
	//nolint:gosec // G703: path is supplied by the test process
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return content
}

func requireListenerRunning(t *testing.T, address string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func requireListenerStopped(t *testing.T, address string) {
	t.Helper()
	require.Eventually(t, func() bool {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return false
		}
		return listener.Close() == nil
	}, 10*time.Second, 20*time.Millisecond)
}

func killScriptingProcess(t *testing.T, pidPath string) {
	t.Helper()
	require.NoError(t, stopScriptingProcess(pidPath))
}

func stopScriptingProcess(pidPath string) error {
	//nolint:gosec // G703: path is supplied by the parent test process
	content, err := os.ReadFile(pidPath)
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		return err
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

func platformShell() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "bash"
}

func goExePath() string {
	name := "go"
	if runtime.GOOS == "windows" {
		name = "go.exe"
	}

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil { //nolint:gosec // G703: test helper, path from PATH env var
			return p
		}
	}
	return name
}
