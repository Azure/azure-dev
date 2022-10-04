//go:build wireinject
// +build wireinject

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/action"
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