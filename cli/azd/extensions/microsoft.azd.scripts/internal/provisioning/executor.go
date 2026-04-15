// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ScriptResult holds the outcome of a single script execution.
type ScriptResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// ScriptExecutor runs shell scripts with a prepared environment.
type ScriptExecutor struct {
	projectPath string
}

// NewScriptExecutor creates a new ScriptExecutor rooted at the given project path.
func NewScriptExecutor(projectPath string) *ScriptExecutor {
	return &ScriptExecutor{projectPath: projectPath}
}

// Execute runs a single script with the provided environment and streams output to stdout/stderr.
func (e *ScriptExecutor) Execute(
	ctx context.Context,
	sc *ScriptConfig,
	env map[string]string,
) (*ScriptResult, error) {
	scriptPath := filepath.Join(e.projectPath, sc.Run)

	shell, args := buildShellCommand(sc.Kind, scriptPath)

	cmd := exec.CommandContext(ctx, shell, args...) //nolint:gosec // shell and args are validated during config parsing
	cmd.Dir = filepath.Dir(scriptPath)
	cmd.Env = mapToEnvSlice(env)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()

	result := &ScriptResult{}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}

	if err != nil {
		return result, fmt.Errorf("script %q exited with code %d: %w", sc.Run, result.ExitCode, err)
	}

	return result, nil
}

// buildShellCommand returns the shell binary and arguments for the given kind.
func buildShellCommand(kind, scriptPath string) (string, []string) {
	switch strings.ToLower(kind) {
	case "pwsh":
		return pwshBinary(), []string{"-NoProfile", "-NonInteractive", "-File", scriptPath}
	default: // "sh"
		return shBinary(), []string{scriptPath}
	}
}

func pwshBinary() string {
	if runtime.GOOS == "windows" {
		return "pwsh.exe"
	}
	return "pwsh"
}

func shBinary() string {
	if runtime.GOOS == "windows" {
		// Prefer Git Bash on Windows
		if gitBash, err := exec.LookPath("bash.exe"); err == nil {
			return gitBash
		}
		return "bash.exe"
	}
	return "/bin/bash"
}

// mapToEnvSlice converts a map to the KEY=VALUE slice format expected by exec.Cmd.Env.
func mapToEnvSlice(env map[string]string) []string {
	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, k+"="+v)
	}
	return result
}
