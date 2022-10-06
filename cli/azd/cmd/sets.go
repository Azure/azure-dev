package cmd

import (
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/action"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/google/wire"
)

func newConsoleFromOptions(rootOptions *internal.GlobalCommandOptions, writer io.Writer, formatter output.Formatter) input.Console {
	return input.NewConsole(!rootOptions.NoPrompt, writer, formatter)
}

func newAzCliFromOptions(rootOptions *internal.GlobalCommandOptions, cmdRun exec.CommandRunner) azcli.AzCli {
	return azcli.NewAzCli(azcli.NewAzCliArgs{
		EnableDebug:     rootOptions.EnableDebugLogging,
		EnableTelemetry: rootOptions.EnableTelemetry,
		CommandRunner:   cmdRun,
		HttpClient:      nil,
	})
}

func newAzdContext() (*azdcontext.AzdContext, error) {
	azdCtx, err := azdcontext.NewAzdContext()
	if err != nil {
		return nil, fmt.Errorf("creating context: %w", err)
	}

	return azdCtx, nil
}

var FormattedConsoleSet = wire.NewSet(
	output.GetCommandFormatter,
	output.GetDefaultWriter,
	newConsoleFromOptions,
)

var CommonSet = wire.NewSet(
	newAzdContext,
	exec.NewCommandRunner,
	FormattedConsoleSet,
)

var InitCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	git.NewGitCliFromRunner,
	newInitAction,
	wire.Bind(new(action.Action), new(*initAction)))

var InfraCreateCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newInfraCreateAction,
	wire.Bind(new(action.Action), new(*infraCreateAction)))

var InfraDeleteCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newInfraDeleteAction,
	wire.Bind(new(action.Action), new(*infraDeleteAction)))

var DeployCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newDeployAction,
	wire.Bind(new(action.Action), new(*deployAction)))

var UpCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	git.NewGitCliFromRunner,
	newInitAction,
	newInfraCreateAction,
	newDeployAction,
	newUpAction,
	wire.FieldsOf(new(upFlags), "initFlags", "infraCreateFlags", "deployFlags"),
	wire.Bind(new(action.Action), new(*upAction)))

var EnvSetCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newEnvSetAction,
	wire.Bind(new(action.Action), new(*envSetAction)))

var EnvSelectCmdSet = wire.NewSet(
	newAzdContext,
	newEnvSelectAction,
	wire.Bind(new(action.Action), new(*envSelectAction)))

var EnvListCmdSet = wire.NewSet(
	CommonSet,
	newEnvListAction,
	wire.Bind(new(action.Action), new(*envListAction)))

var EnvNewCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newEnvNewAction,
	wire.Bind(new(action.Action), new(*envNewAction)))

var EnvRefreshCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newEnvRefreshAction,
	wire.Bind(new(action.Action), new(*envRefreshAction)))

var EnvGetValuesCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newEnvGetValuesAction,
	wire.Bind(new(action.Action), new(*envGetValuesAction)))

var LoginCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newLoginAction,
	wire.Bind(new(action.Action), new(*loginAction)))

var MonitorCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newMonitorAction,
	wire.Bind(new(action.Action), new(*monitorAction)))

var PipelineConfigCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newPipelineConfigAction,
	wire.Bind(new(action.Action), new(*pipelineConfigAction)))
