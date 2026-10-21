// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/spf13/cobra"
)

const extensionID = "azure.ai.content-understanding"

func newMetadataCommand(rootCmd *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:    "metadata",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			metadata := azdext.GenerateExtensionMetadata("1.0", extensionID, rootCmd)
			configureCommandLaunch(metadata.Commands)
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(metadata); err != nil {
				return fmt.Errorf("write extension metadata: %w", err)
			}

			return nil
		},
	}
}

// Environment values are resolved through scoped RPCs, not launch-time process injection.
func configureCommandLaunch(commands []extensions.Command) {
	for index := range commands {
		if commands[index].Launch == nil {
			commands[index].Launch = &extensions.CommandLaunch{}
		}
		commands[index].Launch.NoAzdEnvironment = true
		configureCommandLaunch(commands[index].Subcommands)
	}
}
