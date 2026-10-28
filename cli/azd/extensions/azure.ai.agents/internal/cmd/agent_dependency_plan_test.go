// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"azureaiagent/internal/exterrors"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestPlanAgentServiceUsesOrdersAndDeduplicates(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "toolbox.json"),
		[]byte(`{"name":"new-tools"}`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "connection.yaml"),
		[]byte("name: ref-name\n"),
		0o600,
	))
	definition := loadAgentDependencyDefinition(t, root, "agent", `kind: prompt
name: new-agent
model: gpt-4.1-mini
instructions: Help the user.
harness:
  type: microsoft_copilot_preview
connections:
  - search-resource
  - search
toolboxes:
  - $ref: ./toolbox.json
  - new-tools
skills:
  - new skill
  - name: pinned-skill
    version: "4"
`)

	rawToolboxes, ok := definition.Properties["toolboxes"].([]any)
	require.True(t, ok)
	require.Len(t, rawToolboxes, 2)
	resolvedToolboxes, ok := definition.ResolvedProperties["toolboxes"].([]any)
	require.True(t, ok)
	require.Equal(t, map[string]any{"name": "new-tools"}, resolvedToolboxes[0])

	oldAgent := agentDependencyService(t, "agent", AiAgentHost, map[string]any{
		"kind":         "prompt",
		"name":         "old-agent",
		"model":        "gpt-4.1-mini",
		"instructions": "Old instructions.",
		"harness":      map[string]any{"type": "microsoft_copilot_preview"},
		"connections":  []any{"old-resource"},
		"toolboxes":    []any{"old-tools"},
		"skills": []any{
			"old skill",
			map[string]any{"name": "pinned-skill", "version": "1"},
		},
	}, "old-connection", "old-tools", "oldskill", "manual-agent")
	services := map[string]*azdext.ServiceConfig{
		"project": agentDependencyService(t, "project", AiProjectHost, nil),
		"agent":   oldAgent,
		"old-connection": agentDependencyService(t, "old-connection",
			AiConnectionHost, map[string]any{"name": "old-resource"}),
		"search": agentDependencyService(t, "search", AiConnectionHost, map[string]any{
			"$ref": "./connection.yaml",
			"name": "search-resource",
		}),
		"old-tools": agentDependencyService(t, "old-tools", AiToolboxHost, nil),
		"new-tools": agentDependencyService(t, "new-tools", AiToolboxHost, nil),
		"oldskill":  agentDependencyService(t, "oldskill", AiSkillHost, nil),
		"newskill":  agentDependencyService(t, "newskill", AiSkillHost, nil),
		"pinned-skill": agentDependencyService(
			t, "pinned-skill", AiSkillHost, nil,
		),
		"manual-connection": agentDependencyService(
			t, "manual-connection", AiConnectionHost, nil,
		),
		"manual-agent": agentDependencyService(t, "manual-agent", AiAgentHost, nil),
	}
	currentUses := []string{
		"old-connection",
		"old-tools",
		"oldskill",
		"pinned-skill",
		"manual-connection",
		"manual-agent",
		"undeclared-use",
		"search",
		"new-tools",
		"newskill",
		"project",
		"manual-agent",
	}
	beforeUses := slices.Clone(currentUses)
	beforeAgentUses := slices.Clone(oldAgent.GetUses())
	beforeAgentProperties := oldAgent.GetAdditionalProperties().AsMap()

	plan, err := planAgentServiceUses(
		"agent",
		definition,
		currentUses,
		services,
		root,
	)

	require.NoError(t, err)
	require.Equal(t, []string{
		"project",
		"search",
		"new-tools",
		"newskill",
		"pinned-skill",
		"manual-connection",
		"manual-agent",
		"undeclared-use",
	}, plan.Uses)
	require.Empty(t, plan.Warnings)
	require.Equal(t, beforeUses, currentUses)
	require.Equal(t, beforeAgentUses, oldAgent.GetUses())
	require.Equal(t, beforeAgentProperties, oldAgent.GetAdditionalProperties().AsMap())
}

