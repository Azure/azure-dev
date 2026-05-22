// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newSampleCommand returns the `azd ai agent sample` parent command.
//
// The `sample` namespace groups read-only catalog discovery commands. Today
// only `list` is implemented; future additions (e.g. `sample show <id>`,
// `sample search`) belong here too so the namespace stays focused on
// browsing the curated catalog rather than mutating local state.
func newSampleCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "sample",
		Short: "Browse the curated catalog of agent samples and azd templates.",
		Long: `Browse the curated catalog of agent samples and azd templates.

The catalog is the same source the interactive ` + "`azd ai agent init`" + ` picker uses.
Subcommands emit machine-readable JSON or human-readable text so coding agents
and humans can both discover manifests and repos to feed back into
` + "`azd ai agent init -m <url>`" + ` or ` + "`azd init -t <url>`" + `.`,
	}

	cmd.AddCommand(newSampleListCommand(extCtx))

	return cmd
}
