// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package powershell

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
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

func checkPath(options tools.ExecOptions) (err error) {
	return tools.ToolInPath(options.UserPwsh)
}

// Executes the specified powershell script
// When interactive is true will attach to stdin, stdout & stderr
func (bs *powershellScript) Execute(ctx context.Context, path string, options tools.ExecOptions) (exec.RunResult, error) {
	if err := checkPath(options); err != nil {
		return exec.RunResult{}, &internal.ErrorWithSuggestion{
			Err:        err,
			Suggestion: "PowerShell is not installed or not in the path. To install PowerShell, visit https://learn.microsoft.com/powershell/scripting/install/installing-powershell",
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

	return bs.commandRunner.Run(ctx, runArgs)
}
