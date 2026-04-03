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

// NewPowershellScript creates a new ScriptExecutor for PowerShell scripts.
func NewPowershellScript(commandRunner exec.CommandRunner, cwd string, envVars []string) tools.ScriptExecutor {
	return &powershellScript{
		commandRunner: commandRunner,
		cwd:           cwd,
		envVars:       envVars,
		shellCmd:      "pwsh", // default, resolved in Prepare
	}
}

type powershellScript struct {
	commandRunner exec.CommandRunner
	cwd           string
	envVars       []string
	shellCmd      string // resolved in Prepare: "pwsh" or "powershell"
}

// Prepare validates that PowerShell is available. Tries pwsh first,
// falls back to powershell on Windows. Returns an error with install
// guidance if neither is found.
func (ps *powershellScript) Prepare(_ context.Context, _ string) error {
	// Try pwsh first.
	if ps.commandRunner.ToolInPath("pwsh") == nil {
		ps.shellCmd = "pwsh"
		return nil
	}

	// On Windows, fall back to powershell (PS5).
	if runtime.GOOS == "windows" {
		if ps.commandRunner.ToolInPath("powershell") == nil {
			ps.shellCmd = "powershell"
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
func (ps *powershellScript) Execute(
	ctx context.Context, path string, options tools.ExecOptions,
) (exec.RunResult, error) {
	runArgs := exec.NewRunArgs(ps.shellCmd, path).
		WithCwd(ps.cwd).
		WithEnv(ps.envVars).
		WithShell(true)

	if options.Interactive != nil {
		runArgs = runArgs.WithInteractive(*options.Interactive)
	}

	if options.StdOut != nil {
		runArgs = runArgs.WithStdOut(options.StdOut)
	}

	return ps.commandRunner.Run(ctx, runArgs)
}
