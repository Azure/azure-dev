// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package powershell

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
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
	tempFile      string // temp script created from inline content
}

// Prepare validates that PowerShell is available. Tries pwsh first,
// falls back to powershell on Windows. Returns an error with install
// guidance if neither is found. When the execution context carries
// inline script content, a temp .ps1 file is created.
func (p *powershellExecutor) Prepare(
	_ context.Context, _ string, execCtx tools.ExecutionContext,
) error {
	// Resolve PowerShell binary.
	if err := p.resolvePowerShell(); err != nil {
		return err
	}

	// Create temp file for inline scripts.
	if execCtx.InlineScript != "" {
		tmpFile, err := os.CreateTemp(
			"", fmt.Sprintf("azd-%s-*.ps1", execCtx.HookName),
		)
		if err != nil {
			return fmt.Errorf("creating temp script: %w", err)
		}

		content := "$ErrorActionPreference = 'Stop'\n\n" +
			"# Auto generated file from Azure Developer CLI\n" +
			execCtx.InlineScript + "\n" +
			"if ((Test-Path -LiteralPath variable:\\LASTEXITCODE)) " +
			"{ exit $LASTEXITCODE }\n"

		if err := os.WriteFile(
			tmpFile.Name(), []byte(content), osutil.PermissionExecutableFile,
		); err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return fmt.Errorf("writing temp script: %w", err)
		}

		tmpFile.Close()
		p.tempFile = tmpFile.Name()
	}

	return nil
}

// resolvePowerShell locates pwsh or powershell in PATH.
func (p *powershellExecutor) resolvePowerShell() error {
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

// Execute runs the PowerShell script using the shell resolved in
// Prepare. When a temp file was created during Prepare it is used
// instead of the provided path.
func (p *powershellExecutor) Execute(
	ctx context.Context, path string, execCtx tools.ExecutionContext,
) (exec.RunResult, error) {
	if p.tempFile != "" {
		path = p.tempFile
	}

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

// Cleanup removes any temporary script file created during Prepare.
func (p *powershellExecutor) Cleanup(_ context.Context) error {
	if p.tempFile != "" {
		err := os.Remove(p.tempFile)
		p.tempFile = ""
		return err
	}
	return nil
}
