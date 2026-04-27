// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scripting

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// --- helpers ---

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
