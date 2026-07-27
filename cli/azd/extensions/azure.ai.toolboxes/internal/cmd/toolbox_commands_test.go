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
		assert.Contains(t, le.Suggestion, "azd ai toolbox publish")
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
		Policies: &azure.ToolboxPolicies{
			RaiConfig: &azure.RaiConfig{RaiPolicyName: "Microsoft.Default"},
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
	req := client.createVersionCalls[0].req
	assert.Equal(t, "first", req.Description, "description carried forward")
	assert.Len(t, req.Tools, 2)
	require.NotNil(t, req.Policies, "policies must be carried forward")
	require.NotNil(t, req.Policies.RaiConfig)
	assert.Equal(t, "Microsoft.Default", req.Policies.RaiConfig.RaiPolicyName)
	assert.Empty(t, client.setDefaultCalls, "mutation verbs no longer auto-promote default")
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
	assert.Empty(t, client.setDefaultCalls)
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
		"tb", []string{"a"},
		connectionRemoveFlags{force: true},
		toolboxFlags{output: "table"},
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
		Policies: &azure.ToolboxPolicies{
			RaiConfig: &azure.RaiConfig{RaiPolicyName: "Microsoft.Default"},
		},
	}}
	resolver := newStubConnectionResolver()
	resolver.byName["a"] = &projectConnection{
		ID: "/c/a", Category: connections.ConnectionTypeRemoteTool, Name: "a",
	}

	err := runConnectionRemoveWith(
		t.Context(), client, resolver, "https://e/",
		"tb", []string{"a"},
		connectionRemoveFlags{force: true},
		toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	req := client.createVersionCalls[0].req
	assert.Len(t, req.Tools, 1)
	require.NotNil(t, req.Policies, "policies must be carried forward on remove")
	assert.Equal(t, "Microsoft.Default", req.Policies.RaiConfig.RaiPolicyName)
	assert.Empty(t, client.setDefaultCalls)
}

func TestRunConnectionRemoveWith_ConnectionNotInToolbox(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}}
	client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
		{Name: "tb", Version: "1"}, {Name: "tb", Version: "2"},
	}
	client.versionResults["tb/2"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "2", Tools: []map[string]any{
			{"type": "mcp", "name": "other", "project_connection_id": "/c/other"},
		},
	}}
	resolver := newStubConnectionResolver()
	resolver.byName["a"] = &projectConnection{
		ID: "/c/a", Category: connections.ConnectionTypeRemoteTool, Name: "a",
	}

	err := runConnectionRemoveWith(
		t.Context(), client, resolver, "https://e/",
		"tb", []string{"a"},
		connectionRemoveFlags{force: true},
		toolboxFlags{output: "table"},
	)
	localErr := requireLocalError(t, err, exterrors.CodeConnectionNotInToolbox)
	assert.Contains(t, localErr.Suggestion, `azd ai toolbox show "tb" --version "2"`)
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

