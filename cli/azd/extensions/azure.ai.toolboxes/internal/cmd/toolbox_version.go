// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newToolboxVersionCommand returns the `azd ai toolbox versions` parent.
func newToolboxVersionCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use:   "versions",
		Short: "Inspect toolbox versions.",
		Long: `Inspect toolbox versions.

Use this group to list published versions for a toolbox and inspect their
metadata (for example, when deciding which version to retarget as default).`,
	}

	cmd.AddCommand(newToolboxVersionListCommand(extCtx))
	return cmd
}