func TestPlanAgentServiceUsesIncludesToolboxesAndCleansOldUse(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	definition := loadAgentDependencyDefinition(t, root, "agent", `kind: prompt
name: new-agent
model: gpt-4.1-mini
instructions: Help the user.
harness:
  type: microsoft_copilot_preview
toolboxes:
  - new-tools
`)
	oldAgent := agentDependencyService(t, "agent", AiAgentHost, map[string]any{
		"kind":         "prompt",
		"name":         "old-agent",
		"model":        "gpt-4.1-mini",
		"instructions": "Old instructions.",
		"harness": map[string]any{
			"type": "microsoft_copilot_preview",
		},
		"toolboxes": []any{"old-tools"},
	}, "old-tools", "manual-agent", "project")
	services := map[string]*azdext.ServiceConfig{
		"project":      agentDependencyService(t, "project", AiProjectHost, nil),
		"agent":        oldAgent,
		"new-tools":    agentDependencyService(t, "new-tools", AiToolboxHost, nil),
		"old-tools":    agentDependencyService(t, "old-tools", AiToolboxHost, nil),
		"manual-agent": agentDependencyService(t, "manual-agent", AiAgentHost, nil),
	}

	plan, err := planAgentServiceUses(
		"agent",
		definition,
		[]string{"old-tools", "manual-agent", "project"},
		services,
		root,
	)

	require.NoError(t, err)
	require.Equal(t, []string{"project", "new-tools", "manual-agent"}, plan.Uses)
	require.Empty(t, plan.Warnings)
}

func TestPlanAgentServiceUsesHostedToolboxes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		existing bool
		extra    string
		wantUses []string
	}{
		{
			name:     "add",
			extra:    "toolboxes:\n  - new-tools\n",
			wantUses: []string{"project", "new-tools"},
		},
		{
			name:     "update and deduplicate plural toolbox",
			existing: true,
			extra:    "toolboxes:\n  - new-tools\n  - name: new-tools\n",
			wantUses: []string{"project", "new-tools", "manual-agent"},
		},
		{
			name:     "remove stale toolbox",
			existing: true,
			wantUses: []string{"project", "manual-agent"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			definition := loadAgentDependencyDefinition(t, root, "agent", `kind: hosted
name: agent
codeConfiguration:
  runtime: python_3_13
  entryPoint: app.py
`+test.extra)
			services := map[string]*azdext.ServiceConfig{
				"project":      agentDependencyService(t, "project", AiProjectHost, nil),
				"new-tools":    agentDependencyService(t, "new-tools", AiToolboxHost, nil),
				"old-tools":    agentDependencyService(t, "old-tools", AiToolboxHost, nil),
				"manual-agent": agentDependencyService(t, "manual-agent", AiAgentHost, nil),
			}
			var beforeAgent *azdext.ServiceConfig
			if test.existing {
				oldAgent := hostedAgentDependencyService(t, "agent")
				oldAgent.AdditionalProperties.Fields["toolboxes"] = agentServiceStringListValue([]string{"old-tools"})
				oldAgent.Uses = []string{"old-tools", "manual-agent", "project"}
				services["agent"] = oldAgent
				beforeAgent = proto.Clone(oldAgent).(*azdext.ServiceConfig)
			}

			plan, err := planAgentServiceUses("agent", definition, nil, services, root)
			require.NoError(t, err)
			require.Equal(t, test.wantUses, plan.Uses)
			require.Empty(t, plan.Warnings)
			if test.existing {
				require.True(t, proto.Equal(beforeAgent, services["agent"]))
			}
		})
	}
}

