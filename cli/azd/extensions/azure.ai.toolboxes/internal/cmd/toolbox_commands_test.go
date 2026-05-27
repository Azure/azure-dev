// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"testing"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/connections"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunToolboxDeleteWith covers the live + per-version branches of
// runToolboxDeleteWith.
func TestRunToolboxDeleteWith_Branches(t *testing.T) {
	t.Run("not_found_returns_validation_error", func(t *testing.T) {
		// The default getResults returns NotFound for unknown names.
		client := newMockToolboxClient("https://e/")
		err := runDeleteToolboxVersion(
			t.Context(), client, "missing",
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
			t.Context(), client, "tb",
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
			t.Context(), client, "tb",
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
			t.Context(), client, "tb",
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
			t.Context(), client, "tb",
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
			t.Context(), client, "tb",
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
		t.Context(), client, "tb",
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

func TestRunToolboxListWith(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.listToolboxesResult = []azure.ToolboxObject{
		{Name: "alpha", DefaultVersion: "1"},
		{Name: "beta", DefaultVersion: "2"},
	}

	err := runToolboxListWith(
		t.Context(), client, toolboxFlags{output: "json"},
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
		t.Context(), client, resolver, "https://e/",
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
		t.Context(), client, resolver, "https://e/",
		"tb", "b", connectionAddFlags{}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Equal(t, "first", client.createVersionCalls[0].req.Description, "description carried forward")
	assert.Len(t, client.createVersionCalls[0].req.Tools, 2)
	require.Len(t, client.setDefaultCalls, 1, "default version must be retargeted")
}

func TestRunConnectionAddWith_ConnectionNotFound(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1", Tools: []map[string]any{},
	}}

	resolver := newStubConnectionResolver()
	err := runConnectionAddWith(
		t.Context(), client, resolver, "https://e/",
		"tb", "missing", connectionAddFlags{}, toolboxFlags{output: "table"},
	)
	requireLocalError(t, err, exterrors.CodeConnectionNotFound)
	assert.Empty(t, client.createVersionCalls)
}

// Batch path via --from-file: multiple connections should land in
// a single new toolbox version (one CreateToolboxVersion, one SetDefaultVersion).
func TestRunConnectionAddWith_FromFileAddsMultipleToolsSingleVersion(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1", Tools: []map[string]any{
			{"type": "mcp", "project_connection_id": "/c/existing"},
		},
	}}

	resolver := newStubConnectionResolver()
	resolver.byName["my-mcp"] = &projectConnection{
		ID:       "/c/my-mcp",
		Category: connections.ConnectionTypeRemoteTool,
		Name:     "my-mcp",
		Target:   "https://mcp.example.com",
	}
	resolver.byName["search"] = &projectConnection{
		ID:       "/c/search",
		Category: connections.ConnectionTypeCognitiveSearch,
		Name:     "search",
	}

	inputPath := t.TempDir() + "/add-tools.json"
	err := os.WriteFile(inputPath, []byte(`
{
  "connections": [
    {"name":"my-mcp"},
    {"name":"search", "index":"docs"}
  ]
}`), 0o600)
	require.NoError(t, err)

	err = runConnectionAddWith(
		t.Context(), client, resolver, "https://e/",
		"tb", "", connectionAddFlags{fromFile: inputPath}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1, "single version increment for batch input")
	assert.Len(t, client.createVersionCalls[0].req.Tools, 3, "existing + 2 additions")
	require.Len(t, client.setDefaultCalls, 1)
}

