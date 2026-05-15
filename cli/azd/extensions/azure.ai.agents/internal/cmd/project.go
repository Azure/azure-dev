// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newProjectCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "project <command> [options]",
		Short: "Manage the default Microsoft Foundry project endpoint.",
		Long: `Manage the default Microsoft Foundry project endpoint.

These commands persist a workspace-level project endpoint in the azd global
config (~/.azd/config.json) so that other agent commands can resolve it
without requiring an azd environment or explicit flags.`,
	}

	cmd.AddCommand(newProjectSetCommand(extCtx))
	cmd.AddCommand(newProjectUnsetCommand(extCtx))
	cmd.AddCommand(newProjectShowCommand(extCtx))

	return cmd
}