func TestLoadAgentDefinitionFileRejectsSingularToolbox(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"prompt", "hosted"} {
		for _, mixed := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/singular", true: "/mixed"}[mixed], func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				content := "kind: " + kind + "\nname: agent\n"
				if kind == "prompt" {
					content += "model: gpt-4.1-mini\ninstructions: Help.\nharness:\n  type: microsoft_copilot_preview\n"
				} else {
					content += "codeConfiguration:\n  runtime: python_3_13\n  entryPoint: app.py\n"
				}
				content += "toolbox:\n  name: new-tools\n"
				if mixed {
					content += "toolboxes:\n  - new-tools\n"
				}
				path := filepath.Join(root, "agent.yaml")
				require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
				_, err := projectpkg.LoadAgentDefinitionFile(root, "agent.yaml", "agent")
				require.Error(t, err)
			})
		}
	}
}

func TestPlanAgentServiceUsesRejectsHostedVoiceNameCollision(t *testing.T) {
	t.Parallel()

	for _, targetName := range []string{"shared-agent", "SHARED-AGENT", "distinct-agent"} {
		for _, useRef := range []bool{false, true} {
			t.Run(targetName+map[bool]string{false: "/inline", true: "/ref"}[useRef], func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				definition := loadAgentDependencyDefinition(t, root, "wrapper",
					"kind: voice\nname: shared-agent\nconversationEngine:\n"+
						"  type: hosted_agent\n  name: target\n")
				target := hostedAgentDependencyService(t, "target")
				target.AdditionalProperties.Fields["name"] = structpb.NewStringValue(targetName)
				if useRef {
					require.NoError(t, os.WriteFile(filepath.Join(root, "target.yaml"),
						[]byte("kind: hosted\nname: "+targetName+
							"\ncodeConfiguration:\n  runtime: python_3_13\n  entryPoint: app.py\n"), 0o600))
					target = agentDependencyService(t, "target", AiAgentHost,
						map[string]any{"$ref": "./target.yaml"})
				}
				before := proto.Clone(target).(*azdext.ServiceConfig)
				services := map[string]*azdext.ServiceConfig{
					"target":  target,
					"project": agentDependencyService(t, "project", AiProjectHost, nil),
				}
				currentUses := []string{"manual-use", "project"}
				plan, err := planAgentServiceUses("wrapper", definition, currentUses, services, root)
				if targetName == "distinct-agent" {
					require.NoError(t, err)
					require.Equal(t, []string{"project", "target", "manual-use"}, plan.Uses)
				} else {
					require.ErrorContains(t, err, "cannot use the same Foundry name as its target")
					require.Empty(t, plan.Uses)
					localErr, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok)
					require.Equal(t, exterrors.CodeInvalidAgentManifest, localErr.Code)
				}
				require.True(t, proto.Equal(before, target))
				require.Equal(t, []string{"manual-use", "project"}, currentUses)
			})
		}
	}
}

func TestPlanAgentServiceUsesIncludesHostedVoiceTarget(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	definition := loadAgentDependencyDefinition(t, root, "voice-wrapper", `kind: voice
name: voice-wrapper
conversationEngine:
  type: hosted_agent
  name: new-target
  version: deployed
`)
	oldAgent := agentDependencyService(t, "voice-wrapper", AiAgentHost, map[string]any{
		"kind": "voice",
		"name": "voice-wrapper",
		"conversationEngine": map[string]any{
			"type": "hosted_agent",
			"name": "old-target",
		},
	}, "old-target", "manual-agent", "project")
	services := map[string]*azdext.ServiceConfig{
		"project":       agentDependencyService(t, "project", AiProjectHost, nil),
		"voice-wrapper": oldAgent,
		"old-target":    hostedAgentDependencyService(t, "old-target"),
		"new-target":    hostedAgentDependencyService(t, "new-target"),
		"manual-agent":  agentDependencyService(t, "manual-agent", AiAgentHost, nil),
	}

	plan, err := planAgentServiceUses(
		"voice-wrapper",
		definition,
		[]string{"old-target", "manual-agent", "project"},
		services,
		root,
	)

	require.NoError(t, err)
	require.Equal(t, []string{"project", "new-target", "manual-agent"}, plan.Uses)
	require.Empty(t, plan.Warnings)
}

