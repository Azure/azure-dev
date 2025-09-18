// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package powershell

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// Creates a new PowershellScript command runner
func NewPowershellScript(commandRunner exec.CommandRunner, cwd string, envVars []string) tools.Script {
	return &powershellScript{
		commandRunner: commandRunner,
		cwd:           cwd,
		envVars:       envVars,
	}
}

type powershellScript struct {
	commandRunner exec.CommandRunner
	cwd           string
	envVars       []string
}

func (ps *powershellScript) checkPath(options tools.ExecOptions) error {
	return ps.commandRunner.ToolInPath(strings.Split(options.UserPwsh, " ")[0])
}

// Executes the specified powershell script
// When interactive is true will attach to stdin, stdout & stderr
func (ps *powershellScript) Execute(ctx context.Context, path string, options tools.ExecOptions) (exec.RunResult, error) {
	noPwshError := ps.checkPath(options)
	if noPwshError != nil {

		if runtime.GOOS != "windows" {
			return exec.RunResult{}, &internal.ErrorWithSuggestion{
				Err: noPwshError,
				Suggestion: fmt.Sprintf(
					"PowerShell 7 is not installed or not in the path. To install PowerShell 7, visit %s",
					output.WithLinkFormat("https://learn.microsoft.com/powershell/scripting/install/installing-powershell")),
			}
		}

		options.UserPwsh = "powershell"
		if err := ps.checkPath(options); err != nil {
			return exec.RunResult{}, &internal.ErrorWithSuggestion{
				Err: err,
				Suggestion: fmt.Sprintf(
					"Make sure pwsh (PowerShell 7) or powershell (PowerShell 5) is installed on your system, visit %s",
					output.WithLinkFormat("https://learn.microsoft.com/powershell/scripting/install/installing-powershell")),
			}
		}
	}

	runArgs := exec.NewRunArgs(options.UserPwsh, path).
		WithCwd(ps.cwd).
		WithEnv(ps.envVars).
		WithShell(true)

	if options.Interactive != nil {
		runArgs = runArgs.WithInteractive(*options.Interactive)
	}

	if options.StdOut != nil {
		runArgs = runArgs.WithStdOut(options.StdOut)
	}

	result, err := ps.commandRunner.Run(ctx, runArgs)
	if err != nil {
		if noPwshError != nil {
			err = &internal.ErrorWithSuggestion{
				Err: err,
				Suggestion: fmt.Sprintf("pwsh (PowerShell 7) was not found and powershell (PowerShell 5) was automatically"+
					" used instead. You can try installing pwsh and trying again in case this script is not compatible "+
					"with PowerShell 5. See: %s",
					output.WithLinkFormat("https://learn.microsoft.com/powershell/scripting/install/installing-powershell")),
			}
		}
	}

	return result, err
}
