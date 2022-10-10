//go:build wireinit
// +build wireinit

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/cmd/action"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/google/wire"
	"github.com/spf13/cobra"
)

func initInitAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags initFlags, args []string) (action.Action, error) {
	panic(wire.Build(InitCmdSet))
}

func initInfraCreateAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags infraCreateFlags, args []string) (action.Action, error) {
	panic(wire.Build(InfraCreateCmdSet))
}

func initDeployAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags deployFlags, args []string) (action.Action, error) {
	panic(wire.Build(DeployCmdSet))
}

func initUpAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags upFlags, args []string) (action.Action, error) {
	panic(wire.Build(UpCmdSet))
}

func initInfraDeleteAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags infraDeleteFlags, args []string) (action.Action, error) {
	panic(wire.Build(InfraDeleteCmdSet))
}

func initEnvSetAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags struct{}, args []string) (action.Action, error) {
	panic(wire.Build(EnvSetCmdSet))
}

func initEnvSelectAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags struct{}, args []string) (action.Action, error) {
	panic(wire.Build(EnvSelectCmdSet))
}

func initEnvListAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags struct{}, args []string) (action.Action, error) {
	panic(wire.Build(EnvListCmdSet))
}

func initEnvNewAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags envNewFlags, args []string) (action.Action, error) {
	panic(wire.Build(EnvNewCmdSet))
}

func initEnvRefreshAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags struct{}, args []string) (action.Action, error) {
	panic(wire.Build(EnvRefreshCmdSet))
}

func initEnvGetValuesAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags struct{}, args []string) (action.Action, error) {
	panic(wire.Build(EnvGetValuesCmdSet))
}

func initLoginAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags loginFlags, args []string) (action.Action, error) {
	panic(wire.Build(LoginCmdSet))
}

func initMonitorAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags monitorFlags, args []string) (action.Action, error) {
	panic(wire.Build(MonitorCmdSet))
}

func initPipelineConfigAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags pipelineConfigFlags, args []string) (action.Action, error) {
	panic(wire.Build(PipelineConfigCmdSet))
}

func initRestoreAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags restoreFlags, args []string) (action.Action, error) {
	panic(wire.Build(RestoreCmdSet))
}

func initShowAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags showFlags, args []string) (action.Action, error) {
	panic(wire.Build(ShowCmdSet))
}

func initTemplatesListAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags templatesListFlags, args []string) (action.Action, error) {
	panic(wire.Build(TemplatesListCmdSet))
}

func initTemplatesShowAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags struct{}, args []string) (action.Action, error) {
	panic(wire.Build(TemplatesShowCmdSet))
}

func initVersionAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags versionFlags, args []string) (action.Action, error) {
	panic(wire.Build(VersionCmdSet))
}
