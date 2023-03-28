package powershell

import (
	"context"

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

// Executes the specified powershell script
// When interactive is true will attach to stdin, stdout & stderr
func (bs *powershellScript) Execute(ctx context.Context, path string, interactive bool) (exec.RunResult, error) {
	runArgs := exec.NewRunArgs("pwsh", path).
		WithCwd(bs.cwd).
		WithEnv(bs.envVars).
		WithInteractive(interactive).
		WithShell(true)

	return bs.commandRunner.Run(ctx, runArgs)
}
