// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindSkillEntry(t *testing.T) {
	skills := []map[string]any{
		{"type": "skill_reference", "name": "alpha"},
		{"type": "skill_reference", "name": "beta", "version": "2"},
		{"type": "skill_reference", "name": "gamma"},
	}
	assert.Equal(t, 0, findSkillEntry(skills, "alpha"))
	assert.Equal(t, 1, findSkillEntry(skills, "beta"))
	assert.Equal(t, 2, findSkillEntry(skills, "gamma"))
	assert.Equal(t, -1, findSkillEntry(skills, "delta"))
	assert.Equal(t, -1, findSkillEntry(nil, "any"))
}

func TestFilterOutSkill(t *testing.T) {
	skills := []map[string]any{
		{"type": "skill_reference", "name": "alpha"},
		{"type": "skill_reference", "name": "beta", "version": "2"},
		{"type": "skill_reference", "name": "gamma"},
	}

	got, removed := filterOutSkill(skills, "beta")
	require.True(t, removed)
	require.Len(t, got, 2)
	assert.Equal(t, "alpha", got[0]["name"])
	assert.Equal(t, "gamma", got[1]["name"])

	got2, removed2 := filterOutSkill(skills, "missing")
	assert.False(t, removed2)
	assert.Len(t, got2, 3, "unmodified slice returned when name not found")

	// Removing the only entry returns an empty (not nil) slice — exercises the
	// "removing last skill is OK" semantic.
	single := []map[string]any{{"type": "skill_reference", "name": "only"}}
	got3, removed3 := filterOutSkill(single, "only")
	assert.True(t, removed3)
	assert.Empty(t, got3)
}

func TestRunSkillAddWith_AppendsAndCarriesForward(t *testing.T) {
	existingTools := []map[string]any{
		{"type": "mcp", "name": "a", "project_connection_id": "/c/a"},
	}
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1", Description: "first",
		Tools: existingTools,
		Skills: []map[string]any{
			{"type": "skill_reference", "name": "already-there"},
		},
	}}

	err := runSkillAddWith(t.Context(), client, "tb", "new-skill@3", toolboxFlags{output: "json"})
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)

	req := client.createVersionCalls[0].req
	assert.Equal(t, "first", req.Description, "description carried forward")
	assert.Equal(t, existingTools, req.Tools, "tools carried forward verbatim")

	require.Len(t, req.Skills, 2, "existing skill + new skill")
	assert.Equal(t, "already-there", req.Skills[0]["name"])
	assert.Equal(t, "new-skill", req.Skills[1]["name"])
	assert.Equal(t, "3", req.Skills[1]["version"])
	assert.Equal(t, "skill_reference", req.Skills[1]["type"])

	require.Len(t, client.setDefaultCalls, 1, "new version must be promoted to default")
}

func TestRunSkillAddWith_NoExistingSkills(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{
			{"type": "mcp", "name": "a", "project_connection_id": "/c/a"},
		},
		// Skills nil — exercises the "first skill on a toolbox without any" path.
	}}

	err := runSkillAddWith(t.Context(), client, "tb", "first-skill", toolboxFlags{output: "json"})
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	require.Len(t, client.createVersionCalls[0].req.Skills, 1)
	assert.Equal(t, "first-skill", client.createVersionCalls[0].req.Skills[0]["name"])
	_, hasVersion := client.createVersionCalls[0].req.Skills[0]["version"]
	assert.False(t, hasVersion, "version key must be omitted when @<version> is not provided")
}

func TestRunSkillAddWith_AlreadyAttached(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{{"type": "mcp", "name": "a", "project_connection_id": "/c/a"}},
		Skills: []map[string]any{
			{"type": "skill_reference", "name": "dup"},
		},
	}}

	err := runSkillAddWith(t.Context(), client, "tb", "dup@2", toolboxFlags{output: "json"})
	requireLocalError(t, err, exterrors.CodeSkillAlreadyAttached)
	assert.Empty(t, client.createVersionCalls, "no version should be published when validation fails")
}

func TestRunSkillAddWith_InvalidSpec(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{{"type": "mcp", "name": "a"}},
	}}

	err := runSkillAddWith(t.Context(), client, "tb", "BadName@", toolboxFlags{output: "json"})
	requireLocalError(t, err, exterrors.CodeInvalidSkillSpec)
}

func TestRunSkillRemoveWith_FilteredAndPromoted(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{{"type": "mcp", "name": "a", "project_connection_id": "/c/a"}},
		Skills: []map[string]any{
			{"type": "skill_reference", "name": "keep"},
			{"type": "skill_reference", "name": "drop"},
		},
	}}

	err := runSkillRemoveWith(
		t.Context(), client, "tb", "drop",
		skillRemoveFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	skills := client.createVersionCalls[0].req.Skills
	require.Len(t, skills, 1)
	assert.Equal(t, "keep", skills[0]["name"])
	require.Len(t, client.setDefaultCalls, 1)
}

// Removing the only skill is allowed (no last-skill block).
func TestRunSkillRemoveWith_LastSkillAllowed(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{{"type": "mcp", "name": "a", "project_connection_id": "/c/a"}},
		Skills: []map[string]any{
			{"type": "skill_reference", "name": "only"},
		},
	}}

	err := runSkillRemoveWith(
		t.Context(), client, "tb", "only",
		skillRemoveFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Empty(t, client.createVersionCalls[0].req.Skills, "removing the last skill is allowed")
}

func TestRunSkillRemoveWith_NotAttached(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{{"type": "mcp", "name": "a"}},
		Skills: []map[string]any{
			{"type": "skill_reference", "name": "other"},
		},
	}}

	err := runSkillRemoveWith(
		t.Context(), client, "tb", "missing",
		skillRemoveFlags{force: true}, toolboxFlags{output: "json"},
	)
	requireLocalError(t, err, exterrors.CodeSkillNotInToolbox)
}

func TestRunSkillRemove_NoPromptWithoutForce(t *testing.T) {
	err := runSkillRemove(
		t.Context(), "tb", "any-skill",
		skillRemoveFlags{force: false},
		toolboxFlags{output: "table", noPrompt: true},
	)
	requireLocalError(t, err, exterrors.CodeMissingForceFlag)
}

func TestRunSkillListWith_EmitsAllShapes(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{},
		Skills: []map[string]any{
			{"type": "skill_reference", "name": "alpha", "version": "2"},
			{"type": "skill_reference", "name": "beta"},
		},
	}}

	rows := extractSkillRows(client.versionResults["tb/1"].obj.Skills)
	require.Len(t, rows, 2)
	assert.Equal(t, "alpha", rows[0]["name"])
	assert.Equal(t, "2", rows[0]["version"])
	assert.Equal(t, "skill_reference", rows[0]["type"])
	assert.Equal(t, "beta", rows[1]["name"])
	assert.Empty(t, rows[1]["version"], "empty version means 'use the skill's default'")

	err := runSkillListWith(t.Context(), client, "tb", toolboxFlags{output: "json"})
	require.NoError(t, err)
}

// extractSkillRows must skip malformed entries (defensive against unexpected
// service responses).
func TestExtractSkillRows_SkipsMalformedEntries(t *testing.T) {
	skills := []map[string]any{
		{"type": "skill_reference"},               // missing name
		{"type": "skill_reference", "name": ""},   // empty name
		{"type": "skill_reference", "name": "ok"}, // valid
		{"type": "skill_reference", "name": 42},   // wrong type for name
	}
	rows := extractSkillRows(skills)
	require.Len(t, rows, 1)
	assert.Equal(t, "ok", rows[0]["name"])
}
