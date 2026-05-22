// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/connections"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newToolboxConnectionCommand returns the `azd ai toolbox connection` parent.
func newToolboxConnectionCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use:   "connection",
		Short: "Manage the connection-backed tools attached to a toolbox.",
		Long: `Manage the connection-backed tools attached to a toolbox.

Tools are project connections. Supported categories: RemoteTool (MCP),
CognitiveSearch (Azure AI Search), RemoteA2A, and GroundingWithCustomSearch.
Each mutation publishes a new immutable version and retargets the toolbox
default.`,
	}
	cmd.AddCommand(newToolboxConnectionAddCommand(extCtx))
	cmd.AddCommand(newToolboxConnectionRemoveCommand(extCtx))
	cmd.AddCommand(newToolboxConnectionListCommand(extCtx))
	return cmd
}

// buildToolEntry returns the tool-entry map appropriate for the connection's
// category. Enforces per-input flag rules (--index, --instance-name) and the
// `tool.name` regex.
func buildToolEntry(conn *projectConnection, index, instanceName string) (map[string]any, error) {
	if err := validateToolName(conn.Name); err != nil {
		return nil, err
	}
	// Normalize whitespace-only inputs up front so cross-category flag
	// rejection and required-input validation agree (e.g. `--index "   "`
	// should not be treated as "user supplied a value").
	index = strings.TrimSpace(index)
	instanceName = strings.TrimSpace(instanceName)
	// --index is only meaningful for CognitiveSearch; reject elsewhere.
	if index != "" && conn.Category != connections.ConnectionTypeCognitiveSearch {
		return nil, exterrors.Validation(
			exterrors.CodeUnsupportedIndexFlag,
			fmt.Sprintf(
				"--index is only valid for CognitiveSearch connections, "+
					"connection %q has category %q",
				conn.Name, conn.Category,
			),
			"omit --index for non-CognitiveSearch connections",
		)
	}
	// --instance-name is only meaningful for GroundingWithCustomSearch.
	if instanceName != "" && conn.Category != connections.ConnectionTypeGroundingWithCustomSearch {
		return nil, exterrors.Validation(
			exterrors.CodeUnsupportedInstanceNameFlag,
			fmt.Sprintf(
				"--instance-name is only valid for GroundingWithCustomSearch connections, "+
					"connection %q has category %q",
				conn.Name, conn.Category,
			),
			"omit --instance-name for non-GroundingWithCustomSearch connections",
		)
	}
	switch conn.Category {
	case connections.ConnectionTypeRemoteTool:
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

	case connections.ConnectionTypeCognitiveSearch:
		if index == "" {
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

	case connections.ConnectionTypeRemoteA2A:
		return map[string]any{
			"type":                  "a2a_preview",
			"name":                  conn.Name,
			"project_connection_id": conn.ID,
		}, nil

	case connections.ConnectionTypeGroundingWithCustomSearch:
		if instanceName == "" {
			return nil, exterrors.Validation(
				exterrors.CodeMissingInstanceName,
				fmt.Sprintf(
					"connection %q is a GroundingWithCustomSearch connection; "+
						"--instance-name is required",
					conn.Name,
				),
				"pass --instance-name <name> with the Bing custom-search configuration name",
			)
		}
		return map[string]any{
			"type": "web_search",
			"name": conn.Name,
			"custom_search_configuration": map[string]any{
				"project_connection_id": conn.ID,
				"instance_name":         instanceName,
			},
		}, nil

	default:
		return nil, exterrors.Validation(
			exterrors.CodeUnsupportedConnectionCategory,
			fmt.Sprintf(
				"connection %q has category %q which is not supported as a toolbox tool today; "+
					"supported categories: RemoteTool (MCP), CognitiveSearch (Azure AI Search), "+
					"RemoteA2A, GroundingWithCustomSearch",
				conn.Name, conn.Category,
			),
			"use one of the supported connection categories, "+
				"or file an issue requesting support for the connection category you need",
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

// filterOutConnection returns tools[] with every entry whose
// project_connection_id matches connID stripped (top-level and nested forms).
// `removed` reports whether at least one entry was filtered.
func filterOutConnection(tools []map[string]any, connID string) (result []map[string]any, removed bool) {
	for _, t := range tools {
		if toolEntryReferences(t, func(id string) bool { return id == connID }) {
			removed = true
			continue
		}
		result = append(result, t)
	}
	return result, removed
}

// shortConnectionName extracts the connection's short name from the trailing
// segment of its ARM ID (e.g. ".../connections/my-mcp" → "my-mcp"). Falls back
// to the full id when no slash is present.
func shortConnectionName(id string) string {
	if id == "" {
		return ""
	}
	if i := strings.LastIndex(id, "/"); i >= 0 && i < len(id)-1 {
		return id[i+1:]
	}
	return id
}
