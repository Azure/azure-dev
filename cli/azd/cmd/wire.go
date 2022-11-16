//go:build wireinject
// +build wireinject

package cmd

// Run `go generate ./cmd` or `wire ./cmd` after modifying this file to regenerate `wire_gen.go`.

import (
	"context"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/google/wire"
	"github.com/spf13/cobra"
)

func initConsole(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
) (input.Console, error) {
	panic(wire.Build(FormattedConsoleSet))
}

//#region Root

func initDeployAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags deployFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(DeployCmdSet))
}

func initInitAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags initFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(InitCmdSet))
}

func initLoginAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags loginFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(LoginCmdSet))
}

func initLogoutAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(LogoutCmdSet))
}

func initUpAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags upFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(UpCmdSet))
}

func initMonitorAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags monitorFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(MonitorCmdSet))
}

func initRestoreAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags restoreFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(RestoreCmdSet))
}

func initShowAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags showFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(ShowCmdSet))
}

func initVersionAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags versionFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(VersionCmdSet))
}

//#endregion Root

//#region Auth

func initAuthTokenAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags authTokenFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(AuthTokenCmdSet))
}

//#endregion Auth

//#region Infra

func initInfraCreateAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags infraCreateFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(InfraCreateCmdSet))
}

func initInfraDeleteAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags infraDeleteFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(InfraDeleteCmdSet))
}

//#endregion Infra

//#region Env

func initEnvSetAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags envSetFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(EnvSetCmdSet))
}

func initEnvSelectAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(EnvSelectCmdSet))
}

func initEnvListAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(EnvListCmdSet))
}

func initEnvNewAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags envNewFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(EnvNewCmdSet))
}

func initEnvRefreshAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags envRefreshFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(EnvRefreshCmdSet))
}

func initEnvGetValuesAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags envGetValuesFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(EnvGetValuesCmdSet))
}

//#endregion Env

//#region Pipeline

func initPipelineConfigAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags pipelineConfigFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(PipelineConfigCmdSet))
}

//#endregion Pipeline

//#region Templates

func initTemplatesListAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags templatesListFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(TemplatesListCmdSet))
}

func initTemplatesShowAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(TemplatesShowCmdSet))
}

//#endregion Templates

//#region Config

func initConfigListAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(ConfigListCmdSet))
}

func initConfigGetAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(ConfigGetCmdSet))
}

func initConfigSetAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(ConfigSetCmdSet))
}

func initConfigUnsetAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(ConfigUnsetCmdSet))
}

func initConfigResetAction(
	console input.Console,
	ctx context.Context,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(ConfigResetCmdSet))
}

//#endregion Config
