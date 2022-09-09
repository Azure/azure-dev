/*
Copyright © 2022 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"actioncli/cmd/actions"

	"github.com/spf13/cobra"
)

// upCmdDesign represents the UI design of the init command
func upCmdDesign(global *actions.GlobalFlags) (*cobra.Command, *actions.UpFlags) {
	command := &cobra.Command{
		Use:   "up",
		Short: "A brief description of your command",
		Long: `A longer description that spans multiple lines and likely contains examples
	and usage of using your command. For example:

	Cobra is a CLI library for Go that empowers applications.
	This application is a tool to generate the needed files
	to quickly create a Cobra application.`,
	}

	f := &actions.UpFlags{}
	f.Setup(command.Flags(), global)
	return command, f
}
