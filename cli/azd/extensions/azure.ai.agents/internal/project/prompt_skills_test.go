// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

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

func TestPromptSkillsNodes(t *testing.T) {
	for _, harness := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "harness"}[harness], func(t *testing.T) {
			managed := &agent_yaml.PromptAgent{}
			graph := &promptGraph{managed: managed, env: map[string]string{
				envkey.SkillVersion("skill-a"): "7", envkey.SkillVersion("skill-b"): "3",
			}}
			skills := []string{"skill-a", "skill-b"}
			var node *promptNode
			if harness {
				node = skillsHarnessNode(graph, skills)
			} else {
				node = skillsShellNode(graph, skills)
			}
			require.Nil(t, node.Validate)
			require.NoError(t, node.Resolve(t.Context()))
			require.NoError(t, node.Resolve(t.Context()))
			require.Equal(
				t,
				[]agent_yaml.HarnessSkillRef{{Name: "skill-a", Version: "7"}, {Name: "skill-b", Version: "3"}},
				managed.ResolvedSkills,
			)
			require.Empty(t, managed.Tools, "skills must not synthesize toolbox tools")
			delete(graph.env, envkey.SkillVersion("skill-b"))
			managed.ResolvedSkills = nil
			err := node.Resolve(t.Context())
			require.ErrorContains(t, err, "SKILL_SKILL_B_VERSION")
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Contains(t, localErr.Suggestion, `run 'azd deploy "skill-b"' or 'azd deploy --all'`)
			require.Contains(t, localErr.Suggestion, "azd ai skill add skill-b")
			require.Empty(t, managed.ResolvedSkills)
		})
	}
	graph := &promptGraph{managed: &agent_yaml.PromptAgent{}}
	require.Nil(t, skillsShellNode(graph, nil))
	require.Nil(t, skillsHarnessNode(graph, nil))
}

func TestResolveSkillMarkersScopeAndServiceName(t *testing.T) {
	skills := []string{"code review"}
	env := map[string]string{envkey.SkillVersion("codereview"): "7"}
	resolved, err := resolveSkillMarkers(skills, "https://acct/api/projects/a", env)
	require.NoError(t, err)
	require.Equal(t, []resolvedSkill{{AuthoredName: "code review", Name: "codereview", Version: "7"}}, resolved)
	env[envkey.SkillProjectEndpoint("codereview")] = "https://acct/api/projects/b"
	_, err = resolveSkillMarkers(skills, "https://acct/api/projects/a", env)
	require.ErrorContains(t, err, "different Foundry project")
}