func TestPlanAgentServiceUsesRejectsInvalidDependencies(t *testing.T) {
	t.Parallel()

	voiceDefinition := `kind: voice
name: voice-wrapper
conversationEngine:
  type: hosted_agent
  name: target
`
	promptDefinition := func(extra string) string {
		return `kind: prompt
name: agent
model: gpt-4.1-mini
instructions: Help the user.
` + extra
	}
	hostedDefinition := func(extra string) string {
		return `kind: hosted
name: agent
codeConfiguration:
  runtime: python_3_13
  entryPoint: app.py
` + extra
	}
	tests := []struct {
		name       string
		definition string
		setup      func(*testing.T, map[string]*azdext.ServiceConfig)
		wantError  string
	}{
		{
			name:       "missing Project",
			definition: promptDefinition(""),
			setup: func(_ *testing.T, services map[string]*azdext.ServiceConfig) {
				delete(services, "project")
			},
			wantError: "no azure.ai.project service",
		},
		{
			name:       "multiple Projects",
			definition: promptDefinition(""),
			setup: func(t *testing.T, services map[string]*azdext.ServiceConfig) {
				services["z-project"] = agentDependencyService(
					t, "z-project", AiProjectHost, nil,
				)
				services["a-project"] = agentDependencyService(
					t, "a-project", AiProjectHost, nil,
				)
			},
			wantError: "multiple azure.ai.project services",
		},
		{
			name:       "missing Connection",
			definition: promptDefinition("connections:\n  - missing\n"),
			setup:      func(*testing.T, map[string]*azdext.ServiceConfig) {},
			wantError:  "does not match an azure.ai.connection service",
		},
		{
			name:       "wrong Connection host",
			definition: promptDefinition("connections:\n  - wrong\n"),
			setup: func(t *testing.T, services map[string]*azdext.ServiceConfig) {
				services["wrong"] = agentDependencyService(t, "wrong", AiToolboxHost, nil)
			},
			wantError: "instead of \"azure.ai.connection\"",
		},
		{
			name:       "ambiguous Connection resource name",
			definition: promptDefinition("connections:\n  - shared\n"),
			setup: func(t *testing.T, services map[string]*azdext.ServiceConfig) {
				services["connection-a"] = agentDependencyService(
					t, "connection-a", AiConnectionHost, map[string]any{"name": "shared"},
				)
				services["connection-b"] = agentDependencyService(
					t, "connection-b", AiConnectionHost, map[string]any{"name": "shared"},
				)
			},
			wantError: "is ambiguous",
		},
		{
			name:       "missing Toolbox",
			definition: promptDefinition("harness:\n  type: microsoft_copilot_preview\ntoolboxes:\n  - missing\n"),
			setup:      func(*testing.T, map[string]*azdext.ServiceConfig) {},
			wantError:  "does not match a local service with host \"azure.ai.toolbox\"",
		},
		{
			name:       "plural Toolbox remains a service key",
			definition: promptDefinition("harness:\n  type: microsoft_copilot_preview\ntoolboxes:\n  - My Tools\n"),
			setup: func(t *testing.T, services map[string]*azdext.ServiceConfig) {
				services["MyTools"] = agentDependencyService(t, "MyTools", AiToolboxHost, nil)
			},
			wantError: `toolbox reference "My Tools" does not match`,
		},
		{
			name:       "wrong Toolbox host",
			definition: promptDefinition("harness:\n  type: microsoft_copilot_preview\ntoolboxes:\n  - wrong\n"),
			setup: func(t *testing.T, services map[string]*azdext.ServiceConfig) {
				services["wrong"] = agentDependencyService(t, "wrong", AiConnectionHost, nil)
			},
			wantError: "instead of \"azure.ai.toolbox\"",
		},
		{
			name:       "missing hosted Toolbox",
			definition: hostedDefinition("toolboxes:\n  - missing\n"),
			setup:      func(*testing.T, map[string]*azdext.ServiceConfig) {},
			wantError:  "does not match a local service with host \"azure.ai.toolbox\"",
		},
		{
			name:       "wrong hosted Toolbox host",
			definition: hostedDefinition("toolboxes:\n  - wrong\n"),
			setup: func(t *testing.T, services map[string]*azdext.ServiceConfig) {
				services["wrong"] = agentDependencyService(t, "wrong", AiConnectionHost, nil)
			},
			wantError: "instead of \"azure.ai.toolbox\"",
		},
		{
			name:       "missing local Skill",
			definition: promptDefinition("skills:\n  - missing\n"),
			setup:      func(*testing.T, map[string]*azdext.ServiceConfig) {},
			wantError:  "does not match a local service with host \"azure.ai.skill\"",
		},
		{
			name:       "wrong local Skill host",
			definition: promptDefinition("skills:\n  - wrong\n"),
			setup: func(t *testing.T, services map[string]*azdext.ServiceConfig) {
				services["wrong"] = agentDependencyService(t, "wrong", AiToolboxHost, nil)
			},
			wantError: "instead of \"azure.ai.skill\"",
		},
		{
			name:       "self-reference",
			definition: promptDefinition("connections:\n  - agent\n"),
			setup:      func(*testing.T, map[string]*azdext.ServiceConfig) {},
			wantError:  "points to agent service \"agent\" itself",
		},
		{
			name:       "missing Voice target",
			definition: voiceDefinition,
			setup:      func(*testing.T, map[string]*azdext.ServiceConfig) {},
			wantError:  "does not match a local service with host \"azure.ai.agent\"",
		},
		{
			name:       "wrong Voice target host",
			definition: voiceDefinition,
			setup: func(t *testing.T, services map[string]*azdext.ServiceConfig) {
				services["target"] = agentDependencyService(t, "target", AiToolboxHost, nil)
			},
			wantError: "instead of \"azure.ai.agent\"",
		},
		{
			name:       "non-hosted Voice target",
			definition: voiceDefinition,
			setup: func(t *testing.T, services map[string]*azdext.ServiceConfig) {
				services["target"] = agentDependencyService(t, "target", AiAgentHost, map[string]any{
					"kind":         "prompt",
					"name":         "target",
					"model":        "gpt-4.1-mini",
					"instructions": "Help the user.",
				})
			},
			wantError: "does not reference a hosted agent service",
		},
		{
			name:       "dependency cycle",
			definition: promptDefinition(""),
			setup: func(t *testing.T, services map[string]*azdext.ServiceConfig) {
				services["project"].Uses = []string{"agent"}
			},
			wantError: "dependency cycle",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			definition := loadAgentDependencyDefinition(
				t,
				root,
				"agent",
				test.definition,
			)
			services := map[string]*azdext.ServiceConfig{
				"project": agentDependencyService(t, "project", AiProjectHost, nil),
			}
			test.setup(t, services)
			currentUses := []string{"manual-use", "project"}
			beforeUses := slices.Clone(currentUses)
			projectService := services["project"]
			var beforeProjectUses []string
			var beforeProjectProperties map[string]any
			if projectService != nil {
				beforeProjectUses = slices.Clone(projectService.GetUses())
				if properties := projectService.GetAdditionalProperties(); properties != nil {
					beforeProjectProperties = properties.AsMap()
				}
			}

			_, err := planAgentServiceUses(
				"agent",
				definition,
				currentUses,
				services,
				root,
			)

			require.ErrorContains(t, err, test.wantError)
			require.Equal(t, beforeUses, currentUses)
			if projectService != nil {
				require.Equal(t, beforeProjectUses, projectService.GetUses())
				if properties := projectService.GetAdditionalProperties(); properties != nil {
					require.Equal(t, beforeProjectProperties, properties.AsMap())
				}
			}
		})
	}
}

