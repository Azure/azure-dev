// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"text/tabwriter"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newToolboxListCommand returns the `azd ai toolbox list` command.
func newToolboxListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List toolboxes on the project.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolboxList(cmd.Context(), readToolboxFlags(cmd, extCtx))
		},
	}
	registerToolboxOutputFlag(cmd)
	return cmd
}

func runToolboxList(ctx context.Context, parent toolboxFlags) error {
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}

	client, resolved, err := resolveToolboxAndClient(ctx, parent)
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox list", resolved)

	return runToolboxListWith(ctx, client, parent)
}

// runToolboxListWith is the testable core.
func runToolboxListWith(
	ctx context.Context, client toolboxClient, parent toolboxFlags,
) error {
	live, err := client.ListToolboxes(ctx)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpListToolboxes)
	}

	if parent.output == "json" {
		return emitListJSON(live)
	}
	return emitListTable(live)
}

func emitListJSON(live []azure.ToolboxObject) error {
	toolboxes := make([]map[string]any, 0, len(live))
	for _, t := range live {
		toolboxes = append(toolboxes, map[string]any{
			"id":              t.ID,
			"name":            t.Name,
			"default_version": t.DefaultVersion,
		})
	}
	return emitJSON(map[string]any{"toolboxes": toolboxes})
}

// emitListTable produces NAME / DEFAULT-VERSION. The table intentionally omits
// a TOOLS count to avoid an extra fetch per row; use `toolbox show` to see
// tools for a single toolbox.
func emitListTable(live []azure.ToolboxObject) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDEFAULT-VERSION")
	fmt.Fprintln(w, "----\t---------------")

	sortedLive := slices.Clone(live)
	slices.SortFunc(sortedLive, func(a, b azure.ToolboxObject) int {
		return cmp.Compare(a.Name, b.Name)
	})

	for _, t := range sortedLive {
		fmt.Fprintf(w, "%s\t%s\n", t.Name, t.DefaultVersion)
	}

	return w.Flush()
}
