// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/connections"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tbWithVersions seeds a mock toolbox whose default is "1" but whose latest
// version is "2" (v2 = v1 + a "greeting" skill), reproducing the #8674 setup
// where default != latest.
func tbWithDefaultOneLatestTwo(client *mockToolboxClient) {
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
		{Name: "tb", Version: "1"},
		{Name: "tb", Version: "2"},
	}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools:  []map[string]any{{"type": "mcp", "name": "a", "project_connection_id": "/c/a"}},
		Skills: nil,
	}}
	client.versionResults["tb/2"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "2",
		Tools:  []map[string]any{{"type": "mcp", "name": "a", "project_connection_id": "/c/a"}},
		Skills: []map[string]any{{"type": "skill_reference", "name": "greeting"}},
	}}
}

// Regression for #8674: `skill add` must branch from the LATEST version (v2),
// so the new version accumulates greeting + code-review instead of silently
// dropping greeting by forking from the default (v1).
func TestRunSkillAddWith_BranchesFromLatestNotDefault(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	tbWithDefaultOneLatestTwo(client)

	err := runSkillAddWith(t.Context(), client, "tb", "code-review", skillAddFlags{}, toolboxFlags{output: "json"})
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)

	req := client.createVersionCalls[0].req
	names := skillNames(req.Skills)
	assert.Contains(t, names, "greeting", "skills from latest version must be carried forward")
	assert.Contains(t, names, "code-review", "new skill must be attached")
	assert.Len(t, req.Skills, 2)
}

// --from-version pins the branch source to an explicit version (here the default
// v1), preserving the old default-snapshot behavior on demand.
func TestRunSkillAddWith_FromVersionOverridesLatest(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	tbWithDefaultOneLatestTwo(client)

	err := runSkillAddWith(
		t.Context(), client, "tb", "code-review",
		skillAddFlags{fromVersion: "1"}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)

	names := skillNames(client.createVersionCalls[0].req.Skills)
	assert.NotContains(t, names, "greeting", "branching from v1 must not carry v2's skills")
	assert.Contains(t, names, "code-review")
	assert.Len(t, names, 1)
}

// A --from-version that does not exist is rejected with a clear error.
func TestRunSkillAddWith_FromVersionNotFound(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	tbWithDefaultOneLatestTwo(client)

	err := runSkillAddWith(
		t.Context(), client, "tb", "code-review",
		skillAddFlags{fromVersion: "99"}, toolboxFlags{output: "json"},
	)
	requireLocalError(t, err, exterrors.CodeToolboxVersionNotFound)
	assert.Empty(t, client.createVersionCalls, "no version created when --from-version is invalid")
}

// `skill remove` also branches from the latest version.
func TestRunSkillRemoveWith_BranchesFromLatestNotDefault(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	tbWithDefaultOneLatestTwo(client)

	err := runSkillRemoveWith(
		t.Context(), client, "tb", []string{"greeting"},
		skillRemoveFlags{force: true}, toolboxFlags{output: "json", noPrompt: true},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Empty(t, skillNames(client.createVersionCalls[0].req.Skills),
		"removing greeting from latest (v2) leaves no skills")
}

// resolveBranchVersion falls back to the default version when the toolbox
// reports no versions (edge case), preserving today's behavior.
func TestResolveBranchVersion_EmptyListFallsBackToDefault(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	tb := &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}

	got, err := resolveBranchVersion(t.Context(), client, "tb", tb, "")
	require.NoError(t, err)
	assert.Equal(t, "1", got.Branch)
	assert.Equal(t, "1", got.Latest)
	assert.Equal(t, "1", got.Default)
	assert.False(t, got.branchedFromNonDefault())
}

func TestResolveBranchVersion_EmptyListRejectsUnknownFromVersion(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	tb := &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}

	_, err := resolveBranchVersion(t.Context(), client, "tb", tb, "99")
	requireLocalError(t, err, exterrors.CodeToolboxVersionNotFound)
}

// `connection add` also branches from the latest version, carrying forward the
// tools attached to v2 rather than v1 (default).
func TestRunConnectionAddWith_BranchesFromLatestNotDefault(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
		{Name: "tb", Version: "1"}, {Name: "tb", Version: "2"},
	}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{{"type": "mcp", "name": "a", "project_connection_id": "/c/a"}},
	}}
	client.versionResults["tb/2"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "2",
		Tools: []map[string]any{
			{"type": "mcp", "name": "a", "project_connection_id": "/c/a"},
			{"type": "mcp", "name": "b", "project_connection_id": "/c/b"},
		},
	}}
	resolver := newStubConnectionResolver()
	resolver.byName["c"] = &projectConnection{
		ID: "/c/c", Category: connections.ConnectionTypeRemoteTool, Name: "c", Target: "https://mcp-c",
	}

	err := runConnectionAddWith(
		t.Context(), client, resolver, "https://e/",
		"tb", "c", connectionAddFlags{}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)

	ids := []string{}
	forEachToolConnectionID(client.createVersionCalls[0].req.Tools, func(id string) bool {
		ids = append(ids, id)
		return false
	})
	assert.ElementsMatch(t, []string{"/c/a", "/c/b", "/c/c"}, ids,
		"tools from latest version (v2: a,b) must be carried forward plus the new one (c)")
}

