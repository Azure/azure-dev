// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scripting

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Config holds the configuration for script execution.
type Config struct {
	Shell       string
	Interactive bool
	Args        []string
	// Env overrides the child process environment. When set, the child process
	// receives exactly these variables instead of inheriting os.Environ().
	// This prevents secrets from leaking into the parent process via os.Setenv.
	Env []string
}

// Validate checks if the Config has valid values.
func (c *Config) Validate() error {
	if err := ValidateShell(c.Shell); err != nil {
		return &InvalidShellError{Shell: c.Shell}
	}
	return nil
}

// Executor executes scripts and commands with azd context.
type Executor struct {
	config Config
}

// New creates a new script executor with the given configuration.
func New(config Config) (*Executor, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Executor{config: config}, nil
}

// Execute runs a script file.
func (e *Executor) Execute(ctx context.Context, scriptPath string) error {
	if scriptPath == "" {
		return &ValidationError{Field: "scriptPath", Reason: "cannot be empty"}
	}

	absPath, err := filepath.Abs(scriptPath)
	if err != nil {
		return &ValidationError{
			Field: "scriptPath", Reason: fmt.Sprintf("invalid path: %v", err),
		}
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ScriptNotFoundError{Path: filepath.Base(absPath)}
		}
		return &ValidationError{
			Field: "scriptPath", Reason: fmt.Sprintf("cannot access: %v", err),
		}
	}
	if info.IsDir() {
		return &ValidationError{
			Field: "scriptPath", Reason: "must be a file, not a directory",
		}
	}

	shell := e.config.Shell
	if shell == "" {
		shell = DetectShellFromFile(absPath)
	}

	workingDir := filepath.Dir(absPath)
	return e.executeCommand(ctx, shell, workingDir, absPath, false)
}

// ExecuteDirect runs a command directly without shell wrapping, preserving exact
// argv semantics. This is the preferred mode for "run a program with azd env".
func (e *Executor) ExecuteDirect(ctx context.Context, command string, args []string) error {
	if command == "" {
		return &ValidationError{Field: "command", Reason: "cannot be empty"}
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	cmd := exec.CommandContext(ctx, command, args...) //nolint:gosec
	cmd.Dir = workingDir
	cmd.Env = e.childEnv()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if os.Getenv("AZD_DEBUG") == "true" {
		quotedArgs := make([]string, len(args))
		for i, a := range args {
			quotedArgs[i] = fmt.Sprintf("%q", a)
		}
		fmt.Fprintf(os.Stderr,
			"Executing (direct): %s %s\n", command, strings.Join(quotedArgs, " "))
		fmt.Fprintf(os.Stderr, "Working directory: %q\n", workingDir)
	}

	return e.runCommand(cmd, command, "", false)
}

// ExecuteInline runs an inline script command.
func (e *Executor) ExecuteInline(ctx context.Context, scriptContent string) error {
	if strings.TrimSpace(scriptContent) == "" || !containsPrintable(scriptContent) {
		return &ValidationError{
			Field: "scriptContent", Reason: "must contain printable characters",
		}
	}

	shell := e.config.Shell
	if shell == "" {
		shell = DefaultShell()
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	return e.executeCommand(ctx, shell, workingDir, scriptContent, true)
}

func (e *Executor) executeCommand(
	ctx context.Context, shell, workingDir, scriptOrPath string, isInline bool,
) error {
	cmd := e.buildCommand(ctx, shell, scriptOrPath, isInline)
	cmd.Dir = workingDir
	cmd.Env = e.childEnv()

	if e.config.Interactive {
		cmd.Stdin = os.Stdin
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if os.Getenv("AZD_DEBUG") == "true" {
		e.logDebugInfo(shell, workingDir, scriptOrPath, isInline, cmd.Args)
	}

	return e.runCommand(cmd, scriptOrPath, shell, isInline)
}

func (e *Executor) logDebugInfo(
	shell, workingDir, scriptOrPath string, isInline bool, cmdArgs []string,
) {
	if isInline {
		fmt.Fprintf(os.Stderr, "Executing inline: %s\n", shell)
		fmt.Fprintf(os.Stderr, "Script length: %d chars\n", len(scriptOrPath))
	} else if len(cmdArgs) > 0 {
		quotedArgs := make([]string, len(cmdArgs)-1)
		for i, a := range cmdArgs[1:] {
			quotedArgs[i] = fmt.Sprintf("%q", a)
		}
		fmt.Fprintf(os.Stderr,
			"Executing: %s %s\n", shell, strings.Join(quotedArgs, " "))
	}
	fmt.Fprintf(os.Stderr, "Working directory: %q\n", workingDir)
}

// childEnv returns the environment for the child process. If Config.Env is
// set, it is used directly; otherwise os.Environ() is used as a fallback.
func (e *Executor) childEnv() []string {
	if len(e.config.Env) > 0 {
		return e.config.Env
	}
	return os.Environ()
}

// containsPrintable reports whether s contains at least one printable character
// (Unicode graphic or space). This rejects NUL-only and control-char-only input.
func containsPrintable(s string) bool {
	for _, r := range s {
		if r >= ' ' && r != 0x7F {
			return true
		}
	}
	return false
}

func (e *Executor) runCommand(
	cmd *exec.Cmd, scriptOrPath, shell string, isInline bool,
) error {
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return &ExecutionError{
				ExitCode: exitErr.ExitCode(),
				Shell:    shell,
				IsInline: isInline,
			}
		}
		if shell == "" {
			return fmt.Errorf("failed to execute command %q: %w", scriptOrPath, err)
		}
		if isInline {
			return fmt.Errorf(
				"failed to execute inline script with shell %q: %w", shell, err)
		}
		return fmt.Errorf(
			"failed to execute script %q with shell %q: %w",
			filepath.Base(scriptOrPath), shell, err)
	}
	return nil
}