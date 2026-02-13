// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/spf13/cobra"
)

// newCustomCommand creates the "custom" command group for custom model operations.
func newCustomCommand() *cobra.Command {
	customCmd := &cobra.Command{
		Use:   "custom",
		Short: "Manage custom models in Azure AI Foundry",
	}

	customCmd.AddCommand(newCustomCreateCommand())
	customCmd.AddCommand(newCustomListCommand())
	customCmd.AddCommand(newCustomShowCommand())
	customCmd.AddCommand(newCustomDeleteCommand())

	return customCmd
}