func TestNewPromptGraph_ExplicitSkillsOnly(t *testing.T) {
	t.Run("local skills dir on disk is completely ignored", func(t *testing.T) {
		dir := writeSkillsDir(t, map[string]string{
			"valid-skill":  "---\nname: valid-skill\ndescription: Helps.\n---\nBody\n",
			"broken-skill": "---\nname: broken-skill\ndescription: [invalid]\n---\n",
		})
		targetDir := t.TempDir()
		createSymlinkOrSkip(t, targetDir, filepath.Join(dir, "skills", "symlink-skill"))

		managed := &agent_yaml.PromptAgent{
			AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindPrompt, Name: "my-agent"},
			Model:           "gpt-4.1-mini",
			Instructions:    "Be helpful.",
		}
		g, err := newPromptGraph(dir, managed, nil, map[string]string{}, nil)
		require.NoError(t, err)
		for _, n := range g.nodes {
			require.NotEqual(t, nodeSkill, n.Kind, "local skills/ folder must not produce a skill node")
		}
		require.NoError(t, g.resolve(t.Context(), nil))
		require.Empty(t, managed.ResolvedSkills)
	})

	t.Run("declared versionless skill resolves from env and validates dependency", func(t *testing.T) {
		for _, harness := range []string{"", agent_api.ManagedAgentHarnessMicrosoftCopilot} {
			t.Run("harness="+harness, func(t *testing.T) {
				// Even if a broken local skills/<name>/SKILL.md exists on disk,
				// azure.ai.agents does not parse SKILL.md and only resolves
				// the sibling azure.ai.skill service.
				dir := writeSkillsDir(t, map[string]string{
					"codereview": "---\nname: codereview\ndescription: [invalid]\n---\n",
				})
				managed := &agent_yaml.PromptAgent{
					AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindPrompt, Name: "my-agent"},
					Model:           "gpt-4.1-mini",
					Instructions:    "Be helpful.",
					Harness:         agent_yaml.NewPromptHarness(harness),
					Skills:          []agent_yaml.HarnessSkillRef{{Name: "code review"}},
				}
				env := map[string]string{
					"FOUNDRY_PROJECT_ENDPOINT":        "https://acct.services.ai.azure.com/api/projects/proj",
					envkey.SkillVersion("codereview"): "4",
				}
				g, err := newPromptGraph(dir, managed, nil, env, nil)
				require.NoError(t, err)
				g.agentService = &azdext.ServiceConfig{
					Name: "my-agent",
					Host: foundryAgentHost,
					Uses: []string{"codereview"},
				}
				g.projectServices = map[string]*azdext.ServiceConfig{
					"codereview": {Name: "codereview", Host: foundrySkillHost},
				}
				require.NoError(t, g.resolve(t.Context(), nil))
				require.Equal(t, []agent_yaml.HarnessSkillRef{{Name: "codereview", Version: "4"}}, managed.ResolvedSkills)

				req, err := agent_yaml.CreatePromptAgentAPIRequest(*managed, nil)
				require.NoError(t, err)
				def, ok := req.Definition.(agent_api.ManagedAgentDefinition)
				require.True(t, ok)
				require.Equal(t, []agent_api.SkillReference{{
					Type:    "skill_reference",
					Name:    "codereview",
					Version: "4",
				}}, def.Skills)

				// Missing uses edge fails dependency validation.
				g.agentService.Uses = nil
				require.ErrorContains(
					t,
					g.resolve(t.Context(), nil),
					`skill service "codereview" is not declared in agent service "my-agent" uses`,
				)

				// Missing sibling service fails dependency validation.
				g.agentService.Uses = []string{"codereview"}
				g.projectServices = map[string]*azdext.ServiceConfig{}
				err = g.resolve(t.Context(), nil)
				require.ErrorContains(
					t,
					err,
					`skill "codereview" has no enabled azure.ai.skill sibling service`,
				)
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Contains(t, localErr.Suggestion, "azd ai skill add codereview")
				require.Contains(t, localErr.Suggestion, "uses:")
				require.Contains(t, localErr.Suggestion, "'azd deploy --all'")
				require.Contains(t, localErr.Suggestion, "replace")
				require.Contains(t, localErr.Suggestion, `{name: "codereview", version: "<version>"}`)

				// Occupied service key with a different host fails with conflict guidance.
				g.projectServices = map[string]*azdext.ServiceConfig{
					"codereview": {Name: "codereview", Host: foundryToolboxHost},
				}
				err = g.resolve(t.Context(), nil)
				require.ErrorContains(
					t,
					err,
					`service "codereview" uses host "azure.ai.toolbox" instead of azure.ai.skill`,
				)
				localErr, ok = errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Contains(t, localErr.Suggestion, `rename or remove the conflicting service "codereview"`)
				require.Contains(t, localErr.Suggestion, "update skills: and uses:")
				require.Contains(t, localErr.Suggestion, "'azd ai skill add ...'")
				require.Contains(t, localErr.Suggestion, "'azd deploy --all'")
				require.Contains(t, localErr.Suggestion, "replace")
				require.Contains(t, localErr.Suggestion, `{name: "codereview", version: "<version>"}`)
			})
		}
	})

	t.Run("declared version-pinned skill does not create skill node or require marker", func(t *testing.T) {
		dir := writeSkillsDir(t, map[string]string{
			"skill-a": "---\nname: skill-a\n---\n",
		})
		managed := &agent_yaml.PromptAgent{
			AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindPrompt, Name: "my-agent"},
			Model:           "gpt-4.1-mini",
			Instructions:    "Be helpful.",
			Skills:          []agent_yaml.HarnessSkillRef{{Name: "skill-a", Version: "2"}},
		}
		g, err := newPromptGraph(dir, managed, nil, map[string]string{}, nil)
		require.NoError(t, err)
		for _, n := range g.nodes {
			require.NotEqual(t, nodeSkill, n.Kind, "version-pinned skill must not produce a skill node")
		}
		require.NoError(t, g.resolve(t.Context(), nil))
		require.Empty(t, managed.ResolvedSkills)
	})
}