// Public entry-point validation: empty connection without --from-file.
func TestRunConnectionAdd_RejectsEmptyConnectionWithoutFromFile(t *testing.T) {
	err := runConnectionAdd(
		t.Context(), "tb", "",
		connectionAddFlags{},
		toolboxFlags{output: "table"},
		newStubConnectionResolver(),
	)
	requireLocalError(t, err, exterrors.CodeInvalidPositionalArg)
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
		"tb", "a", connectionRemoveFlags{force: true}, toolboxFlags{output: "table"},
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
		"tb", "a", connectionRemoveFlags{force: true}, toolboxFlags{output: "json"},
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
		"tb", "a", connectionRemoveFlags{force: true}, toolboxFlags{output: "table"},
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

func TestRunToolboxCreateWith_FromFileCreatesInitialVersion(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	resolver := newStubConnectionResolver()
	resolver.byName["mcp"] = &projectConnection{
		ID: "/c/mcp", Category: connections.ConnectionTypeRemoteTool, Name: "mcp",
		Target: "https://mcp.example.com",
	}

	inputPath := t.TempDir() + "/create.yaml"
	err := os.WriteFile(inputPath, []byte(`
description: toolbox from file
connections:
  - name: mcp
`), 0o600)
	require.NoError(t, err)

	err = runToolboxCreateWith(
		t.Context(), client, resolver, "https://e/", "tb",
		toolboxCreateFlags{fromFile: inputPath}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Equal(t, "toolbox from file", client.createVersionCalls[0].req.Description)
	assert.Len(t, client.createVersionCalls[0].req.Tools, 1)
}

func TestRunToolboxCreateWith_SkillsFromFile(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	resolver := newStubConnectionResolver()
	resolver.byName["mcp"] = &projectConnection{
		ID: "/c/mcp", Category: connections.ConnectionTypeRemoteTool, Name: "mcp",
		Target: "https://mcp.example.com",
	}

	inputPath := t.TempDir() + "/create.yaml"
	require.NoError(t, os.WriteFile(inputPath, []byte(`
description: tb with skills
connections:
  - name: mcp
skills:
  - name: pinned
    version: "3"
  - name: unpinned
`), 0o600))

	err := runToolboxCreateWith(
		t.Context(), client, resolver, "https://e/", "tb",
		toolboxCreateFlags{fromFile: inputPath},
		toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)

	skills := client.createVersionCalls[0].req.Skills
	require.Len(t, skills, 2)

	byName := map[string]map[string]any{}
	for _, s := range skills {
		n, _ := s["name"].(string)
		byName[n] = s
	}
	require.Contains(t, byName, "pinned")
	require.Contains(t, byName, "unpinned")
	assert.Equal(t, "skill_reference", byName["pinned"]["type"])
	assert.Equal(t, "3", byName["pinned"]["version"])
	_, hasVersion := byName["unpinned"]["version"]
	assert.False(t, hasVersion, "skill without version must omit the version key")
}

func TestRunToolboxCreateWith_DuplicateSkillRejected(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	resolver := newStubConnectionResolver()
	resolver.byName["mcp"] = &projectConnection{
		ID: "/c/mcp", Category: connections.ConnectionTypeRemoteTool, Name: "mcp",
		Target: "https://mcp.example.com",
	}

	inputPath := t.TempDir() + "/create.yaml"
	require.NoError(t, os.WriteFile(inputPath, []byte(`
description: tb
connections:
  - name: mcp
skills:
  - name: dup
  - name: dup
    version: "2"
`), 0o600))

	err := runToolboxCreateWith(
		t.Context(), client, resolver, "https://e/", "tb",
		toolboxCreateFlags{fromFile: inputPath},
		toolboxFlags{output: "json"},
	)
	requireLocalError(t, err, exterrors.CodeDuplicateSkill)
	assert.Empty(t, client.createVersionCalls, "no version should be created when local validation fails")
}

func TestRunToolboxCreateWith_AlreadyExists(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}}

	err := runToolboxCreateWith(
		t.Context(), client, newStubConnectionResolver(), "https://e/", "tb",
		toolboxCreateFlags{}, toolboxFlags{output: "table"},
	)
	requireLocalError(t, err, exterrors.CodeInvalidToolboxName)
}

func TestRunToolboxCreateWith_NoConnectionsRejected(t *testing.T) {
	client := newMockToolboxClient("https://e/")

	err := runToolboxCreateWith(
		t.Context(), client, newStubConnectionResolver(), "https://e/", "tb",
		toolboxCreateFlags{}, toolboxFlags{output: "table"},
	)
	requireLocalError(t, err, exterrors.CodeInvalidToolboxName)
	assert.Empty(t, client.createVersionCalls)
}

