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
	// block alternative shells for non-windows.
	// This is because the only alternative shell supported is powershell5 which is not available on non-windows platforms.
	if runtime.GOOS != "windows" {
		options.StrictShell = true
	}

	if err := bs.checkInstalled(options); err != nil {
		if options.StrictShell {
			return exec.RunResult{}, &internal.ErrorWithSuggestion{
				Err: err,
				Suggestion: fmt.Sprintf("PowerShell 7 is not installed or not in the path. To install PowerShell 7, visit %s",
					output.WithLinkFormat("https://learn.microsoft.com/powershell/scripting/install/installing-powershell")),
			}
		}

		// non-strict mode, check for alternative shell powershell 5
		options.UserPwsh = "powershell"
		if err := bs.checkInstalled(options); err != nil {
			return exec.RunResult{}, &internal.ErrorWithSuggestion{
				Err:        err,
				Suggestion: "Make sure Powershell is installed in your system.",
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

	return bs.commandRunner.Run(ctx, runArgs)
}
