// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

type customShowFlags struct {
	Name   string
	Output string
}

func newCustomShowCommand() *cobra.Command {
	flags := &customShowFlags{}

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show details of a custom model",
		Long:  "Show detailed information about a specific custom model in the Azure AI Foundry custom model registry.",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Showing custom model details...")
			fmt.Printf("  Name:          %s\n", flags.Name)
			fmt.Printf("  Output format: %s\n", flags.Output)
			fmt.Println()
			fmt.Println("[TODO] Fetch model details from FDP API: GET /custom-models/{name}")
			fmt.Println()
			fmt.Println("Custom model show is not yet implemented.")
			return nil
		},
	}

	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "Model name")
	cmd.Flags().StringVarP(&flags.Output, "output", "o", "table", "Output format (table, json)")

	_ = cmd.MarkFlagRequired("name")

	return cmd
}
