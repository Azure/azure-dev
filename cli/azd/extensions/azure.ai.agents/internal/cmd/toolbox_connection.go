// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newToolboxConnectionCommand returns the `azd ai agent toolbox connection` parent.
func newToolboxConnectionCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use:   "connection",
		Short: "Manage the connection-backed tools attached to a toolbox.",
		Long: `Manage the connection-backed tools attached to a toolbox.

Tools are project connections (MCP servers via RemoteTool, or Azure AI Search
indexes via CognitiveSearch). Each mutation publishes a new immutable version
and retargets the toolbox default.`,
	}
	cmd.AddCommand(newToolboxConnectionAddCommand(extCtx))
	cmd.AddCommand(newToolboxConnectionRemoveCommand(extCtx))
	cmd.AddCommand(newToolboxConnectionListCommand(extCtx))
	return cmd
}

// connectionAddFlags carries the verb-specific flags for `connection add`.
type connectionAddFlags struct {
	index string
}

// newToolboxConnectionAddCommand returns the `connection add` command.
func newToolboxConnectionAddCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &connectionAddFlags{}

	cmd := &cobra.Command{
		Use:   "add <toolbox> <connection>",
		Short: "Attach a project connection to a toolbox.",
		Long: `Attach a project connection to a toolbox.

The tool entry shape is inferred from the connection's ARM category:
  RemoteTool       → mcp tool with server_label/server_url from the connection target
  CognitiveSearch  → azure_ai_search tool (requires --index)
Other categories are rejected.

If the toolbox has a local pending record (from 'toolbox create'), v1 is
published with this connection as the only tool. Otherwise the current default
version is fetched, the tool entry is appended, a new version is POSTed, and
the toolbox default is retargeted.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConnectionAdd(
				cmd.Context(), args[0], args[1], *flags,
				readToolboxFlags(cmd, extCtx),
				defaultConnectionResolver{},
			)
		},
	}

	cmd.Flags().StringVar(
		&flags.index, "index", "",
		"Index name (required when the connection's category is CognitiveSearch).",
	)
	registerToolboxOutputFlag(cmd)
	return cmd
}

func runConnectionAdd(
	ctx context.Context, toolboxName, connName string,
	verb connectionAddFlags, parent toolboxFlags,
	resolver connectionResolver,
) error {
	if err := validateToolboxName(toolboxName); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}
	if strings.TrimSpace(connName) == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidPositionalArg,
			"<connection> must not be empty",
			"pass the short name of a project connection",
		)
	}

	client, resolved, err := resolveToolboxAndClient(ctx, parent)
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox connection add", resolved)

	store, closer, err := newAzdPendingToolboxStore()
	if err != nil {
		return exterrors.Internal(exterrors.CodeAzdClientFailed,
			fmt.Sprintf("failed to open the pending-toolbox store: %s", err))
	}
	defer closer()

	return runConnectionAddWith(ctx, client, resolver, store, resolved.Endpoint,
		toolboxName, connName, verb, parent)
}

// runConnectionAddWith is the testable core.
func runConnectionAddWith(
	ctx context.Context, client toolboxClient, resolver connectionResolver,
	store pendingToolboxStore,
	endpoint, toolboxName, connName string,
	verb connectionAddFlags, parent toolboxFlags,
) error {
	conn, err := resolver.resolveConnection(ctx, endpoint, connName)
	if err != nil {
		return err
	}

	entry, err := buildToolEntry(conn, verb.index)
	if err != nil {
		return err
	}

	// Pending-promotion path: if a pending record exists, POST v1 directly.
	// A store-read failure must not silently fall through to the live-toolbox
	// branch (which would 404 and report CodeToolboxNotFound).
	pending, err := store.Get(ctx, endpoint, toolboxName)
	if err != nil {
		return exterrors.Internal(
			exterrors.CodePendingToolboxStoreFailed,
			fmt.Sprintf("failed to read pending toolbox state: %s", err),
		)
	}
	if pending != nil {
		req := &azure.CreateToolboxVersionRequest{
			Description: pending.Description,
			Tools:       []map[string]any{entry},
		}
		created, err := client.CreateToolboxVersion(ctx, toolboxName, req)
		if err != nil {
			return exterrors.ServiceFromAzure(err, exterrors.OpCreateToolboxVersion)
		}
		if _, err := store.Clear(ctx, endpoint, toolboxName); err != nil {
			return exterrors.Internal(
				exterrors.CodePendingToolboxStoreFailed,
				fmt.Sprintf("failed to clear pending toolbox record: %s", err),
			)
		}
		return emitConnectionAddResult(toolboxName, created.Version, conn, parent.output, true)
	}

	// Existing-toolbox path: fetch default → append → POST → PATCH default_version.
	tb, err := client.GetToolbox(ctx, toolboxName)
	if err != nil {
		if isAzureNotFound(err) {
			return exterrors.Dependency(
				exterrors.CodeToolboxNotFound,
				fmt.Sprintf("toolbox %q not found", toolboxName),
				fmt.Sprintf(
					"run 'azd ai agent toolbox create %q' first, then re-run 'connection add'",
					toolboxName,
				),
			)
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolbox)
	}

	current, err := client.GetToolboxVersion(ctx, toolboxName, tb.DefaultVersion)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolboxVersion)
	}

	if duplicateConnectionInTools(current.Tools, conn.ID) {
		return exterrors.Validation(
			exterrors.CodeDuplicateConnection,
			fmt.Sprintf(
				"connection %q (%s) is already attached to toolbox %q",
				connName, conn.ID, toolboxName,
			),
			fmt.Sprintf("use 'connection list %q' to inspect current tools", toolboxName),
		)
	}

	newTools := slices.Clone(current.Tools)
	newTools = append(newTools, entry)

	req := &azure.CreateToolboxVersionRequest{
		Description: current.Description,
		Metadata:    current.Metadata,
		Tools:       newTools,
	}
	created, err := client.CreateToolboxVersion(ctx, toolboxName, req)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateToolboxVersion)
	}

	if _, err := client.SetDefaultVersion(ctx, toolboxName, created.Version); err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpSetDefaultVersion)
	}

	return emitConnectionAddResult(toolboxName, created.Version, conn, parent.output, false)
}

// buildToolEntry returns the tool-entry map appropriate for the connection's
// category. Enforces the --index flag rules from § 5.6 and the `tool.name`
// regex from § 4.2.
func buildToolEntry(conn *projectConnection, index string) (map[string]any, error) {
	if err := validateToolName(conn.Name); err != nil {
		return nil, err
	}
	switch conn.Category {
	case azure.ConnectionTypeRemoteTool:
		if index != "" {
			return nil, exterrors.Validation(
				exterrors.CodeUnsupportedIndexFlag,
				fmt.Sprintf(
					"--index is only valid for CognitiveSearch connections, "+
						"connection %q has category %q",
					conn.Name, conn.Category,
				),
				"omit --index for RemoteTool (MCP) connections",
			)
		}
		// Reject locally rather than letting the service produce a generic 400.
		if strings.TrimSpace(conn.Target) == "" {
			return nil, exterrors.Validation(
				exterrors.CodeConnectionMissingTarget,
				fmt.Sprintf(
					"connection %q is a RemoteTool but has no target URL",
					conn.Name,
				),
				"set the target on the project connection (this is the MCP server URL)",
			)
		}
		return map[string]any{
			"type":                  "mcp",
			"name":                  conn.Name,
			"server_label":          conn.Name,
			"server_url":            conn.Target,
			"project_connection_id": conn.ID,
		}, nil

	case azure.ConnectionTypeCognitiveSearch:
		if strings.TrimSpace(index) == "" {
			return nil, exterrors.Validation(
				exterrors.CodeMissingIndex,
				fmt.Sprintf(
					"connection %q is a CognitiveSearch connection; --index is required",
					conn.Name,
				),
				"pass --index <name> with the search index to attach",
			)
		}
		return map[string]any{
			"type": "azure_ai_search",
			"name": conn.Name,
			"azure_ai_search": map[string]any{
				"indexes": []any{
					map[string]any{
						"project_connection_id": conn.ID,
						"index_name":            index,
					},
				},
			},
		}, nil

	default:
		return nil, exterrors.Validation(
			exterrors.CodeUnsupportedConnectionCategory,
			fmt.Sprintf(
				"connection %q has category %q; v1 supports RemoteTool and CognitiveSearch only",
				conn.Name, conn.Category,
			),
			"use a RemoteTool (MCP) or CognitiveSearch (Azure AI Search) connection",
		)
	}
}

// duplicateConnectionInTools reports whether any tool entry already references
// the given project_connection_id.
func duplicateConnectionInTools(tools []map[string]any, connID string) bool {
	found := false
	forEachToolConnectionID(tools, func(id string) bool {
		if id == connID {
			found = true
			return true
		}
		return false
	})
	return found
}

// emitConnectionAddResult prints the standard output for a successful add.
func emitConnectionAddResult(
	toolboxName, newVersion string, conn *projectConnection, output string, promoted bool,
) error {
	if output == "json" {
		payload := map[string]any{
			"toolbox":             toolboxName,
			"version":             newVersion,
			"connection":          conn.Name,
			"connectionId":        conn.ID,
			"category":            string(conn.Category),
			"promotedFromPending": promoted,
		}
		return emitJSON(payload)
	}
	if promoted {
		fmt.Printf(
			"Published toolbox %s version %s with connection %s.\n",
			toolboxName, newVersion, conn.Name,
		)
	} else {
		fmt.Printf(
			"Attached connection %s to toolbox %s (now at version %s).\n",
			conn.Name, toolboxName, newVersion,
		)
	}
	return nil
}
