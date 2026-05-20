// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"azure.ai.toolboxes/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newToolboxConnectionListCommand returns the `connection list` command.
func newToolboxConnectionListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "list <toolbox>",
		Short: "List the connection-backed tools attached to a toolbox.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConnectionList(cmd.Context(), args[0], readToolboxFlags(cmd, extCtx))
		},
	}
	registerToolboxOutputFlag(cmd)
	return cmd
}

func runConnectionList(ctx context.Context, toolboxName string, parent toolboxFlags) error {
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
	logResolvedEndpoint("toolbox connection list", resolved)

	return runConnectionListWith(ctx, client, toolboxName, parent)
}

func runConnectionListWith(
	ctx context.Context, client toolboxClient, toolboxName string, parent toolboxFlags,
) error {
	tb, err := client.GetToolbox(ctx, toolboxName)
	if err != nil {
		return toolboxNotFoundOrService(err, toolboxName, exterrors.OpGetToolbox)
	}

	version, err := client.GetToolboxVersion(ctx, toolboxName, tb.DefaultVersion)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolboxVersion)
	}

	connections := extractConnectionTools(version.Tools)

	if parent.output == "json" {
		return emitJSON(map[string]any{"connections": connections})
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tCONNECTION\tTYPE")
	fmt.Fprintln(w, "----\t----------\t----")
	for _, c := range connections {
		fmt.Fprintf(w, "%s\t%s\t%s\n", c["name"], c["connection"], c["type"])
	}
	return w.Flush()
}

// extractConnectionTools collapses the tool list to one row per connection-backed
// entry, surfacing the short connection name parsed from the trailing segment
// of the connection ARM ID (the `connection` column in `connection list`).
func extractConnectionTools(tools []map[string]any) []map[string]string {
	rows := []map[string]string{}
	for _, t := range tools {
		toolType, _ := t["type"].(string)
		toolName, _ := t["name"].(string)
		switch toolType {
		case "mcp":
			if id, ok := t["project_connection_id"].(string); ok && id != "" {
				rows = append(rows, map[string]string{
					"name":          toolName,
					"connection":    shortConnectionName(id),
					"connection_id": id,
					"type":          toolType,
				})
			}
		case "azure_ai_search":
			if search, ok := t["azure_ai_search"].(map[string]any); ok {
				if indexes, ok := search["indexes"].([]any); ok {
					for _, idx := range indexes {
						m, _ := idx.(map[string]any)
						if m == nil {
							continue
						}
						id, _ := m["project_connection_id"].(string)
						idxName, _ := m["index_name"].(string)
						rows = append(rows, map[string]string{
							"name":          toolName,
							"connection":    shortConnectionName(id),
							"connection_id": id,
							"type":          toolType,
							"index":         idxName,
						})
					}
				}
			}
		}
	}
	return rows
}