func TestRunConnectionRemoveWith_BranchesFromLatestNotDefault(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
		{Name: "tb", Version: "1"}, {Name: "tb", Version: "2"},
	}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools: []map[string]any{{"type": "mcp", "name": "a", "project_connection_id": "/c/a"}},
	}}
	client.versionResults["tb/2"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "2",
		Tools: []map[string]any{
			{"type": "mcp", "name": "a", "project_connection_id": "/c/a"},
			{"type": "mcp", "name": "b", "project_connection_id": "/c/b"},
		},
		Skills: []map[string]any{{"type": "skill_reference", "name": "greeting"}},
	}}
	resolver := newStubConnectionResolver()
	resolver.byName["b"] = &projectConnection{
		ID: "/c/b", Category: connections.ConnectionTypeRemoteTool, Name: "b",
	}

	err := runConnectionRemoveWith(
		t.Context(), client, resolver, "https://e/",
		"tb", []string{"b"}, connectionRemoveFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)

	req := client.createVersionCalls[0].req
	require.Len(t, req.Tools, 1)
	assert.Equal(t, "/c/a", req.Tools[0]["project_connection_id"])
	assert.Equal(t, []string{"greeting"}, skillNames(req.Skills),
		"skills from latest version must be carried forward")
}

// Regression for review feedback (#8674 PR): skill add/remove must carry
// forward the branched-from version's policies (e.g. rai_config), matching
// connection add/remove — otherwise a skill mutation silently drops governance.
func TestRunSkillAddWith_CarriesForwardPolicies(t *testing.T) {
	policies := &azure.ToolboxPolicies{RaiConfig: &azure.RaiConfig{RaiPolicyName: "strict"}}
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools:    []map[string]any{{"type": "mcp", "name": "a", "project_connection_id": "/c/a"}},
		Policies: policies,
	}}

	err := runSkillAddWith(t.Context(), client, "tb", "new-skill", skillAddFlags{}, toolboxFlags{output: "json"})
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Equal(t, policies, client.createVersionCalls[0].req.Policies,
		"policies must be carried forward into the new version")
}

func TestRunSkillRemoveWith_CarriesForwardPolicies(t *testing.T) {
	policies := &azure.ToolboxPolicies{RaiConfig: &azure.RaiConfig{RaiPolicyName: "strict"}}
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}}
	client.versionResults["tb/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "tb", Version: "1",
		Tools:    []map[string]any{{"type": "mcp", "name": "a", "project_connection_id": "/c/a"}},
		Skills:   []map[string]any{{"type": "skill_reference", "name": "greeting"}},
		Policies: policies,
	}}

	err := runSkillRemoveWith(
		t.Context(), client, "tb", []string{"greeting"},
		skillRemoveFlags{force: true}, toolboxFlags{output: "json", noPrompt: true},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Equal(t, policies, client.createVersionCalls[0].req.Policies,
		"policies must be carried forward into the new version")
}

// resolveBranchVersion derives "latest" by recency (CreatedAt) first, so the
// most recently created version wins even when it is not the highest number
// (addresses the review question about max-numeric != true tip).
func TestResolveBranchVersion_LatestByCreatedAt(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	tb := &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}
	// Version "2" is numerically highest but "10" was created most recently.
	client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
		{Name: "tb", Version: "2", CreatedAt: 100},
		{Name: "tb", Version: "10", CreatedAt: 200},
	}

	got, err := resolveBranchVersion(t.Context(), client, "tb", tb, "")
	require.NoError(t, err)
	assert.Equal(t, "10", got.Latest, "most recently created version wins")
	assert.Equal(t, "10", got.Branch)
}

// With CreatedAt unset (tied at 0), the numeric-aware comparator breaks the tie.
func TestResolveBranchVersion_LatestNumericFallback(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	tb := &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}
	client.listVersionsResults["tb"] = []azure.ToolboxVersionObject{
		{Name: "tb", Version: "1"},
		{Name: "tb", Version: "2"},
	}

	got, err := resolveBranchVersion(t.Context(), client, "tb", tb, "")
	require.NoError(t, err)
	assert.Equal(t, "2", got.Latest, "highest version wins when CreatedAt is unset")
}

// skillNames extracts the "name" of each skill entry for assertions.
func skillNames(skills []map[string]any) []string {
	out := make([]string, 0, len(skills))
	for _, s := range skills {
		if n, ok := s["name"].(string); ok {
			out = append(out, n)
		}
	}
	return out
}
