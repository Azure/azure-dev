// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.demo/internal/config"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/invopop/jsonschema"
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
				"1.0",                // schema version
				"microsoft.azd.demo", // extension id
				rootCmd,
			)

			// Add custom configuration schemas
			metadata.Configuration = generateConfigurationMetadata()

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

// generateConfigurationMetadata creates configuration schemas for the demo extension.
// This demonstrates how extension developers can define type-safe configuration
// requirements using Go structs and automatic schema generation.
func generateConfigurationMetadata() *extensions.ConfigurationMetadata {
	// Generate schemas from Go types automatically
	return &extensions.ConfigurationMetadata{
		Global:  jsonschema.Reflect(&config.CustomGlobalConfig{}),
		Project: jsonschema.Reflect(&config.CustomProjectConfig{}),
		Service: jsonschema.Reflect(&config.CustomServiceConfig{}),
	}
}
