package powershell

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// Creates a new PowershellScript command runner
func NewPowershellScript(commandRunner exec.CommandRunner, cwd string, env *environment.Environment) tools.Script {
	return &powershellScript{
		commandRunner: commandRunner,
		cwd:           cwd,
		env:           env,
	}
}

type powershellScript struct {
	commandRunner exec.CommandRunner
	cwd           string
	env           *environment.Environment
}

// Executes the specified powershell script
// When interactive is true will attach to stdin, stdout & stderr
func (bs *powershellScript) Execute(ctx context.Context, path string, interactive bool) (exec.RunResult, error) {
	runArgs := exec.NewRunArgs("pwsh", path).
		WithCwd(bs.cwd).
		WithEnv(bs.env.Environ()).
		WithInteractive(interactive).
		WithShell(true)

	return bs.commandRunner.Run(ctx, runArgs)
}
