//go:build wireinject
// +build wireinject

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/cmd/action"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/google/wire"
	"github.com/spf13/cobra"
)

func injectInitAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags initFlags, args []string) (action.Action, error) {
	panic(wire.Build(InitCmdSet))
}

func injectInfraCreateAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags infraCreateFlags, args []string) (action.Action, error) {
	panic(wire.Build(InfraCreateCmdSet))
}

func injectDeployAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags deployFlags, args []string) (action.Action, error) {
	panic(wire.Build(DeployCmdSet))
}

func injectUpAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags upFlags, args []string) (action.Action, error) {
	panic(wire.Build(UpCmdSet))
}

func injectInfraDeleteAction(cmd *cobra.Command, o *internal.GlobalCommandOptions, flags infraDeleteFlags, args []string) (action.Action, error) {
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