func TestPlanAgentServiceUsesPreservesUsesWhenOldDefinitionCannotBeParsed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	definition := loadAgentDependencyDefinition(t, root, "agent", `kind: prompt
name: new-agent
model: gpt-4.1-mini
instructions: Help the user.
`)
	oldAgent := agentDependencyService(t, "agent", AiAgentHost, map[string]any{
		"$ref": "../missing.yaml",
	}, "old-connection", "old-toolbox", "old-skill", "manual-agent")
	services := map[string]*azdext.ServiceConfig{
		"project":        agentDependencyService(t, "project", AiProjectHost, nil),
		"agent":          oldAgent,
		"old-connection": agentDependencyService(t, "old-connection", AiConnectionHost, nil),
		"old-toolbox":    agentDependencyService(t, "old-toolbox", AiToolboxHost, nil),
		"old-skill":      agentDependencyService(t, "old-skill", AiSkillHost, nil),
		"manual-agent":   agentDependencyService(t, "manual-agent", AiAgentHost, nil),
	}
	currentUses := []string{
		"old-connection",
		"old-toolbox",
		"old-skill",
		"manual-agent",
		"undeclared-use",
		"project",
	}
	beforeUses := slices.Clone(currentUses)
	beforeAgentUses := slices.Clone(oldAgent.GetUses())
	beforeAgentProperties := oldAgent.GetAdditionalProperties().AsMap()

	plan, err := planAgentServiceUses(
		"agent",
		definition,
		currentUses,
		services,
		root,
	)

	require.NoError(t, err)
	require.Equal(t, []string{
		"project",
		"old-connection",
		"old-toolbox",
		"old-skill",
		"manual-agent",
		"undeclared-use",
	}, plan.Uses)
	require.Len(t, plan.Warnings, 1)
	require.Contains(t, plan.Warnings[0], "preserved existing uses entries")
	require.Equal(t, beforeUses, currentUses)
	require.Equal(t, beforeAgentUses, oldAgent.GetUses())
	require.Equal(t, beforeAgentProperties, oldAgent.GetAdditionalProperties().AsMap())
}

