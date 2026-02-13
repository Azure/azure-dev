// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

type customListFlags struct {
	Output string
}

func newCustomListCommand() *cobra.Command {
	flags := &customListFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all custom models",
		Long:  "List all custom models registered in the Azure AI Foundry custom model registry.",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Listing custom models...")
			fmt.Printf("  Output format: %s\n", flags.Output)
			fmt.Println()
			fmt.Println("[TODO] Fetch custom models from FDP API: GET /custom-models")
			fmt.Println()
			fmt.Println("Custom model listing is not yet implemented.")
			return nil
		},
	}

	cmd.Flags().StringVarP(&flags.Output, "output", "o", "table", "Output format (table, json)")

	return cmd
}
