//go:build wireinject
// +build wireinject

package cmd

// Run `go generate ./cmd` or `wire ./cmd` after modifying this file to regenerate `wire_gen.go`.

import (
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/google/wire"
	"github.com/spf13/cobra"
)

//#region Root

func initDeployAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags deployFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(DeployCmdSet))
}

func initInitAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags initFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(InitCmdSet))
}

func initLoginAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags loginFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(LoginCmdSet))
}

func initUpAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags upFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(UpCmdSet))
}

func initMonitorAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags monitorFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(MonitorCmdSet))
}

func initRestoreAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags restoreFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(RestoreCmdSet))
}

func initShowAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags showFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(ShowCmdSet))
}

func initVersionAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags versionFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(VersionCmdSet))
}

//#endregion Root

//#region Infra

func initInfraCreateAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags infraCreateFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(InfraCreateCmdSet))
}

func initInfraDeleteAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags infraDeleteFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(InfraDeleteCmdSet))
}

//#endregion Infra

//#region Env

func initEnvSetAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(EnvSetCmdSet))
}

func initEnvSelectAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(EnvSelectCmdSet))
}

func initEnvListAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(EnvListCmdSet))
}

func initEnvNewAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags envNewFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(EnvNewCmdSet))
}

func initEnvRefreshAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(EnvRefreshCmdSet))
}

func initEnvGetValuesAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(EnvGetValuesCmdSet))
}

//#endregion Env

//#region Pipeline

func initPipelineConfigAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags pipelineConfigFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(PipelineConfigCmdSet))
}

//#endregion Pipeline

//#region Templates

func initTemplatesListAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags templatesListFlags,
	args []string,
) (actions.Action, error) {
	panic(wire.Build(TemplatesListCmdSet))
}

func initTemplatesShowAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(TemplatesShowCmdSet))
}

//#endregion Templates

//#region Config

func initConfigListAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(ConfigListCmdSet))
}

func initConfigGetAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(ConfigGetCmdSet))
}

func initConfigSetAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(ConfigSetCmdSet))
}

func initConfigUnsetAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(ConfigUnsetCmdSet))
}

func initConfigResetAction(
	cmd *cobra.Command,
	o *internal.GlobalCommandOptions,
	flags struct{},
	args []string,
) (actions.Action, error) {
	panic(wire.Build(ConfigResetCmdSet))
}

//#endregion Config
