// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newMetadataCommand(rootCmd *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:    "metadata",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			metadata := azdext.GenerateExtensionMetadata("1.0", "azure.ai.latency", rootCmd)
			content, err := json.MarshalIndent(metadata, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal extension metadata: %w", err)
			}
			if _, err := cmd.OutOrStdout().Write(append(content, '\n')); err != nil {
				return fmt.Errorf("write extension metadata: %w", err)
			}
			return nil
		},
	}
}
