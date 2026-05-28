// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"text/tabwriter"
	"time"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newToolboxVersionListCommand returns `azd ai toolbox version list <toolbox>`.
func newToolboxVersionListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "list <toolbox>",
		Short: "List published versions for a toolbox.",
		Long: `List published versions for a toolbox.

Shows one row per published version and marks which one is currently the
default. Use this when choosing a target for 'toolbox publish'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolboxVersionList(cmd.Context(), args[0], readToolboxFlags(cmd, extCtx))
		},
	}

	registerToolboxOutputFlag(cmd)
	return cmd
}

func runToolboxVersionList(ctx context.Context, toolboxName string, parent toolboxFlags) error {
	if err := validateToolboxName(toolboxName); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}

	client, resolved, err := resolveToolboxAndClient(ctx, parent)
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox version list", resolved)

	return runToolboxVersionListWith(ctx, client, toolboxName, parent)
}

// runToolboxVersionListWith is the testable core.
func runToolboxVersionListWith(
	ctx context.Context, client toolboxClient, toolboxName string, parent toolboxFlags,
) error {
	tb, err := client.GetToolbox(ctx, toolboxName)
	if err != nil {
		return toolboxNotFoundOrService(err, toolboxName, exterrors.OpGetToolbox)
	}

	versions, err := client.ListToolboxVersions(ctx, toolboxName)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpListToolboxVersions)
	}

	// Stable ordering: numeric version descending when possible, otherwise
	// lexical descending as a fallback.
	slices.SortFunc(versions, func(a, b azure.ToolboxVersionObject) int {
		return versionSortDescending(a.Version, b.Version)
	})

	if parent.output == "json" {
		return emitToolboxVersionListJSON(tb.Name, tb.DefaultVersion, versions)
	}
	return emitToolboxVersionListTable(tb.Name, tb.DefaultVersion, versions)
}

// versionSortDescending returns a negative number when a sorts after b (newer
// first). Numeric versions compare numerically; otherwise fall back to
// lexical descending.
func versionSortDescending(a, b string) int {
	ai, aErr := strconv.Atoi(a)
	bi, bErr := strconv.Atoi(b)
	if aErr == nil && bErr == nil {
		return cmp.Compare(bi, ai)
	}
	return cmp.Compare(b, a)
}

func emitToolboxVersionListJSON(name, defaultVersion string, versions []azure.ToolboxVersionObject) error {
	items := make([]map[string]any, 0, len(versions))
	for _, v := range versions {
		items = append(items, map[string]any{
			"id":           v.ID,
			"name":         v.Name,
			"version":      v.Version,
			"description":  v.Description,
			"created_at":   v.CreatedAt,
			"tools_count":  len(v.Tools),
			"skills_count": len(v.Skills),
			"is_default":   v.Version == defaultVersion,
		})
	}

	return emitJSON(map[string]any{
		"toolbox":         name,
		"default_version": defaultVersion,
		"versions":        items,
	})
}

func emitToolboxVersionListTable(name, defaultVersion string, versions []azure.ToolboxVersionObject) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "VERSION\tDEFAULT\tCREATED\tTOOLS\tSKILLS\tDESCRIPTION")
	fmt.Fprintln(w, "-------\t-------\t-------\t-----\t------\t-----------")

	for _, v := range versions {
		marker := ""
		if v.Version == defaultVersion {
			marker = "*"
		}
		created := "-"
		if v.CreatedAt > 0 {
			created = time.Unix(v.CreatedAt, 0).UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%d\t%d\t%s\n",
			v.Version,
			marker,
			created,
			len(v.Tools),
			len(v.Skills),
			v.Description,
		)
	}

	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Printf("\nToolbox: %s\n", name)
	fmt.Printf("Default version: %s\n", defaultVersion)
	return nil
}
