package cmd

import (
	"github.com/spf13/cobra"
)

func newModelCommand() *cobra.Command {
	modelCmd := &cobra.Command{
		Use:   "model",
		Short: "Commands for managing Azure AI models",
	}

	modelListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all models",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}

	modelCmd.AddCommand(modelListCmd)
	modelCmd.AddCommand(newDeploymentCommand())

	return modelCmd
}
