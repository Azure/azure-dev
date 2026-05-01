// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

const (
	extensionID = "azure.logicappsstandard"
)

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  extensionID,
		Short: "Extension for packaging Logic Apps Standard projects, including support for custom code projects",
	})

	// Standard lifecycle, metadata, and version commands
	rootCmd.AddCommand(azdext.NewListenCommand(configureListen))
	rootCmd.AddCommand(azdext.NewMetadataCommand("1.0", extensionID, NewRootCommand))
	rootCmd.AddCommand(azdext.NewVersionCommand(extensionID, version, &extCtx.OutputFormat))

	return rootCmd
}