// Client-side ^[A-Za-z0-9_-]+$ enforcement on tool entry names.
func TestBuildToolEntry_RejectsInvalidName(t *testing.T) {
	_, err := buildToolEntry(&projectConnection{
		ID:       "/c/x",
		Category: connections.ConnectionTypeRemoteTool,
		Name:     "tools.v1", // dot is not in ^[A-Za-z0-9_-]+$
		Target:   "https://mcp",
	}, "", "")
	le := requireLocalError(t, err, exterrors.CodeInvalidToolboxName)
	assert.Contains(t, le.Message, "tool entry name")
	assert.Contains(t, le.Message, "tools.v1")
}

func TestRunToolboxVersionListWith_Success(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "2",
	}}
	client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
		{
			Name: "tb", Version: "1", CreatedAt: 1715600000, Description: "old",
			Tools: []map[string]any{{"type": "mcp"}},
		},
		{
			Name: "tb", Version: "2", CreatedAt: 1715700000, Description: "current",
			Tools: []map[string]any{{"type": "mcp"}, {"type": "web_search"}},
		},
		{
			Name: "tb", Version: "10", CreatedAt: 1715800000, Description: "new",
			Tools: []map[string]any{},
		},
	}

	err := runToolboxVersionListWith(
		t.Context(), client, "tb", toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
}

func TestRunToolboxVersionListWith_ToolboxNotFound(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	err := runToolboxVersionListWith(
		t.Context(), client, "missing", toolboxFlags{output: "table"},
	)
	requireLocalError(t, err, exterrors.CodeToolboxNotFound)
}

func TestRunToolboxVersionListWith_ListVersionsServiceError(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.listVersionsErr = errors.New("list versions failed")

	err := runToolboxVersionListWith(
		t.Context(), client, "tb", toolboxFlags{output: "json"},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list versions failed")
}

func TestRunConnectionRemove_NoPromptWithoutForce(t *testing.T) {
	err := runConnectionRemove(
		t.Context(), "tb", "conn",
		connectionRemoveFlags{force: false},
		toolboxFlags{output: "table", noPrompt: true},
		newStubConnectionResolver(),
	)
	requireLocalError(t, err, exterrors.CodeMissingForceFlag)
}

// Carry-forward: skills attached to the current default version must survive
// across new versions published by `connection add`.
func TestRunConnectionAddWith_CarriesForwardSkills(t *testing.T) {
	skills := []map[string]any{
		{"type": "skill_reference", "name": "alpha", "version": "1"},
		{"type": "skill_reference", "name": "beta"},
	}
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1", Description: "first",
		Tools: []map[string]any{
			{"type": "mcp", "name": "a", "project_connection_id": "/c/a"},
		},
		Skills: skills,
	}}
	resolver := newStubConnectionResolver()
	resolver.byName["b"] = &projectConnection{
		ID: "/c/b", Category: connections.ConnectionTypeRemoteTool, Name: "b", Target: "https://mcp-b",
	}

	err := runConnectionAddWith(
		t.Context(), client, resolver, "https://e/",
		"tb", "b", connectionAddFlags{}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Equal(t, skills, client.createVersionCalls[0].req.Skills,
		"skills must be carried forward verbatim into the new version")
}

// Carry-forward: skills attached to the current default version must survive
// across new versions published by `connection remove`.
func TestRunConnectionRemoveWith_CarriesForwardSkills(t *testing.T) {
	skills := []map[string]any{
		{"type": "skill_reference", "name": "alpha"},
	}
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{
			{"type": "mcp", "name": "a", "project_connection_id": "/c/a"},
			{"type": "mcp", "name": "b", "project_connection_id": "/c/b"},
		},
		Skills: skills,
	}}
	resolver := newStubConnectionResolver()
	resolver.byName["a"] = &projectConnection{
		ID: "/c/a", Category: connections.ConnectionTypeRemoteTool, Name: "a",
	}

	err := runConnectionRemoveWith(
		t.Context(), client, resolver, "https://e/",
		"tb", "a", connectionRemoveFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Equal(t, skills, client.createVersionCalls[0].req.Skills,
		"skills must be carried forward verbatim into the new version")
}
