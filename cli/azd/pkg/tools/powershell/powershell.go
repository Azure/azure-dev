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
		commandRunner:  commandRunner,
		cwd:            cwd,
		envVars:        envVars,
		checkInstalled: checkPath,
	}
}

// for testing
func NewPowershellScriptWithMockCheckPath(
	commandRunner exec.CommandRunner,
	cwd string,
	envVars []string,
	mockCheckPath checkInstalled) tools.Script {
	return &powershellScript{
		commandRunner:  commandRunner,
		cwd:            cwd,
		envVars:        envVars,
		checkInstalled: mockCheckPath,
	}
}

type powershellScript struct {
	commandRunner  exec.CommandRunner
	cwd            string
	envVars        []string
	checkInstalled checkInstalled
}

type checkInstalled func(options tools.ExecOptions) error

func checkPath(options tools.ExecOptions) (err error) {
	return tools.ToolInPath(strings.Split(options.UserPwsh, " ")[0])
}

// Executes the specified powershell script
// When interactive is true will attach to stdin, stdout & stderr
func (bs *powershellScript) Execute(ctx context.Context, path string, options tools.ExecOptions) (exec.RunResult, error) {
	noPwshError := bs.checkInstalled(options)
	if noPwshError != nil {

		if runtime.GOOS != "windows" {
			return exec.RunResult{}, &internal.ErrorWithSuggestion{
				Err: noPwshError,
				Suggestion: fmt.Sprintf(
					"PowerShell 7 is not installed or not in the path. To install PowerShell 7, visit %s",
					output.WithLinkFormat("https://learn.microsoft.com/powershell/scripting/install/installing-powershell")),
			}
		}

		// non-strict mode, check for alternative shell powershell 5
		options.UserPwsh = "powershell"
		if err := bs.checkInstalled(options); err != nil {
			return exec.RunResult{}, &internal.ErrorWithSuggestion{
				Err: err,
				Suggestion: fmt.Sprintf(
					"Make sure pwsh (Powershell 7) or powershell (Powershell 5) is installed on your system, visit %s",
					output.WithLinkFormat("https://learn.microsoft.com/powershell/scripting/install/installing-powershell")),
			}
		}
	}

	runArgs := exec.NewRunArgs(options.UserPwsh, path).
		WithCwd(bs.cwd).
		WithEnv(bs.envVars).
		WithShell(true)

	if options.Interactive != nil {
		runArgs = runArgs.WithInteractive(*options.Interactive)
	}

	if options.StdOut != nil {
		runArgs = runArgs.WithStdOut(options.StdOut)
	}

	result, err := bs.commandRunner.Run(ctx, runArgs)
	if err != nil {
		if noPwshError != nil {
			err = &internal.ErrorWithSuggestion{
				Err: err,
				Suggestion: "pwsh (Powershell 7) was not found and powershell (Powershell 5) was automatically used " +
					"instead. You can try installing pwsh and trying again in case this script is not compatible with " +
					"Powershell 5.",
			}
		}
	}

	return result, err
}
