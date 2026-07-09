// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
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

	err := runSkillAddWith(t.Context(), client, "tb", "new-skill@3", skillAddFlags{}, toolboxFlags{output: "json"})
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

	assert.Empty(t, client.setDefaultCalls, "mutation verbs no longer auto-promote default")
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

	err := runSkillAddWith(t.Context(), client, "tb", "first-skill", skillAddFlags{}, toolboxFlags{output: "json"})
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

	err := runSkillAddWith(t.Context(), client, "tb", "dup@2", skillAddFlags{}, toolboxFlags{output: "json"})
	requireLocalError(t, err, exterrors.CodeSkillAlreadyAttached)
	assert.Empty(t, client.createVersionCalls, "no version should be created when validation fails")
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

	err := runSkillAddWith(t.Context(), client, "tb", "BadName@", skillAddFlags{}, toolboxFlags{output: "json"})
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

	err := runSkillRemoveWith(t.Context(), client, "tb", []string{"drop"},
		skillRemoveFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	skills := client.createVersionCalls[0].req.Skills
	require.Len(t, skills, 1)
	assert.Equal(t, "keep", skills[0]["name"])
	assert.Empty(t, client.setDefaultCalls)
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

	err := runSkillRemoveWith(t.Context(), client, "tb", []string{"only"},
		skillRemoveFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Empty(t, client.createVersionCalls[0].req.Skills, "removing the last skill is allowed")
}

// Regression: skillName with surrounding whitespace must match the stored
// canonical entry rather than producing a misleading "not in toolbox" error.
func TestRunSkillRemoveWith_TrimsSkillName(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{{"type": "mcp", "name": "a", "project_connection_id": "/c/a"}},
		Skills: []map[string]any{
			{"type": "skill_reference", "name": "beta"},
		},
	}}

	err := runSkillRemoveWith(t.Context(), client, "tb", []string{"  beta  "},
		skillRemoveFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Empty(t, client.createVersionCalls[0].req.Skills)
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

	err := runSkillRemoveWith(t.Context(), client, "tb", []string{"missing"},
		skillRemoveFlags{force: true}, toolboxFlags{output: "json"},
	)
	requireLocalError(t, err, exterrors.CodeSkillNotInToolbox)
}

// Regression test for azure-dev#9034: sequential `skill remove` calls (with
// no --from-version) must chain onto each other by branching from the latest
// version, not both forking from the stale default version.
func TestRunSkillRemoveWith_SequentialRemovesChainFromLatest(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{{"type": "mcp", "name": "a", "project_connection_id": "/c/a"}},
		Skills: []map[string]any{
			{"type": "skill_reference", "name": "alpha"},
			{"type": "skill_reference", "name": "beta"},
			{"type": "skill_reference", "name": "gamma"},
		},
	}}
	client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
		{Name: "tb", Version: "1"},
	}

	// Step 1: remove "alpha".
	err := runSkillRemoveWith(t.Context(), client, "tb", []string{"alpha"},
		skillRemoveFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	firstReq := client.createVersionCalls[0].req
	require.Len(t, firstReq.Skills, 2, "beta and gamma remain after removing alpha")

	// Simulate the real service state after step 1.
	client.versionResults["tb/2"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "2", Tools: firstReq.Tools, Skills: firstReq.Skills,
	}}
	client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
		{Name: "tb", Version: "1"},
		{Name: "tb", Version: "2"},
	}

	// Step 2: remove "beta" with no --from-version. Pre-fix, this would
	// branch from the still-default version "1" (which still has alpha,
	// beta, gamma) and produce a sibling that never reflects step 1's
	// removal. Post-fix, it must branch from latest ("2": beta, gamma) and
	// end up with only "gamma".
	err = runSkillRemoveWith(t.Context(), client, "tb", []string{"beta"},
		skillRemoveFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 2)
	secondReq := client.createVersionCalls[1].req
	require.Len(t, secondReq.Skills, 1,
		"second remove must branch from latest (post-step-1 state), not the stale default")
	assert.Equal(t, "gamma", secondReq.Skills[0]["name"])
}

