/*
Copyright © 2022 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"actioncli/cmd/actions"
	"actioncli/pkg/action"
	"os"

	"github.com/spf13/cobra"
)

func Execute() {
	err := Build().Execute()
	if err != nil {
		os.Exit(1)
	}
}

func Build() *cobra.Command {
	// rootCmd represents the base command when called without any subcommands
	var rootCmd = &cobra.Command{
		Use:   "actioncli",
		Short: "A brief description of your application",
		Long: `A longer description that spans multiple lines and likely contains
	examples and usage of using your application. For example:

	Cobra is a CLI library for Go that empowers applications.
	This application is a tool to generate the needed files
	to quickly create a Cobra application.`,
	}

	opts := &actions.GlobalFlags{}
	rootCmd.PersistentFlags().BoolVar(&opts.NoPrompt, "no-prompt", false, "Accepts the default value instead of prompting, or it fails if there is no default.")
	rootCmd.AddCommand(BuildCmd(opts, initCmdDesign, InjectInitAction))
	rootCmd.AddCommand(BuildCmd(opts, deployCmdDesign, InjectDeployAction))
	rootCmd.AddCommand(BuildCmd(opts, upCmdDesign, InjectUpAction))

	return rootCmd
}

type Builder[F any] func(opts *actions.GlobalFlags) (*cobra.Command, *F)

func BuildCmd[F any](opts *actions.GlobalFlags, builder Builder[F], actionInjector func() (action.Action[F], error)) *cobra.Command {
	cmd, flags := builder(opts)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		action, err := actionInjector()
		if err != nil {
			return err
		}
		return action.Run(cmd.Context(), *flags, args)
	}
	return cmd
}
