// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newMetadataCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "metadata",
		Short:  "Generate extension metadata including command structure and configuration schemas",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Get root command for metadata generation
			rootCmd := cmd.Root()

			// Generate extension metadata with commands and configuration
			metadata := azdext.GenerateExtensionMetadata(
				"1.0",             // schema version
				"azure.ai.agents", // extension id
				rootCmd,
			)

			// Output as JSON
			jsonBytes, err := json.MarshalIndent(metadata, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal metadata: %w", err)
			}

			fmt.Println(string(jsonBytes))
			return nil
		},
	}
}
