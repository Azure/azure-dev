// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"text/tabwriter"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newToolboxListCommand returns the `azd ai agent toolbox list` command.
func newToolboxListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List toolboxes on the project, plus any local pending records.",
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

	return runToolboxListWith(ctx, client, resolved.Endpoint, parent)
}

// runToolboxListWith is the testable core.
func runToolboxListWith(
	ctx context.Context, client toolboxClient, endpoint string, parent toolboxFlags,
) error {
	live, err := client.ListToolboxes(ctx)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpListToolboxes)
	}

	// Best-effort merge of pending records; failures are non-fatal.
	var pending map[string]PendingToolbox
	azdClient, azdErr := azdext.NewAzdClient()
	if azdErr != nil {
		log.Printf("toolbox list: azd client unavailable, skipping pending merge: %v", azdErr)
	} else {
		defer azdClient.Close()
		items, perr := listPendingToolboxes(ctx, azdClient, endpoint)
		if perr != nil {
			log.Printf("toolbox list: pending-toolbox read failed: %v", perr)
		} else {
			pending = items
		}
	}

	// Drop pending records that already exist live-side to avoid duplicates.
	liveNames := map[string]struct{}{}
	for _, t := range live {
		liveNames[t.Name] = struct{}{}
	}
	for k := range pending {
		if _, dup := liveNames[k]; dup {
			delete(pending, k)
		}
	}

	if parent.output == "json" {
		return emitListJSON(live, pending)
	}
	return emitListTable(ctx, client, live, pending)
}

func emitListJSON(live []azure.ToolboxObject, pending map[string]PendingToolbox) error {
	toolboxes := make([]map[string]any, 0, len(live)+len(pending))
	for _, t := range live {
		toolboxes = append(toolboxes, map[string]any{
			"id":              t.ID,
			"name":            t.Name,
			"default_version": t.DefaultVersion,
			"pending":         false,
		})
	}
	for _, k := range slices.Sorted(maps.Keys(pending)) {
		p := pending[k]
		toolboxes = append(toolboxes, map[string]any{
			"name":        k,
			"pending":     true,
			"description": p.Description,
			"createdAt":   p.CreatedAt,
		})
	}
	return emitJSON(map[string]any{"toolboxes": toolboxes})
}

// emitListTable produces NAME / DEFAULT-VERSION / STATE / CREATED. Per spec
// § 13 Decision 2, the table intentionally omits a TOOLS count to avoid an
// extra GET /versions per row; `toolbox show` reports it for one toolbox.
func emitListTable(
	_ context.Context, _ toolboxClient,
	live []azure.ToolboxObject, pending map[string]PendingToolbox,
) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDEFAULT-VERSION\tSTATE\tCREATED")
	fmt.Fprintln(w, "----\t---------------\t-----\t-------")

	sortedLive := slices.Clone(live)
	slices.SortFunc(sortedLive, func(a, b azure.ToolboxObject) int {
		return cmp.Compare(a.Name, b.Name)
	})

	for _, t := range sortedLive {
		fmt.Fprintf(w, "%s\t%s\t\t\n", t.Name, t.DefaultVersion)
	}

	for _, name := range slices.Sorted(maps.Keys(pending)) {
		fmt.Fprintf(w, "%s\t-\tpending\t%s\n", name, pending[name].CreatedAt)
	}

	return w.Flush()
}