func TestPlanAgentServiceUsesRejectsInvalidDefinitionFormat(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "agent.yaml"),
		[]byte("kind: prompt\nname: broken\ninstructions: Help.\n"),
		0o600,
	))
	_, err := projectpkg.LoadAgentDefinitionFile(root, "agent.yaml", "agent")

	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(t, exterrors.CodeInvalidAgentManifest, localErr.Code)
}

func loadAgentDependencyDefinition(
	t *testing.T,
	projectRoot string,
	serviceName string,
	content string,
) projectpkg.AgentDefinitionFile {
	t.Helper()
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "agent.yaml"),
		[]byte(content),
		0o600,
	))
	definition, err := projectpkg.LoadAgentDefinitionFile(
		projectRoot,
		"agent.yaml",
		serviceName,
	)
	require.NoError(t, err)
	return definition
}

func agentDependencyService(
	t *testing.T,
	name string,
	host string,
	properties map[string]any,
	uses ...string,
) *azdext.ServiceConfig {
	t.Helper()
	service := &azdext.ServiceConfig{
		Name: name,
		Host: host,
		Uses: slices.Clone(uses),
	}
	if properties != nil {
		structure, err := structpb.NewStruct(properties)
		require.NoError(t, err)
		service.AdditionalProperties = structure
	}
	return service
}

func hostedAgentDependencyService(t *testing.T, name string) *azdext.ServiceConfig {
	return agentDependencyService(t, name, AiAgentHost, map[string]any{
		"kind": "hosted",
		"name": name,
		"codeConfiguration": map[string]any{
			"runtime":    "python_3_13",
			"entryPoint": "app.py",
		},
	})
}
