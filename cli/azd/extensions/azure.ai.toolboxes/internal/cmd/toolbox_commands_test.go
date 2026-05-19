// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"testing"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/connections"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunToolboxDeleteWith covers the live + per-version branches of
// runToolboxDeleteWith that do not depend on the azd config store. The
// pending-only path is exercised indirectly through TestEndpointBucketKey.
func TestRunToolboxDeleteWith_Branches(t *testing.T) {
	t.Run("not_found_no_pending_returns_validation_error", func(t *testing.T) {
		// The default getResults returns NotFound for unknown names.
		client := newMockToolboxClient("https://e/")
		err := runDeleteToolboxVersion(
			t.Context(), client, "https://e/", "missing",
			toolboxDeleteFlags{version: "1", force: true}, toolboxFlags{output: "table"},
		)
		requireLocalError(t, err, exterrors.CodeToolboxNotFound)
		assert.Empty(t, client.deleteVersionCalls)
	})

	t.Run("version_is_default_with_others_blocks_with_retarget_suggestion", func(t *testing.T) {
		client := newMockToolboxClient("https://e/")
		client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
			Name: "tb", DefaultVersion: "2",
		}}
		client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
			{Name: "tb", Version: "1"}, {Name: "tb", Version: "2"},
		}
		err := runDeleteToolboxVersion(
			t.Context(), client, "https://e/", "tb",
			toolboxDeleteFlags{version: "2", force: true}, toolboxFlags{output: "table"},
		)
		le := requireLocalError(t, err, exterrors.CodeDefaultVersionDelete)
		assert.Contains(t, le.Suggestion, "default-version")
		assert.Empty(t, client.deleteVersionCalls, "service must not be called")
	})

	t.Run("version_is_only_remaining_without_force_blocks", func(t *testing.T) {
		client := newMockToolboxClient("https://e/")
		client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
			Name: "tb", DefaultVersion: "1",
		}}
		client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
			{Name: "tb", Version: "1"},
		}
		err := runDeleteToolboxVersion(
			t.Context(), client, "https://e/", "tb",
			toolboxDeleteFlags{version: "1", force: false}, toolboxFlags{output: "table"},
		)
		requireLocalError(t, err, exterrors.CodeOnlyVersionDelete)
		assert.Empty(t, client.deleteVersionCalls)
	})

	t.Run("version_is_only_remaining_with_force_proceeds", func(t *testing.T) {
		client := newMockToolboxClient("https://e/")
		client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
			Name: "tb", DefaultVersion: "1",
		}}
		client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
			{Name: "tb", Version: "1"},
		}
		err := runDeleteToolboxVersion(
			t.Context(), client, "https://e/", "tb",
			toolboxDeleteFlags{version: "1", force: true}, toolboxFlags{output: "json"},
		)
		require.NoError(t, err)
		require.Len(t, client.deleteVersionCalls, 1)
		assert.Equal(t, "tb", client.deleteVersionCalls[0].name)
		assert.Equal(t, "1", client.deleteVersionCalls[0].version)
	})

	t.Run("non_default_version_with_force_proceeds", func(t *testing.T) {
		client := newMockToolboxClient("https://e/")
		client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
			Name: "tb", DefaultVersion: "5",
		}}
		err := runDeleteToolboxVersion(
			t.Context(), client, "https://e/", "tb",
			toolboxDeleteFlags{version: "3", force: true}, toolboxFlags{output: "json"},
		)
		require.NoError(t, err)
		require.Len(t, client.deleteVersionCalls, 1)
		assert.Equal(t, "3", client.deleteVersionCalls[0].version)
	})

	// Non-default version delete has no confirmation prompt.
	t.Run("non_default_version_without_force_does_not_prompt", func(t *testing.T) {
		client := newMockToolboxClient("https://e/")
		client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
			Name: "tb", DefaultVersion: "5",
		}}
		err := runDeleteToolboxVersion(
			t.Context(), client, "https://e/", "tb",
			toolboxDeleteFlags{version: "3", force: false}, toolboxFlags{output: "json"},
		)
		require.NoError(t, err)
		require.Len(t, client.deleteVersionCalls, 1,
			"non-default version delete must proceed without prompting")
	})
}

func TestRunToolboxDelete_NoPromptWithoutForce(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	// Parent-toolbox delete with --no-prompt and no --force must reject.
	err := runDeleteToolbox(
		t.Context(), client, "https://e/", "tb",
		toolboxDeleteFlags{},
		toolboxFlags{output: "table", noPrompt: true},
	)
	requireLocalError(t, err, exterrors.CodeMissingForceFlag)
}

func TestRunToolboxDelete_InvalidName(t *testing.T) {
	err := runToolboxDelete(
		t.Context(), "bad/name",
		toolboxDeleteFlags{force: true},
		toolboxFlags{output: "table"},
	)
	requireLocalError(t, err, exterrors.CodeInvalidToolboxName)
}

