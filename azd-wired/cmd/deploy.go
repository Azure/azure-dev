/*
Copyright © 2022 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"actioncli/cmd/actions"

	"github.com/spf13/cobra"
)

// deployCmdDesign represents the UI design of the init command
func deployCmdDesign(global *actions.GlobalFlags) (*cobra.Command, *actions.DeployFlags) {
	command := &cobra.Command{
		Use:   "deploy",
		Short: "A brief description of your command",
		Long: `A longer description that spans multiple lines and likely contains examples
	and usage of using your command. For example:

	Cobra is a CLI library for Go that empowers applications.
	This application is a tool to generate the needed files
	to quickly create a Cobra application.`,
	}

	f := &actions.DeployFlags{}
	f.Setup(command.Flags(), global)
	return command, f
}
