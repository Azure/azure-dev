// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"

	"github.com/stretchr/testify/require"
)

const validSkillMD = `---
name: agentdevcompute
description: Helps with dev compute tasks.
metadata:
  version: 1.2.0
---
# Body
Some skill instructions.
`

func writeSkillsDir(t *testing.T, skills map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range skills {
		bundle := filepath.Join(dir, "skills", name)
		require.NoError(t, os.MkdirAll(bundle, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(bundle, "SKILL.md"), []byte(text), 0o600))
	}
	return dir
}

func TestParseSkillMD(t *testing.T) {
	for _, tt := range []struct{ name, text, version, wantError string }{
		{"valid", validSkillMD, "1.2.0", ""},
		{"optional version", "---\nname: s\ndescription: d\n---\nbody", "", ""},
		{"missing description", "---\nname: s\n---\nbody", "", "description"},
		{"no frontmatter", "# Body", "", "frontmatter"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeSkillsDir(t, map[string]string{"s": tt.text})
			meta, err := parseSkillMD(filepath.Join(dir, "skills", "s", "SKILL.md"))
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.version, meta.Version)
			require.NotEmpty(t, meta.Instructions)
		})
	}
}

func TestScanSkillsDir(t *testing.T) {
	dir := writeSkillsDir(t, map[string]string{
		"bravo": strings.Replace(validSkillMD, "agentdevcompute", "bravo", 1),
		"alpha": strings.Replace(validSkillMD, "agentdevcompute", "alpha", 1),
	})
	bundles, err := scanSkillsDir(dir)
	require.NoError(t, err)
	require.Len(t, bundles, 2)
	require.Equal(t, "alpha", bundles[0].Dir)
	require.Equal(t, "bravo", bundles[1].Dir)
	bundles, err = scanSkillsDir(t.TempDir())
	require.NoError(t, err)
	require.Nil(t, bundles)
}

func TestPromptSkillsNodes(t *testing.T) {
	for _, harness := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "harness"}[harness], func(t *testing.T) {
			managed := &agent_yaml.PromptAgent{}
			graph := &promptGraph{managed: managed, env: map[string]string{
				envkey.SkillVersion("skill-a"): "7", envkey.SkillVersion("skill-b"): "3",
			}}
			skills := []skillBundle{
				{Dir: "skill-a", Meta: skillMeta{Name: "skill-a", Instructions: "body"}},
				{Dir: "skill-b", Meta: skillMeta{Name: "skill-b", Instructions: "body"}},
			}
			var node *promptNode
			if harness {
				node = skillsHarnessNode(graph, skills)
			} else {
				node = skillsShellNode(graph, skills)
			}
			require.NoError(t, node.Validate())
			require.NoError(t, node.Resolve(t.Context()))
			require.NoError(t, node.Resolve(t.Context()))
			require.Equal(t, []agent_yaml.HarnessSkillRef{{Name: "skill-a", Version: "7"}, {Name: "skill-b", Version: "3"}},
				managed.ResolvedSkills)
			require.Empty(t, managed.Tools, "skills must not synthesize toolbox tools")
			delete(graph.env, envkey.SkillVersion("skill-b"))
			managed.ResolvedSkills = nil
			require.ErrorContains(t, node.Resolve(t.Context()), "SKILL_SKILL_B_VERSION")
			require.Empty(t, managed.ResolvedSkills)
			skills[0].Meta.Instructions = ""
			require.ErrorContains(t, node.Validate(), "instructions")
		})
	}
	graph := &promptGraph{managed: &agent_yaml.PromptAgent{}}
	require.Nil(t, skillsShellNode(graph, nil))
	require.Nil(t, skillsHarnessNode(graph, nil))
}

func TestResolveSkillMarkersScopeAndServiceName(t *testing.T) {
	skills := []skillBundle{{Dir: "code-review", Meta: skillMeta{Name: "code review"}}}
	env := map[string]string{envkey.SkillVersion("codereview"): "7"}
	resolved, err := resolveSkillMarkers(skills, "https://acct/api/projects/a", env)
	require.NoError(t, err)
	require.Equal(t, []resolvedSkill{{Name: "codereview", Version: "7"}}, resolved)
	env[envkey.SkillProjectEndpoint("codereview")] = "https://acct/api/projects/b"
	_, err = resolveSkillMarkers(skills, "https://acct/api/projects/a", env)
	require.ErrorContains(t, err, "different Foundry project")
}
