// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package powershell

import (
	"context"
	"fmt"
	"runtime"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// NewExecutor creates a PowerShell HookExecutor. Takes only IoC-injectable deps.
func NewExecutor(commandRunner exec.CommandRunner) tools.HookExecutor {
	return &powershellExecutor{
		commandRunner: commandRunner,
		shellCmd:      "pwsh", // default, resolved in Prepare
	}
}

type powershellExecutor struct {
	commandRunner exec.CommandRunner
	shellCmd      string // resolved in Prepare: "pwsh" or "powershell"
}

// Prepare validates that PowerShell is available. Tries pwsh first,
// falls back to powershell on Windows. Returns an error with install
// guidance if neither is found.
func (p *powershellExecutor) Prepare(
	_ context.Context, _ string, _ tools.ExecutionContext,
) error {
	// Try pwsh first.
	if p.commandRunner.ToolInPath("pwsh") == nil {
		p.shellCmd = "pwsh"
		return nil
	}

	// On Windows, fall back to powershell (PS5).
	if runtime.GOOS == "windows" {
		if p.commandRunner.ToolInPath("powershell") == nil {
			p.shellCmd = "powershell"
			return nil
		}
		return &internal.ErrorWithSuggestion{
			Err: fmt.Errorf("neither pwsh nor powershell found in PATH"),
			Suggestion: fmt.Sprintf(
				"Make sure pwsh (PowerShell 7) or powershell (PowerShell 5) is installed. Visit %s",
				output.WithLinkFormat(
					"https://learn.microsoft.com/powershell/scripting/install/installing-powershell",
				)),
		}
	}

	// Non-Windows: pwsh is required.
	return &internal.ErrorWithSuggestion{
		Err: fmt.Errorf("pwsh not found in PATH"),
		Suggestion: fmt.Sprintf(
			"PowerShell 7 is not installed or not in the path. Visit %s",
			output.WithLinkFormat(
				"https://learn.microsoft.com/powershell/scripting/install/installing-powershell",
			)),
	}
}

// Execute runs the PowerShell script using the shell resolved in Prepare.
func (p *powershellExecutor) Execute(
	ctx context.Context, path string, execCtx tools.ExecutionContext,
) (exec.RunResult, error) {
	runArgs := exec.NewRunArgs(p.shellCmd, path).
		WithCwd(execCtx.Cwd).
		WithEnv(execCtx.EnvVars).
		WithShell(true)

	if execCtx.Interactive != nil {
		runArgs = runArgs.WithInteractive(*execCtx.Interactive)
	}

	if execCtx.StdOut != nil {
		runArgs = runArgs.WithStdOut(execCtx.StdOut)
	}

	return p.commandRunner.Run(ctx, runArgs)
}