func TestRunToolboxShowWith_LiveAndVersionMissing(t *testing.T) {
	t.Run("default version live happy path", func(t *testing.T) {
		client := newMockToolboxClient("https://e/")
		client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
			Name: "tb", DefaultVersion: "1",
		}}
		client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
			Name: "tb", Version: "1", Tools: []map[string]any{
				{"type": "mcp", "name": "x", "project_connection_id": "/c/x"},
			},
		}}
		err := runToolboxShowWith(
			t.Context(), client, "https://e/", "tb",
			toolboxShowFlags{}, toolboxFlags{output: "json"},
		)
		require.NoError(t, err)
	})

	t.Run("explicit version missing returns validation error", func(t *testing.T) {
		client := newMockToolboxClient("https://e/")
		client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
			Name: "tb", DefaultVersion: "1",
		}}
		err := runToolboxShowWith(
			t.Context(), client, "https://e/", "tb",
			toolboxShowFlags{version: "9"}, toolboxFlags{output: "table"},
		)
		requireLocalError(t, err, exterrors.CodeToolboxNotFound)
	})
}

func TestRunToolboxListWith_MergesNoPending(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.listToolboxesResult = []azure.ToolboxObject{
		{Name: "alpha", DefaultVersion: "1"},
		{Name: "beta", DefaultVersion: "2"},
	}
	client.versionResults["alpha/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "alpha", Version: "1", Tools: []map[string]any{
			{"type": "mcp", "name": "t1"},
		},
	}}
	client.versionResults["beta/2"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "beta", Version: "2", Tools: []map[string]any{},
	}}

	err := runToolboxListWith(
		t.Context(), client, "https://e/", toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
}

func TestRunConnectionAddWith_DuplicateRejected(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1", Tools: []map[string]any{
			{"type": "mcp", "name": "x", "project_connection_id": "/c/x"},
		},
	}}

	resolver := newStubConnectionResolver()
	resolver.byName["x"] = &projectConnection{
		ID: "/c/x", Category: connections.ConnectionTypeRemoteTool, Name: "x", Target: "https://mcp",
	}

	err := runConnectionAddWith(
		t.Context(), client, resolver, newStubPendingStore(), "https://e/",
		"tb", "x", connectionAddFlags{}, toolboxFlags{output: "json"},
	)
	requireLocalError(t, err, exterrors.CodeDuplicateConnection)
	assert.Empty(t, client.createVersionCalls, "must not POST on duplicate")
}

func TestRunConnectionAddWith_AppendsAndPromotesDefault(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1", Description: "first", Tools: []map[string]any{
			{"type": "mcp", "name": "a", "project_connection_id": "/c/a"},
		},
	}}

	resolver := newStubConnectionResolver()
	resolver.byName["b"] = &projectConnection{
		ID: "/c/b", Category: connections.ConnectionTypeRemoteTool, Name: "b", Target: "https://mcp-b",
	}

	err := runConnectionAddWith(
		t.Context(), client, resolver, newStubPendingStore(), "https://e/",
		"tb", "b", connectionAddFlags{}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Equal(t, "first", client.createVersionCalls[0].req.Description, "description carried forward")
	assert.Len(t, client.createVersionCalls[0].req.Tools, 2)
	require.Len(t, client.setDefaultCalls, 1, "default_version must be retargeted")
}

func TestRunConnectionAddWith_ConnectionNotFound(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1", Tools: []map[string]any{},
	}}

	resolver := newStubConnectionResolver()
	err := runConnectionAddWith(
		t.Context(), client, resolver, newStubPendingStore(), "https://e/",
		"tb", "missing", connectionAddFlags{}, toolboxFlags{output: "table"},
	)
	requireLocalError(t, err, exterrors.CodeConnectionNotFound)
	assert.Empty(t, client.createVersionCalls)
}

func TestRunConnectionRemoveWith_LastToolBlocks(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1", Tools: []map[string]any{
			{"type": "mcp", "name": "a", "project_connection_id": "/c/a"},
		},
	}}
	resolver := newStubConnectionResolver()
	resolver.byName["a"] = &projectConnection{
		ID: "/c/a", Category: connections.ConnectionTypeRemoteTool, Name: "a",
	}

	err := runConnectionRemoveWith(
		t.Context(), client, resolver, "https://e/",
		"tb", "a", toolboxFlags{output: "table"},
	)
	requireLocalError(t, err, exterrors.CodeLastToolRemoval)
	assert.Empty(t, client.createVersionCalls)
}

