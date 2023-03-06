package bash

import (
	"context"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// Creates a new BashScript command runner
func NewBashScript(commandRunner exec.CommandRunner, cwd string, env *environment.Environment) tools.Script {
	return &bashScript{
		commandRunner: commandRunner,
		cwd:           cwd,
		env:           env,
	}
}

type bashScript struct {
	commandRunner exec.CommandRunner
	cwd           string
	env           *environment.Environment
}

// Executes the specified bash script
// When interactive is true will attach to stdin, stdout & stderr
func (bs *bashScript) Execute(ctx context.Context, path string, interactive bool) (exec.RunResult, error) {
	var runArgs exec.RunArgs
	// Bash likes all path separators in POSIX format
	path = strings.ReplaceAll(path, "\\", "/")

	if runtime.GOOS == "windows" {
		runArgs = exec.NewRunArgs("bash", path)
	} else {
		runArgs = exec.NewRunArgs("", path)
	}

	runArgs = runArgs.
		WithCwd(bs.cwd).
		WithEnv(bs.env.Environ()).
		WithInteractive(interactive).
		WithShell(true)

	return bs.commandRunner.Run(ctx, runArgs)
}