func TestRunSkillRemove_NoPromptWithoutForce(t *testing.T) {
	err := runSkillRemove(
		t.Context(), "tb", []string{"any-skill"},
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

// Batch removal via variadic positionals.
func TestRunSkillRemoveWith_VariadicPositionals(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{{"type": "mcp", "name": "a", "project_connection_id": "/c/a"}},
		Skills: []map[string]any{
			{"type": "skill_reference", "name": "alpha"},
			{"type": "skill_reference", "name": "beta"},
			{"type": "skill_reference", "name": "gamma"},
		},
	}}

	err := runSkillRemoveWith(t.Context(), client, "tb", []string{"alpha", "gamma"},
		skillRemoveFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1, "one new version created for the whole batch")
	require.Len(t, client.createVersionCalls[0].req.Skills, 1)
	assert.Equal(t, "beta", client.createVersionCalls[0].req.Skills[0]["name"])
}

// Regression test for azure-dev#9034: sequential `skill add` calls (with no
// --from-version) must chain onto each other by branching from the latest
// version, not both forking from the stale default version.
func TestRunSkillAddWith_SequentialAddsChainFromLatest(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1", Description: "base",
		Tools: []map[string]any{{"type": "mcp", "name": "a", "project_connection_id": "/c/a"}},
	}}
	client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
		{Name: "tb", Version: "1"},
	}

	// Step 1: attach "greeting". Only version "1" exists so this branches
	// from it either way; the mock synthesizes the created version.
	err := runSkillAddWith(t.Context(), client, "tb", "greeting", skillAddFlags{}, toolboxFlags{output: "json"})
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	firstReq := client.createVersionCalls[0].req
	require.Len(t, firstReq.Skills, 1)
	assert.Equal(t, "greeting", firstReq.Skills[0]["name"])

	// Simulate what the real service does after step 1: the new version "2"
	// now exists with the content just sent, the default is still "1" (no
	// auto-promotion — see toolbox_publish.go), and versions-list reports both.
	client.versionResults["tb/2"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "2", Description: firstReq.Description,
		Tools: firstReq.Tools, Skills: firstReq.Skills,
	}}
	client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
		{Name: "tb", Version: "1"},
		{Name: "tb", Version: "2"},
	}

	// Step 2: attach "code-review" with no --from-version. Pre-fix, this
	// would branch from the still-default version "1" and silently drop
	// "greeting" (the exact #9034 repro). Post-fix, it must branch from the
	// latest version "2" and carry "greeting" forward.
	err = runSkillAddWith(t.Context(), client, "tb", "code-review", skillAddFlags{}, toolboxFlags{output: "json"})
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 2)
	secondReq := client.createVersionCalls[1].req
	names := make([]string, 0, len(secondReq.Skills))
	for _, s := range secondReq.Skills {
		names = append(names, s["name"].(string))
	}
	assert.ElementsMatch(t, []string{"greeting", "code-review"}, names,
		"second add must carry the first add's skill forward (branch from latest, not default)")
}

// --from-version default opts back into the pre-#9034-fix behavior: branch
// from the toolbox's current default rather than the latest version.
func TestRunSkillAddWith_FromVersionDefaultOptsOutOfLatest(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Skills: []map[string]any{{"type": "skill_reference", "name": "in-default"}},
	}}
	client.versionResults["tb/2"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "2",
		Skills: []map[string]any{{"type": "skill_reference", "name": "in-latest"}},
	}}
	client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
		{Name: "tb", Version: "1"},
		{Name: "tb", Version: "2"},
	}

	err := runSkillAddWith(
		t.Context(), client, "tb", "new-skill",
		skillAddFlags{fromVersion: "default"}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	names := make([]string, 0, 2)
	for _, s := range client.createVersionCalls[0].req.Skills {
		names = append(names, s["name"].(string))
	}
	assert.ElementsMatch(t, []string{"in-default", "new-skill"}, names,
		"--from-version default must branch from the default version, not the latest")
}

// Batch attachment via --from-file.
func TestRunSkillAddWith_FromFile(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{{"type": "mcp", "name": "a"}},
	}}

	inputPath := t.TempDir() + "/skills.yaml"
	require.NoError(t, os.WriteFile(inputPath, []byte(`
skills:
  - name: alpha
  - name: beta
    version: "2"
`), 0o600))

	err := runSkillAddWith(t.Context(), client, "tb", "",
		skillAddFlags{fromFile: inputPath},
		toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	require.Len(t, client.createVersionCalls[0].req.Skills, 2)
	names := []string{
		client.createVersionCalls[0].req.Skills[0]["name"].(string),
		client.createVersionCalls[0].req.Skills[1]["name"].(string),
	}
	assert.ElementsMatch(t, []string{"alpha", "beta"}, names)
}