func TestRunConnectionRemoveWith_FilteredAndPromoted(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1", Tools: []map[string]any{
			{"type": "mcp", "name": "a", "project_connection_id": "/c/a"},
			{"type": "mcp", "name": "b", "project_connection_id": "/c/b"},
		},
	}}
	resolver := newStubConnectionResolver()
	resolver.byName["a"] = &projectConnection{
		ID: "/c/a", Category: connections.ConnectionTypeRemoteTool, Name: "a",
	}

	err := runConnectionRemoveWith(
		t.Context(), client, resolver, "https://e/",
		"tb", "a", toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Len(t, client.createVersionCalls[0].req.Tools, 1)
	require.Len(t, client.setDefaultCalls, 1)
}

func TestRunConnectionRemoveWith_ConnectionNotInToolbox(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1", Tools: []map[string]any{
			{"type": "mcp", "name": "other", "project_connection_id": "/c/other"},
		},
	}}
	resolver := newStubConnectionResolver()
	resolver.byName["a"] = &projectConnection{
		ID: "/c/a", Category: connections.ConnectionTypeRemoteTool, Name: "a",
	}

	err := runConnectionRemoveWith(
		t.Context(), client, resolver, "https://e/",
		"tb", "a", toolboxFlags{output: "table"},
	)
	requireLocalError(t, err, exterrors.CodeConnectionNotInToolbox)
}

func TestRunConnectionListWith_EmitsAllShapes(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1", Tools: []map[string]any{
			{"type": "mcp", "name": "m", "project_connection_id": "/conn/m"},
			{
				"type": "azure_ai_search",
				"name": "s",
				"azure_ai_search": map[string]any{
					"indexes": []any{
						map[string]any{"project_connection_id": "/conn/s", "index_name": "i"},
					},
				},
			},
			{"type": "code_interpreter", "name": "ci"}, // not surfaced
		},
	}}

	err := runConnectionListWith(
		t.Context(), client, "tb", toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
}

func TestRunToolboxUpdate_MissingDefaultVersion(t *testing.T) {
	err := runToolboxUpdate(
		t.Context(), "tb",
		toolboxUpdateFlags{},
		toolboxFlags{output: "table"},
	)
	requireLocalError(t, err, exterrors.CodeMissingUpdateField)
}

// Pending-record promotion path: POST v1 with the carried-forward
// description, then clear the record.
func TestRunConnectionAddWith_PendingPromotion(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	resolver := newStubConnectionResolver()
	resolver.byName["my-mcp"] = &projectConnection{
		ID:       "/c/my-mcp",
		Category: connections.ConnectionTypeRemoteTool,
		Name:     "my-mcp",
		Target:   "https://mcp.example.com",
	}

	store := newStubPendingStore()
	store.records[store.key("https://e/", "tb")] = &PendingToolbox{
		Description: "Research-time toolset",
		CreatedAt:   "2026-05-12T10:23:00Z",
	}

	err := runConnectionAddWith(
		t.Context(), client, resolver, store, "https://e/",
		"tb", "my-mcp", connectionAddFlags{}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1, "v1 must be POSTed")
	assert.Equal(t, "Research-time toolset", client.createVersionCalls[0].req.Description,
		"description from pending record must be carried forward")
	assert.Len(t, client.createVersionCalls[0].req.Tools, 1)
	assert.Empty(t, client.setDefaultCalls, "first version is default automatically; no PATCH")
	assert.Equal(t, 1, store.clearCalls, "pending record must be cleared")
	assert.Empty(t, store.records, "pending record must be removed after success")
}

// A pending-store read failure must surface as Internal, not silently fall
// through to a misleading CodeToolboxNotFound.
func TestRunConnectionAddWith_PendingStoreFailureSurfaces(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	resolver := newStubConnectionResolver()
	resolver.byName["c"] = &projectConnection{
		ID: "/c/c", Category: connections.ConnectionTypeRemoteTool, Name: "c",
		Target: "https://mcp.example.com",
	}

	store := newStubPendingStore()
	store.getErr = errors.New("config read failed")

	err := runConnectionAddWith(
		t.Context(), client, resolver, store, "https://e/",
		"tb", "c", connectionAddFlags{}, toolboxFlags{output: "table"},
	)
	requireLocalError(t, err, exterrors.CodePendingToolboxStoreFailed)
	assert.Empty(t, client.createVersionCalls,
		"existing-toolbox branch must not be entered when the pending store fails")
}

// Client-side ^[A-Za-z0-9_-]+$ enforcement on tool entry names.
func TestBuildToolEntry_RejectsInvalidName(t *testing.T) {
	_, err := buildToolEntry(&projectConnection{
		ID:       "/c/x",
		Category: connections.ConnectionTypeRemoteTool,
		Name:     "tools.v1", // dot is not in ^[A-Za-z0-9_-]+$
		Target:   "https://mcp",
	}, "")
	le := requireLocalError(t, err, exterrors.CodeInvalidToolboxName)
	assert.Contains(t, le.Message, "tool entry name")
	assert.Contains(t, le.Message, "tools.v1")
}
