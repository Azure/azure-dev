// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package executor

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errType any
	}{
		{"valid_empty_config", Config{}, false, nil},
		{"valid_bash", Config{Shell: "bash"}, false, nil},
		{"valid_pwsh", Config{Shell: "pwsh"}, false, nil},
		{"valid_cmd", Config{Shell: "cmd"}, false, nil},
		{"valid_sh", Config{Shell: "sh"}, false, nil},
		{"valid_zsh", Config{Shell: "zsh"}, false, nil},
		{"valid_powershell", Config{Shell: "powershell"}, false, nil},
		{"valid_uppercase", Config{Shell: "BASH"}, false, nil},
		{"invalid_shell", Config{Shell: "python"}, true, &InvalidShellError{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec, err := New(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if tt.errType != nil {
					if _, ok := errors.AsType[*InvalidShellError](err); !ok {
						t.Fatalf("expected InvalidShellError, got %T", err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if exec == nil {
				t.Fatal("executor should not be nil")
			}
		})
	}
}

func TestExecute_Validation(t *testing.T) {
	exec, _ := New(Config{})

	t.Run("empty_path", func(t *testing.T) {
		err := exec.Execute(t.Context(), "")
		if err == nil {
			t.Fatal("expected error")
		}
		valErr, ok := errors.AsType[*ValidationError](err)
		if !ok {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if valErr.Field != "scriptPath" {
			t.Fatalf("expected field 'scriptPath', got %q", valErr.Field)
		}
	})

	t.Run("nonexistent_file", func(t *testing.T) {
		err := exec.Execute(t.Context(), filepath.Join(t.TempDir(), "no-such-file.sh"))
		if err == nil {
			t.Fatal("expected error")
		}
		if _, ok := errors.AsType[*ScriptNotFoundError](err); !ok {
			t.Fatalf("expected ScriptNotFoundError, got %T: %v", err, err)
		}
	})

	t.Run("directory_path", func(t *testing.T) {
		err := exec.Execute(t.Context(), t.TempDir())
		if err == nil {
			t.Fatal("expected error")
		}
		valErr, ok := errors.AsType[*ValidationError](err)
		if !ok {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if !strings.Contains(valErr.Reason, "directory") {
			t.Fatalf("expected reason about directory, got %q", valErr.Reason)
		}
	})
}

func TestExecute_ValidScript(t *testing.T) {
	dir := t.TempDir()
	var scriptPath string
	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(dir, "test.cmd")
		if err := os.WriteFile(scriptPath, []byte("@echo off\r\necho hello"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		scriptPath = filepath.Join(dir, "test.sh")
		if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho hello"), 0o700); err != nil { //nolint:gosec // G306 test script needs execute permission
			t.Fatal(err)
		}
	}
	exec, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Execute(t.Context(), scriptPath); err != nil {
		t.Fatalf("unexpected error executing script: %v", err)
	}
}

func TestExecute_WithExplicitShell(t *testing.T) {
	dir := t.TempDir()
	var scriptPath, shell string
	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(dir, "test.ps1")
		if err := os.WriteFile(scriptPath, []byte("Write-Host 'hello'"), 0o600); err != nil {
			t.Fatal(err)
		}
		shell = "powershell"
	} else {
		scriptPath = filepath.Join(dir, "test.sh")
		if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho hello"), 0o700); err != nil { //nolint:gosec // G306 test script needs execute permission
			t.Fatal(err)
		}
		shell = "bash"
	}
	exec, err := New(Config{Shell: shell})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Execute(t.Context(), scriptPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecute_ScriptWithArgs(t *testing.T) {
	dir := t.TempDir()
	var scriptPath string
	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(dir, "args.cmd")
		if err := os.WriteFile(scriptPath, []byte("@echo off\r\necho %1"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		scriptPath = filepath.Join(dir, "args.sh")
		if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho $1"), 0o700); err != nil { //nolint:gosec // G306 test script needs execute permission
			t.Fatal(err)
		}
	}
	exec, err := New(Config{Args: []string{"test-arg"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Execute(t.Context(), scriptPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteInline_Validation(t *testing.T) {
	exec, _ := New(Config{})

	t.Run("empty_content", func(t *testing.T) {
		err := exec.ExecuteInline(t.Context(), "")
		if err == nil {
			t.Fatal("expected error")
		}
		if _, ok := errors.AsType[*ValidationError](err); !ok {
			t.Fatalf("expected ValidationError, got %T", err)
		}
	})

	t.Run("whitespace_only", func(t *testing.T) {
		err := exec.ExecuteInline(t.Context(), "   \t  ")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestExecuteInline_Valid(t *testing.T) {
	shell := "cmd"
	if runtime.GOOS != "windows" {
		shell = "bash"
	}
	exec, err := New(Config{Shell: shell})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.ExecuteInline(t.Context(), "echo hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteInline_DefaultShell(t *testing.T) {
	exec, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.ExecuteInline(t.Context(), "echo default-shell"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteInline_FailingCommand(t *testing.T) {
	var shell, cmd string
	if runtime.GOOS == "windows" {
		shell = "cmd"
		cmd = "exit /b 42"
	} else {
		shell = "bash"
		cmd = "exit 42"
	}
	exec, err := New(Config{Shell: shell})
	if err != nil {
		t.Fatal(err)
	}
	err = exec.ExecuteInline(t.Context(), cmd)
	if err == nil {
		t.Fatal("expected error for failing command")
	}
	execErr, ok := errors.AsType[*ExecutionError](err)
	if !ok {
		t.Fatalf("expected ExecutionError, got %T: %v", err, err)
	}
	if execErr.ExitCode != 42 {
		t.Fatalf("expected exit code 42, got %d", execErr.ExitCode)
	}
}

func TestExecute_DebugLogging(t *testing.T) {
	t.Setenv("AZD_DEBUG", "true")
	dir := t.TempDir()
	var scriptPath string
	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(dir, "debug.cmd")
		if err := os.WriteFile(scriptPath, []byte("@echo off\r\necho debug"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		scriptPath = filepath.Join(dir, "debug.sh")
		if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho debug"), 0o700); err != nil { //nolint:gosec // G306 test script needs execute permission
			t.Fatal(err)
		}
	}
	exec, _ := New(Config{})
	if err := exec.Execute(t.Context(), scriptPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteInline_DebugLogging(t *testing.T) {
	t.Setenv("AZD_DEBUG", "true")
	shell := "cmd"
	if runtime.GOOS != "windows" {
		shell = "bash"
	}
	exec, _ := New(Config{Shell: shell})
	if err := exec.ExecuteInline(t.Context(), "echo debug-inline"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- runCommand error paths ---

func TestRunCommand_NonExitError_Inline(t *testing.T) {
	// Use a command that does not exist to get a non-ExitError failure
	e := &Executor{config: Config{}}
	cmd := e.buildCommand(t.Context(), "nonexistent-shell-binary-12345", "echo hi", true)
	err := e.runCommand(cmd, "echo hi", "nonexistent-shell-binary-12345", true)
	if err == nil {
		t.Fatal("expected error from nonexistent binary")
	}
	if !strings.Contains(err.Error(), "failed to execute inline script") {
		t.Fatalf("expected inline error message, got: %v", err)
	}
}

func TestRunCommand_NonExitError_File(t *testing.T) {
	e := &Executor{config: Config{}}
	cmd := e.buildCommand(t.Context(), "nonexistent-shell-binary-12345", "/tmp/script.sh", false)
	err := e.runCommand(cmd, "/tmp/script.sh", "nonexistent-shell-binary-12345", false)
	if err == nil {
		t.Fatal("expected error from nonexistent binary")
	}
	if !strings.Contains(err.Error(), "failed to execute script") {
		t.Fatalf("expected file error message, got: %v", err)
	}
}

func TestExecuteCommand_Interactive(t *testing.T) {

	exec, err := New(Config{Interactive: true})
	if err != nil {
		t.Fatal(err)
	}

	var shell string
	if runtime.GOOS == "windows" {
		shell = "cmd"
	} else {
		shell = "bash"
	}

	dir := t.TempDir()
	// Execute a simple command in interactive mode — verifies stdin wiring doesn't crash
	err = exec.executeCommand(t.Context(), shell, dir, "echo interactive-test", true)
	if err != nil {
		t.Fatalf("interactive executeCommand failed: %v", err)
	}
}

func TestExecuteCommand_DebugLogging(t *testing.T) {
	t.Setenv("AZD_DEBUG", "true")

	exec, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}

	var shell string
	if runtime.GOOS == "windows" {
		shell = "cmd"
	} else {
		shell = "bash"
	}

	dir := t.TempDir()
	err = exec.executeCommand(t.Context(), shell, dir, "echo debug-test", true)
	if err != nil {
		t.Fatalf("executeCommand with debug failed: %v", err)
	}
}