func TestRunToolboxPublish_WhitespaceVersion(t *testing.T) {
	err := runToolboxPublish(
		t.Context(), "tb", "  ",
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

	envCalls := stubToolboxEndpointEnv(t)

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

	// The versioned MCP endpoint is written to the active azd environment.
	require.Len(t, *envCalls, 1)
	assert.Equal(t, "tb", (*envCalls)[0].name)
	assert.Equal(t,
		"https://e/toolboxes/tb/versions/v1/mcp?api-version=v1",
		(*envCalls)[0].value,
	)
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

func TestRunToolboxCreateWith_NoEntriesRejected(t *testing.T) {
	client := newMockToolboxClient("https://e/")

	err := runToolboxCreateWith(
		t.Context(), client, newStubConnectionResolver(), "https://e/", "tb",
		toolboxCreateFlags{}, toolboxFlags{output: "table"},
	)
	requireLocalError(t, err, exterrors.CodeInvalidToolboxName)
	assert.Empty(t, client.createVersionCalls)
}

// policies.rai_config in --from-file is forwarded to the data-plane request
// as ToolboxPolicies.RaiConfig.RaiPolicyName.
func TestRunToolboxCreateWith_ForwardsRaiPolicy(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	resolver := newStubConnectionResolver()
	resolver.byName["mcp"] = &projectConnection{
		ID: "/c/mcp", Category: connections.ConnectionTypeRemoteTool, Name: "mcp",
		Target: "https://mcp.example.com",
	}

	inputPath := t.TempDir() + "/create.yaml"
	require.NoError(t, os.WriteFile(inputPath, []byte(`
description: tb with rai
connections:
  - name: mcp
policies:
  rai_config:
    rai_policy_name: Microsoft.Default
`), 0o600))

	err := runToolboxCreateWith(
		t.Context(), client, resolver, "https://e/", "tb",
		toolboxCreateFlags{fromFile: inputPath}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	req := client.createVersionCalls[0].req
	require.NotNil(t, req.Policies)
	require.NotNil(t, req.Policies.RaiConfig)
	assert.Equal(t, "Microsoft.Default", req.Policies.RaiConfig.RaiPolicyName)
}

// policies.rai_config with an empty/whitespace name is rejected locally with a
// fix-it suggestion rather than forwarded to the data plane.
func TestRunToolboxCreateWith_EmptyRaiPolicyNameRejected(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	resolver := newStubConnectionResolver()
	resolver.byName["mcp"] = &projectConnection{
		ID: "/c/mcp", Category: connections.ConnectionTypeRemoteTool, Name: "mcp",
		Target: "https://mcp.example.com",
	}

	inputPath := t.TempDir() + "/create.yaml"
	require.NoError(t, os.WriteFile(inputPath, []byte(`
connections:
  - name: mcp
policies:
  rai_config:
    rai_policy_name: "   "
`), 0o600))

	err := runToolboxCreateWith(
		t.Context(), client, resolver, "https://e/", "tb",
		toolboxCreateFlags{fromFile: inputPath}, toolboxFlags{output: "table"},
	)
	requireLocalError(t, err, exterrors.CodeInvalidParameter)
	assert.Empty(t, client.createVersionCalls)
}

// A tools-only --from-file payload (no connections) is valid and the raw
// entries reach the data plane verbatim.
func TestRunToolboxCreateWith_ToolsOnlyFromFile(t *testing.T) {
	client := newMockToolboxClient("https://e/")

	inputPath := t.TempDir() + "/create.yaml"
	require.NoError(t, os.WriteFile(inputPath, []byte(`
description: connectionless toolbox
tools:
  - type: web_search
    name: web
  - type: file_search
    name: files
`), 0o600))

	err := runToolboxCreateWith(
		t.Context(), client, newStubConnectionResolver(), "https://e/", "tb",
		toolboxCreateFlags{fromFile: inputPath}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	tools := client.createVersionCalls[0].req.Tools
	require.Len(t, tools, 2)
	assert.Equal(t, "web_search", tools[0]["type"])
	assert.Equal(t, "file_search", tools[1]["type"])
}

// Preview toolbox tools are forwarded in the exact shape defined by the
// Foundry toolbox API. Type-specific validation remains service-owned.
func TestRunToolboxCreateWith_NewPreviewTools(t *testing.T) {
	client := newMockToolboxClient("https://e/")

	inputPath := t.TempDir() + "/create.yaml"
	require.NoError(t, os.WriteFile(inputPath, []byte(`
tools:
  - type: work_iq_preview
    name: work-iq
    project_connection_id: /connections/work-iq
  - type: fabric_iq_preview
    name: fabric-iq
    project_connection_id: /connections/fabric-iq
    server_label: fabric
    server_url: https://fabric.example.com/mcp
    require_approval: never
  - type: toolbox_search_preview
    name: toolbox-search
`), 0o600))

	err := runToolboxCreateWith(
		t.Context(), client, newStubConnectionResolver(), "https://e/", "tb",
		toolboxCreateFlags{fromFile: inputPath}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Equal(t, []map[string]any{
		{
			"type":                  "work_iq_preview",
			"name":                  "work-iq",
			"project_connection_id": "/connections/work-iq",
		},
		{
			"type":                  "fabric_iq_preview",
			"name":                  "fabric-iq",
			"project_connection_id": "/connections/fabric-iq",
			"server_label":          "fabric",
			"server_url":            "https://fabric.example.com/mcp",
			"require_approval":      "never",
		},
		{"type": "toolbox_search_preview", "name": "toolbox-search"},
	}, client.createVersionCalls[0].req.Tools)
}

// Connection-backed entries come first, raw tools[] entries after.
func TestRunToolboxCreateWith_MixedConnectionsAndTools(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	resolver := newStubConnectionResolver()
	resolver.byName["mcp"] = &projectConnection{
		ID: "/c/mcp", Category: connections.ConnectionTypeRemoteTool, Name: "mcp",
		Target: "https://mcp.example.com",
	}

	inputPath := t.TempDir() + "/create.yaml"
	require.NoError(t, os.WriteFile(inputPath, []byte(`
connections:
  - name: mcp
tools:
  - type: web_search
    name: web
`), 0o600))

	err := runToolboxCreateWith(
		t.Context(), client, resolver, "https://e/", "tb",
		toolboxCreateFlags{fromFile: inputPath}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	tools := client.createVersionCalls[0].req.Tools
	require.Len(t, tools, 2)
	assert.Equal(t, "mcp", tools[0]["type"])
	assert.Equal(t, "web_search", tools[1]["type"])
}

// A tools[] entry missing the discriminator `type` is rejected locally.
func TestRunToolboxCreateWith_RawToolMissingType(t *testing.T) {
	client := newMockToolboxClient("https://e/")

	inputPath := t.TempDir() + "/create.yaml"
	require.NoError(t, os.WriteFile(inputPath, []byte(`
tools:
  - name: oops
`), 0o600))

	err := runToolboxCreateWith(
		t.Context(), client, newStubConnectionResolver(), "https://e/", "tb",
		toolboxCreateFlags{fromFile: inputPath}, toolboxFlags{output: "table"},
	)
	requireLocalError(t, err, exterrors.CodeMissingToolType)
	assert.Empty(t, client.createVersionCalls)
}

// A raw tools[] entry whose `name` violates the service regex is rejected
// locally rather than producing a generic 400. Covers invalid characters,
// explicit empty string, and non-string YAML scalars.
func TestRunToolboxCreateWith_RawToolInvalidName(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "invalid chars",
			yaml: `
tools:
  - type: web_search
    name: bad.name
`,
			wantErr: exterrors.CodeInvalidToolboxName,
		},
		{
			name: "empty string",
			yaml: `
tools:
  - type: web_search
    name: ""
`,
			wantErr: exterrors.CodeInvalidToolboxName,
		},
		{
			name: "non-string scalar",
			yaml: `
tools:
  - type: web_search
    name: 42
`,
			wantErr: exterrors.CodeInvalidParameter,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newMockToolboxClient("https://e/")
			inputPath := t.TempDir() + "/create.yaml"
			require.NoError(t, os.WriteFile(inputPath, []byte(tc.yaml), 0o600))

			err := runToolboxCreateWith(
				t.Context(), client, newStubConnectionResolver(), "https://e/", "tb",
				toolboxCreateFlags{fromFile: inputPath}, toolboxFlags{output: "table"},
			)
			requireLocalError(t, err, tc.wantErr)
			assert.Empty(t, client.createVersionCalls)
		})
	}
}

// Two entries sharing the same `name` collide regardless of source. A common
// mistake is a raw tool whose name matches an attached connection's short name.
func TestRunToolboxCreateWith_DuplicateToolNameRejected(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	resolver := newStubConnectionResolver()
	resolver.byName["mcp"] = &projectConnection{
		ID: "/c/mcp", Category: connections.ConnectionTypeRemoteTool, Name: "mcp",
		Target: "https://mcp.example.com",
	}

	inputPath := t.TempDir() + "/create.yaml"
	require.NoError(t, os.WriteFile(inputPath, []byte(`
connections:
  - name: mcp
tools:
  - type: web_search
    name: mcp
`), 0o600))

	err := runToolboxCreateWith(
		t.Context(), client, resolver, "https://e/", "tb",
		toolboxCreateFlags{fromFile: inputPath}, toolboxFlags{output: "table"},
	)
	requireLocalError(t, err, exterrors.CodeDuplicateToolName)
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
	err := runConnectionRemove(t.Context(), "tb", []string{"conn"},
		connectionRemoveFlags{force: false},
		toolboxFlags{output: "table", noPrompt: true},
		newStubConnectionResolver(),
	)
	requireLocalError(t, err, exterrors.CodeMissingForceFlag)
}

// Carry-forward: skills attached to the current default version must survive
// across new versions created by `connection add`.
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
// across new versions created by `connection remove`.
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
		"tb", []string{"a"},
		connectionRemoveFlags{force: true},
		toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Equal(t, skills, client.createVersionCalls[0].req.Skills,
		"skills must be carried forward verbatim into the new version")
}

// Batch removal via variadic positionals.
func TestRunConnectionRemoveWith_VariadicPositionals(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{
			{"type": "mcp", "name": "a", "project_connection_id": "/c/a"},
			{"type": "mcp", "name": "b", "project_connection_id": "/c/b"},
			{"type": "mcp", "name": "c", "project_connection_id": "/c/c"},
		},
	}}
	resolver := newStubConnectionResolver()
	resolver.byName["a"] = &projectConnection{ID: "/c/a", Name: "a", Category: connections.ConnectionTypeRemoteTool}
	resolver.byName["b"] = &projectConnection{ID: "/c/b", Name: "b", Category: connections.ConnectionTypeRemoteTool}

	err := runConnectionRemoveWith(
		t.Context(), client, resolver, "https://e/",
		"tb", []string{"a", "b"},
		connectionRemoveFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1, "one new version created for the whole batch")
	require.Len(t, client.createVersionCalls[0].req.Tools, 1)
	assert.Equal(t, "/c/c", client.createVersionCalls[0].req.Tools[0]["project_connection_id"])
}
