// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewRootCommand(t *testing.T) {
	cmd := NewRootCommand()
	if cmd == nil {
		t.Fatal("NewRootCommand returned nil")
	}
	if cmd.Use == "" {
		t.Error("root command Use should not be empty")
	}

	// Check that expected subcommands exist
	subNames := map[string]bool{}
	for _, sub := range cmd.Commands() {
		subNames[sub.Name()] = true
	}

	if !subNames["version"] {
		t.Error("expected subcommand \"version\" not found")
	}
}

func TestRootCommand_Flags(t *testing.T) {
	cmd := NewRootCommand()

	shellFlag := cmd.Flags().Lookup("shell")
	if shellFlag == nil {
		t.Fatal("expected --shell flag")
	}
	if shellFlag.Shorthand != "s" {
		t.Errorf("expected -s shorthand, got %q", shellFlag.Shorthand)
	}

	interactiveFlag := cmd.Flags().Lookup("interactive")
	if interactiveFlag == nil {
		t.Fatal("expected --interactive flag")
	}
	if interactiveFlag.Shorthand != "i" {
		t.Errorf("expected -i shorthand, got %q", interactiveFlag.Shorthand)
	}
}

func TestRootCommand_RunE_InlineExecution(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetContext(t.Context())

	if err := cmd.RunE(cmd, []string{"echo inline-test"}); err != nil {
		t.Fatalf("RunE inline execution failed: %v", err)
	}
}

func TestRootCommand_RunE_FileExecution(t *testing.T) {
	dir := t.TempDir()
	var scriptPath string
	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(dir, "test.cmd")
		if err := os.WriteFile(scriptPath, []byte("@echo off\r\necho file-test"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		scriptPath = filepath.Join(dir, "test.sh")
		if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho file-test"), 0o700); err != nil { //nolint:gosec // G306 test script needs execute permission
			t.Fatal(err)
		}
	}

	cmd := NewRootCommand()
	cmd.SetContext(t.Context())

	if err := cmd.RunE(cmd, []string{scriptPath}); err != nil {
		t.Fatalf("RunE file execution failed: %v", err)
	}
}

func TestRootCommand_RunE_WithArgs(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetContext(t.Context())

	// Multi-arg with no --shell routes to direct exec (exact argv).
	// Use a real executable: cmd /c echo on Windows, echo on Unix.
	var args []string
	if runtime.GOOS == "windows" {
		args = []string{"cmd", "/c", "echo", "arg1", "arg2"}
	} else {
		args = []string{"echo", "arg1", "arg2"}
	}

	if err := cmd.RunE(cmd, args); err != nil {
		t.Fatalf("RunE with args failed: %v", err)
	}
}

func TestRootCommand_PersistentPreRunE_Debug(t *testing.T) {
	t.Setenv("AZD_DEBUG", "")

	cmd := NewRootCommand()
	cmd.SetContext(t.Context())
	cmd.SetArgs([]string{"--debug", "version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute with --debug version failed: %v", err)
	}

	if os.Getenv("AZD_DEBUG") != "true" {
		t.Error("expected AZD_DEBUG to be set to 'true'")
	}
}

func TestRootCommand_PersistentPreRunE_NoPrompt(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "")

	cmd := NewRootCommand()
	cmd.SetContext(t.Context())
	cmd.SetArgs([]string{"--no-prompt", "version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute with --no-prompt version failed: %v", err)
	}

	if os.Getenv("AZD_NO_PROMPT") != "true" {
		t.Error("expected AZD_NO_PROMPT to be set to 'true'")
	}
}
