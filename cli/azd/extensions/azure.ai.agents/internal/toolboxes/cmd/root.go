// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func NewToolboxesRootCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "toolboxes",
		Short: "Manage AI toolboxes.",
	}

	// cmd.AddCommand(azdext.NewListenCommand(configureExtensionHost))
	cmd.AddCommand(newVersionCommand())
	cmd.AddCommand(azdext.NewMetadataCommand("1.0", "azure.ai.agents", func() *cobra.Command {
		return cmd
	}))

	cmd.AddCommand(newCreateCommand(extCtx))

	return cmd
}
