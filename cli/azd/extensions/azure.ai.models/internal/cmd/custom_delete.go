// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

type customDeleteFlags struct {
	Name        string
	KeepWeights bool
	Force       bool
}

func newCustomDeleteCommand() *cobra.Command {
	flags := &customDeleteFlags{}

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a custom model",
		Long:  "Delete a custom model from the Azure AI Foundry custom model registry and optionally remove its weights.",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Deleting custom model...")
			fmt.Printf("  Name:         %s\n", flags.Name)
			fmt.Printf("  Keep weights: %v\n", flags.KeepWeights)
			fmt.Printf("  Force:        %v\n", flags.Force)
			fmt.Println()
			fmt.Println("[TODO] Delete model from FDP API: DELETE /custom-models/{name}")
			fmt.Println()
			fmt.Println("Custom model deletion is not yet implemented.")
			return nil
		},
	}

	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "Model name")
	cmd.Flags().BoolVar(&flags.KeepWeights, "keep-weights", false, "Remove from registry but keep weights in data store")
	cmd.Flags().BoolVarP(&flags.Force, "force", "f", false, "Skip confirmation prompt")

	_ = cmd.MarkFlagRequired("name")

	return cmd
}
