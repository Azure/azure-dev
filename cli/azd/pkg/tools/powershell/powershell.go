// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package powershell provides functionality to execute PowerShell scripts.
//
// PowerShell 5 Fallback Feature:
// When the alpha feature "powershell.fallback5" is enabled, the PowerShell script runner
// will attempt to fallback to PowerShell 5 (powershell.exe) if PowerShell 7 (pwsh.exe)
// is not available.
//
// To enable this feature:
//   azd config set alpha.powershell.fallback5 on
//
// To disable this feature:
//   azd config set alpha.powershell.fallback5 off
//
// When enabled:
// - First tries to use PowerShell 7 (pwsh)
// - If PowerShell 7 is not available, falls back to PowerShell 5 (powershell)
// - If neither is available, returns an appropriate error message
//
// When disabled (default):
// - Only tries to use PowerShell 7 (pwsh)
// - If PowerShell 7 is not available, returns an error with instructions to install PowerShell 7

package powershell

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// Creates a new PowershellScript command runner
func NewPowershellScript(commandRunner exec.CommandRunner, cwd string, envVars []string, alphaFeaturesManager *alpha.FeatureManager) tools.Script {
	return &powershellScript{
		commandRunner:        commandRunner,
		cwd:                  cwd,
		envVars:              envVars,
		checkInstalled:       checkPath,
		alphaFeaturesManager: alphaFeaturesManager,
	}
}

// for testing
func NewPowershellScriptWithMockCheckPath(
	commandRunner exec.CommandRunner,
	cwd string,
	envVars []string,
	mockCheckPath checkInstalled,
	alphaFeaturesManager *alpha.FeatureManager) tools.Script {
	return &powershellScript{
		commandRunner:        commandRunner,
		cwd:                  cwd,
		envVars:              envVars,
		checkInstalled:       mockCheckPath,
		alphaFeaturesManager: alphaFeaturesManager,
	}
}

type powershellScript struct {
	commandRunner        exec.CommandRunner
	cwd                  string
	envVars              []string
	checkInstalled       checkInstalled
	alphaFeaturesManager *alpha.FeatureManager
}

type checkInstalled func(options tools.ExecOptions, enableFallback bool) (command string, err error)

func checkPath(options tools.ExecOptions, enableFallback bool) (command string, err error) {
	// First try pwsh (PowerShell 7)
	pwshCommand := strings.Split(options.UserPwsh, " ")[0]
	err = tools.ToolInPath(pwshCommand)
	if err == nil {
		return options.UserPwsh, nil
	}

	// If pwsh is not available and fallback is enabled, try powershell (PowerShell 5)
	if enableFallback {
		err = tools.ToolInPath("powershell")
		if err == nil {
			return "powershell", nil
		}
	}

	// Return original error if no fallback or both failed
	return "", tools.ToolInPath(pwshCommand)
}

// Executes the specified powershell script
// When interactive is true will attach to stdin, stdout & stderr
func (bs *powershellScript) Execute(ctx context.Context, path string, options tools.ExecOptions) (exec.RunResult, error) {
	// Check if PowerShell 5 fallback is enabled
	enableFallback := bs.alphaFeaturesManager != nil && bs.alphaFeaturesManager.IsEnabled(alpha.FeatureId("powershell.fallback5"))

	command, err := bs.checkInstalled(options, enableFallback)
	if err != nil {
		suggestion := fmt.Sprintf("PowerShell 7 is not installed or not in the path. To install PowerShell 7, visit %s",
			output.WithLinkFormat("https://learn.microsoft.com/powershell/scripting/install/installing-powershell"))

		if enableFallback {
			suggestion = fmt.Sprintf("PowerShell is not available. Either install PowerShell 7 from %s or ensure PowerShell 5 is available",
				output.WithLinkFormat("https://learn.microsoft.com/powershell/scripting/install/installing-powershell"))
		}

		return exec.RunResult{}, &internal.ErrorWithSuggestion{
			Err:        err,
			Suggestion: suggestion,
		}
	}

	runArgs := exec.NewRunArgs(command, path).
		WithCwd(bs.cwd).
		WithEnv(bs.envVars).
		WithShell(true)

	if options.Interactive != nil {
		runArgs = runArgs.WithInteractive(*options.Interactive)
	}

	if options.StdOut != nil {
		runArgs = runArgs.WithStdOut(options.StdOut)
	}

	return bs.commandRunner.Run(ctx, runArgs)
}
