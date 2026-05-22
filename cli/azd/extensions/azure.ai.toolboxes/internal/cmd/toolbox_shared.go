// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"

	"azure.ai.toolboxes/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// toolboxNotFoundOrService maps a GetToolbox / GetToolboxVersion error to the
// right structured error: Dependency(CodeToolboxNotFound) on 404, ServiceError
// otherwise.
func toolboxNotFoundOrService(err error, name, op string) error {
	if isAzureNotFound(err) {
		return exterrors.Dependency(
			exterrors.CodeToolboxNotFound,
			fmt.Sprintf("toolbox %q not found", name),
			"run 'azd ai toolbox list' to see available toolboxes",
		)
	}
	return exterrors.ServiceFromAzure(err, op)
}

// forEachToolConnectionID invokes fn for every project_connection_id reference
// in tools[] (top-level on mcp entries, nested under azure_ai_search.indexes
// on search entries). fn returns true to stop early.
func forEachToolConnectionID(tools []map[string]any, fn func(connID string) bool) {
	for _, t := range tools {
		if toolEntryReferences(t, func(id string) bool { return fn(id) }) {
			return
		}
	}
}

// toolEntryReferences runs match against every connection ID referenced by a
// single tool entry and returns true on the first hit. Recognized shapes:
//   - top-level `project_connection_id` (mcp, a2a_preview)
//   - `azure_ai_search.indexes[].project_connection_id`
//   - `custom_search_configuration.project_connection_id` (web_search)
func toolEntryReferences(t map[string]any, match func(connID string) bool) bool {
	if id, ok := t["project_connection_id"].(string); ok && id != "" && match(id) {
		return true
	}
	if search, ok := t["azure_ai_search"].(map[string]any); ok {
		if indexes, ok := search["indexes"].([]any); ok {
			for _, idx := range indexes {
				m, ok := idx.(map[string]any)
				if !ok {
					continue
				}
				if id, ok := m["project_connection_id"].(string); ok && id != "" && match(id) {
					return true
				}
			}
		}
	}
	if cfg, ok := t["custom_search_configuration"].(map[string]any); ok {
		if id, ok := cfg["project_connection_id"].(string); ok && id != "" && match(id) {
			return true
		}
	}
	return false
}

// emitJSON marshals payload as indented JSON to stdout.
func emitJSON(payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// withAzdClient opens the azd client, invokes fn, and closes the client.
// A client-open failure is surfaced as Internal(CodeAzdClientFailed).
func withAzdClient(fn func(c *azdext.AzdClient) error) error {
	c, err := azdext.NewAzdClient()
	if err != nil {
		return exterrors.Internal(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("failed to create azd client: %s", err),
		)
	}
	defer c.Close()
	return fn(c)
}