func TestPromptGraph_WarnsOnUnreferencedSkillUses(t *testing.T) {
	t.Parallel()

	collectWarnings := func(messages []string) []string {
		var warnings []string
		for _, m := range messages {
			if strings.HasPrefix(m, "Warning:") {
				warnings = append(warnings, m)
			}
		}
		return warnings
	}

	t.Run("single enabled unreferenced skill sibling emits warning", func(t *testing.T) {
		t.Parallel()

		managed := &agent_yaml.PromptAgent{
			AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindPrompt, Name: "my-agent"},
			Model:           "gpt-4.1-mini",
			Instructions:    "Be helpful.",
		}
		g, err := newPromptGraph(t.TempDir(), managed, nil, map[string]string{}, nil)
		require.NoError(t, err)
		g.agentService = &azdext.ServiceConfig{
			Name: "my-agent",
			Host: foundryAgentHost,
			Uses: []string{"codereview"},
		}
		g.projectServices = map[string]*azdext.ServiceConfig{
			"codereview": {Name: "codereview", Host: foundrySkillHost},
		}

		var messages []string
		require.NoError(t, g.resolve(t.Context(), func(m string) { messages = append(messages, m) }))
		require.Empty(t, managed.ResolvedSkills)

		warnings := collectWarnings(messages)
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0], `agent service "my-agent"`)
		require.Contains(t, warnings[0], `azure.ai.skill sibling service "codereview"`)
		require.Contains(
			t,
			warnings[0],
			"Add it to the skills: list in the prompt agent definition (inline or in its referenced file), "+
				"or remove it from uses: if unneeded.",
		)
	})

	t.Run("multiple unreferenced skills pluralize, deduplicate, and ignore non-skill siblings", func(t *testing.T) {
		t.Parallel()

		managed := &agent_yaml.PromptAgent{
			AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindPrompt, Name: "my-agent"},
			Model:           "gpt-4.1-mini",
			Instructions:    "Be helpful.",
		}
		g, err := newPromptGraph(t.TempDir(), managed, nil, map[string]string{}, nil)
		require.NoError(t, err)
		g.agentService = &azdext.ServiceConfig{
			Name: "my-agent",
			Host: foundryAgentHost,
			Uses: []string{
				"project",
				"conn",
				"tbx",
				"missing-svc",
				"nil-svc",
				"   ",
				"skill-b",
				"skill-a",
				"skill-b",
				"  skill-a  ",
			},
		}
		g.projectServices = map[string]*azdext.ServiceConfig{
			"project": {Name: "project", Host: foundryProjectHost},
			"conn":    {Name: "conn", Host: foundryConnectionHost},
			"tbx":     {Name: "tbx", Host: foundryToolboxHost},
			"nil-svc": nil,
			"skill-a": {Name: "skill-a", Host: foundrySkillHost},
			"skill-b": {Name: "skill-b", Host: foundrySkillHost},
		}

		var messages []string
		require.NoError(t, g.resolve(t.Context(), func(m string) { messages = append(messages, m) }))

		warnings := collectWarnings(messages)
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0], `azure.ai.skill sibling services "skill-b", "skill-a"`)
		require.Contains(
			t,
			warnings[0],
			"Add them to the skills: list in the prompt agent definition (inline or in its referenced file), "+
				"or remove them from uses: if unneeded.",
		)
		require.Equal(t, 1, strings.Count(warnings[0], `"skill-b"`))
		require.Equal(t, 1, strings.Count(warnings[0], `"skill-a"`))
		for _, ignored := range []string{"project", "conn", "tbx", "missing-svc", "nil-svc"} {
			require.NotContains(t, warnings[0], ignored)
		}
	})

	t.Run("referenced versionless unsanitized and version-pinned skills do not warn", func(t *testing.T) {
		t.Parallel()

		managed := &agent_yaml.PromptAgent{
			AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindPrompt, Name: "my-agent"},
			Model:           "gpt-4.1-mini",
			Instructions:    "Be helpful.",
			Skills: []agent_yaml.HarnessSkillRef{
				{Name: "code review"},
				{Name: "skill-a", Version: "2"},
			},
		}
		env := map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT":        "https://acct.services.ai.azure.com/api/projects/proj",
			envkey.SkillVersion("codereview"): "4",
		}
		g, err := newPromptGraph(t.TempDir(), managed, nil, env, nil)
		require.NoError(t, err)
		g.agentService = &azdext.ServiceConfig{
			Name: "my-agent",
			Host: foundryAgentHost,
			Uses: []string{"codereview", "skill-a"},
		}
		g.projectServices = map[string]*azdext.ServiceConfig{
			"codereview": {Name: "codereview", Host: foundrySkillHost},
			"skill-a":    {Name: "skill-a", Host: foundrySkillHost},
		}

		var messages []string
		require.NoError(t, g.resolve(t.Context(), func(m string) { messages = append(messages, m) }))
		require.Empty(t, collectWarnings(messages))
		require.Equal(t, []agent_yaml.HarnessSkillRef{{Name: "codereview", Version: "4"}}, managed.ResolvedSkills)
	})

	t.Run("disabled or condition-error skill siblings do not warn or fail resolve", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name        string
			uses        []string
			enabledFunc dependencyEnabled
			wantWarning string
		}{
			{
				name: "disabled skill sibling",
				uses: []string{"skill-disabled"},
				enabledFunc: func(context.Context, string) (bool, error) {
					return false, nil
				},
			},
			{
				name: "condition lookup error on undeclared skill",
				uses: []string{"skill-err"},
				enabledFunc: func(context.Context, string) (bool, error) {
					return false, errors.New("condition lookup failed")
				},
			},
			{
				name: "mixed enabled, disabled, and erroring siblings warns only for enabled",
				uses: []string{"skill-disabled", "skill-err", "skill-enabled"},
				enabledFunc: func(_ context.Context, svc string) (bool, error) {
					switch svc {
					case "skill-enabled":
						return true, nil
					case "skill-err":
						return true, errors.New("condition lookup failed")
					default:
						return false, nil
					}
				},
				wantWarning: `azure.ai.skill sibling service "skill-enabled"`,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				managed := &agent_yaml.PromptAgent{
					AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindPrompt, Name: "my-agent"},
					Model:           "gpt-4.1-mini",
					Instructions:    "Be helpful.",
				}
				g, err := newPromptGraph(t.TempDir(), managed, nil, map[string]string{}, nil)
				require.NoError(t, err)
				g.agentService = &azdext.ServiceConfig{
					Name: "my-agent",
					Host: foundryAgentHost,
					Uses: tc.uses,
				}
				g.projectServices = map[string]*azdext.ServiceConfig{
					"skill-disabled": {Name: "skill-disabled", Host: foundrySkillHost},
					"skill-err":      {Name: "skill-err", Host: foundrySkillHost},
					"skill-enabled":  {Name: "skill-enabled", Host: foundrySkillHost},
				}
				g.dependencyEnabled = tc.enabledFunc

				var messages []string
				require.NoError(t, g.resolve(t.Context(), func(m string) { messages = append(messages, m) }))

				warnings := collectWarnings(messages)
				if tc.wantWarning == "" {
					require.Empty(t, warnings)
					return
				}
				require.Len(t, warnings, 1)
				require.Contains(t, warnings[0], tc.wantWarning)
				require.NotContains(t, warnings[0], "skill-disabled")
				require.NotContains(t, warnings[0], "skill-err")
			})
		}
	})

	t.Run("nil progress reporter skips warning and dependencyEnabled", func(t *testing.T) {
		t.Parallel()

		managed := &agent_yaml.PromptAgent{
			AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindPrompt, Name: "my-agent"},
			Model:           "gpt-4.1-mini",
			Instructions:    "Be helpful.",
		}
		g, err := newPromptGraph(t.TempDir(), managed, nil, map[string]string{}, nil)
		require.NoError(t, err)
		g.agentService = &azdext.ServiceConfig{
			Name: "my-agent",
			Host: foundryAgentHost,
			Uses: []string{"codereview"},
		}
		g.projectServices = map[string]*azdext.ServiceConfig{
			"codereview": {Name: "codereview", Host: foundrySkillHost},
		}
		called := false
		g.dependencyEnabled = func(context.Context, string) (bool, error) {
			called = true
			return true, nil
		}

		require.NoError(t, g.resolve(t.Context(), nil))
		require.False(t, called, "dependencyEnabled must not be called when progress is nil")
	})

	t.Run("nil receivers and partial graphs are safe", func(t *testing.T) {
		t.Parallel()

		called := false
		trackEnabled := func(context.Context, string) (bool, error) {
			called = true
			return true, nil
		}
		noopWarn := func(string) {}

		require.NotPanics(t, func() {
			warnUnreferencedSkillUses(t.Context(), nil)
			warnUnreferencedSkillUses(t.Context(), &promptGraph{warn: noopWarn, dependencyEnabled: trackEnabled})
			warnUnreferencedSkillUses(t.Context(), &promptGraph{
				warn:              noopWarn,
				managed:           &agent_yaml.PromptAgent{},
				dependencyEnabled: trackEnabled,
			})
			warnUnreferencedSkillUses(t.Context(), &promptGraph{
				warn:              noopWarn,
				managed:           &agent_yaml.PromptAgent{},
				agentService:      &azdext.ServiceConfig{Uses: []string{"s"}},
				dependencyEnabled: trackEnabled,
			})
		})
		require.False(t, called)
	})
}
