// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

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

The tool entry shape is inferred from the connection's category:
  RemoteTool       → mcp tool wired to the connection's MCP server URL
  CognitiveSearch  → azure_ai_search tool (requires --index)
                     Note: "CognitiveSearch" is the category for Azure AI Search.
Other categories are rejected.

If the toolbox has a local pending record (from 'toolbox create'), v1 is
published with this connection as the only tool. Otherwise the current
default version is fetched, the tool entry is appended, and a new default
version is published.`,
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
		"Index name (required when the connection's category is CognitiveSearch, i.e. Azure AI Search).",
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
		// The version has been published. A pending-store clear failure is
		// non-fatal: log it but proceed so the user sees the success path.
		if _, err := store.Clear(ctx, endpoint, toolboxName); err != nil {
			log.Printf(
				"toolbox connection add: %q v%s was published, but the local pending record could not be cleared: %v",
				toolboxName, created.Version, err,
			)
		}
		return emitConnectionAddResult(toolboxName, created.Version, conn, parent.output, true, endpoint)
	}

	// Existing-toolbox path: fetch default → append → POST → PATCH default_version.
	tb, err := client.GetToolbox(ctx, toolboxName)
	if err != nil {
		if isAzureNotFound(err) {
			return exterrors.Dependency(
				exterrors.CodeToolboxNotFound,
				fmt.Sprintf("toolbox %q not found", toolboxName),
				fmt.Sprintf(
					"run 'azd ai toolbox create %q' first, then re-run 'connection add'",
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
		// The new version exists but isn't the default. Surface this so the
		// user can recover with `toolbox update --default-version <v>` rather
		// than silently losing the connection add.
		return exterrors.Dependency(
			exterrors.CodeSetDefaultVersionFailed,
			fmt.Sprintf(
				"toolbox %q version %q was created but could not be promoted to default: %s",
				toolboxName, created.Version, err,
			),
			fmt.Sprintf(
				"run `azd ai toolbox update %q --default-version %q` to retarget the default",
				toolboxName, created.Version,
			),
		)
	}

	return emitConnectionAddResult(toolboxName, created.Version, conn, parent.output, false, endpoint)
}

// emitConnectionAddResult prints the standard output for a successful add. The
// resolved endpoint is included so the user can paste it into agent code.
func emitConnectionAddResult(
	toolboxName, newVersion string, conn *projectConnection, output string, promoted bool, endpoint string,
) error {
	mcpURL := buildToolboxMcpURL(endpoint, toolboxName, newVersion)
	if output == "json" {
		payload := map[string]any{
			"toolbox":             toolboxName,
			"version":             newVersion,
			"connection":          conn.Name,
			"connectionId":        conn.ID,
			"category":            string(conn.Category),
			"promotedFromPending": promoted,
			"endpoint":            mcpURL,
		}
		return emitJSON(payload)
	}
	if promoted {
		fmt.Printf("Published toolbox %s version %s with connection %s.\n",
			toolboxName, newVersion, conn.Name)
	} else {
		fmt.Printf("Attached connection %s to toolbox %s (now at version %s).\n",
			conn.Name, toolboxName, newVersion)
	}
	// Surface the MCP endpoint so the dev can wire it into agent code. Suggest
	// `azd env set` when running inside an azd project.
	envVar := strings.ReplaceAll(strings.ToUpper(toolboxName), "-", "_") + "_MCP_ENDPOINT"
	fmt.Printf("\nEndpoint: %s\n", mcpURL)
	fmt.Printf("Save it as an env var:\n  azd env set %s %s\n", envVar, mcpURL)
	return nil
}
