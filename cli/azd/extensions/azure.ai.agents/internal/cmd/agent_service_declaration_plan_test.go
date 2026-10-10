// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	projectpkg "azureaiagent/internal/project"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestPlanAgentServiceDeclaration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	definition := loadAgentServiceDeclarationDefinition(
		t,
		root,
		"definitions/agent.yaml",
		"agent-service",
	)
	dependencies := agentDependencyPlan{
		Uses:     []string{"ai-project", "new-connection", "manual-agent"},
		Warnings: []string{"preserved an unresolved existing dependency"},
	}

	tests := []struct {
		name           string
		existing       map[string]any
		sourceOverride *string
		wantMutation   agentServiceMutation
		wantService    map[string]any
	}{
		{
			name:         "add with default source",
			wantMutation: agentServiceMutationAdded,
			wantService: map[string]any{
				"host":    AiAgentHost,
				"$ref":    "./definitions/agent.yaml",
				"project": "definitions",
				"uses":    []any{"ai-project", "new-connection", "manual-agent"},
			},
		},
		{
			name: "update preserves core fields and unrelated extensions",
			existing: map[string]any{
				"$ref":          "./old-agent.yaml",
				"host":          AiAgentHost,
				"project":       "./existing-source",
				"uses":          []any{"old-connection", "ai-project", "manual-agent"},
				"apiVersion":    "2025-01-01",
				"condition":     "${DEPLOY_AGENT}",
				"dist":          "dist",
				"docker":        map[string]any{"path": "./Dockerfile", "context": "."},
				"env":           map[string]any{"API_KEY": "${API_KEY}", "PROMPT": "${PROMPT}"},
				"hooks":         map[string]any{"predeploy": []any{"echo ${MESSAGE}"}},
				"image":         "${AGENT_IMAGE}",
				"infra":         map[string]any{"path": "infra", "module": "main"},
				"k8s":           map[string]any{"namespace": "agents"},
				"language":      "python",
				"module":        "infra",
				"remoteBuild":   true,
				"resourceGroup": "${RESOURCE_GROUP}",
				"resourceName":  "${AGENT_NAME}",
				"extensionState": map[string]any{
					"enabled": true,
					"label":   "keep",
				},
				"kind":         "prompt",
				"name":         "old-agent",
				"model":        "gpt-4.1-mini",
				"instructions": "Old instructions.",
				"connections":  []any{"old-connection"},
				"toolboxes":    []any{"old-toolbox"},
				"skills":       []any{"old-skill"},
				"memory":       map[string]any{"enabled": true},
				"container":    map[string]any{"cpu": "1"},
				"deployments":  []any{map[string]any{"name": "old"}},
				"resources":    []any{map[string]any{"name": "old"}},
				"environmentVariables": []any{
					map[string]any{"name": "OLD", "value": "old"},
				},
			},
			wantMutation: agentServiceMutationUpdated,
			wantService: map[string]any{
				"$ref":          "./definitions/agent.yaml",
				"host":          AiAgentHost,
				"project":       "./existing-source",
				"uses":          []any{"ai-project", "new-connection", "manual-agent"},
				"apiVersion":    "2025-01-01",
				"condition":     "${DEPLOY_AGENT}",
				"dist":          "dist",
				"docker":        map[string]any{"path": "./Dockerfile", "context": "."},
				"env":           map[string]any{"API_KEY": "${API_KEY}", "PROMPT": "${PROMPT}"},
				"hooks":         map[string]any{"predeploy": []any{"echo ${MESSAGE}"}},
				"image":         "${AGENT_IMAGE}",
				"infra":         map[string]any{"path": "infra", "module": "main"},
				"k8s":           map[string]any{"namespace": "agents"},
				"language":      "python",
				"module":        "infra",
				"remoteBuild":   true,
				"resourceGroup": "${RESOURCE_GROUP}",
				"resourceName":  "${AGENT_NAME}",
				"extensionState": map[string]any{
					"enabled": true,
					"label":   "keep",
				},
			},
		},
		{
			name: "update defaults missing project to definition directory",
			existing: map[string]any{
				"host": AiAgentHost,
				"$ref": "./old-agent.yaml",
			},
			wantMutation: agentServiceMutationUpdated,
			wantService: map[string]any{
				"host":    AiAgentHost,
				"$ref":    "./definitions/agent.yaml",
				"project": "definitions",
				"uses":    []any{"ai-project", "new-connection", "manual-agent"},
			},
		},
		{
			name: "unchanged service is a no-op",
			existing: map[string]any{
				"host":     AiAgentHost,
				"$ref":     "./definitions/agent.yaml",
				"project":  "definitions",
				"uses":     []any{"ai-project", "new-connection", "manual-agent"},
				"env":      map[string]any{"PROMPT": "${PROMPT}"},
				"hooks":    map[string]any{"predeploy": []any{"echo ${MESSAGE}"}},
				"language": "python",
				"extensionState": map[string]any{
					"enabled": true,
				},
			},
			wantMutation: agentServiceMutationUnchanged,
			wantService: map[string]any{
				"host":     AiAgentHost,
				"$ref":     "./definitions/agent.yaml",
				"project":  "definitions",
				"uses":     []any{"ai-project", "new-connection", "manual-agent"},
				"env":      map[string]any{"PROMPT": "${PROMPT}"},
				"hooks":    map[string]any{"predeploy": []any{"echo ${MESSAGE}"}},
				"language": "python",
				"extensionState": map[string]any{
					"enabled": true,
				},
			},
		},
		{
			name: "source override replaces configured project",
			existing: map[string]any{
				"host":    AiAgentHost,
				"$ref":    "./old-agent.yaml",
				"project": "../outside",
			},
			sourceOverride: new("./source"),
			wantMutation:   agentServiceMutationUpdated,
			wantService: map[string]any{
				"host":    AiAgentHost,
				"$ref":    "./definitions/agent.yaml",
				"project": "source",
				"uses":    []any{"ai-project", "new-connection", "manual-agent"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var existing *structpb.Struct
			if test.existing != nil {
				existing = agentServiceDeclarationStruct(t, test.existing)
			}
			var beforeExisting *structpb.Struct
			if existing != nil {
				beforeExisting = proto.Clone(existing).(*structpb.Struct)
			}
			beforeUses := slices.Clone(dependencies.Uses)

			plan, err := planAgentServiceDeclaration(agentServiceDeclarationInput{
				ServiceName:     "agent-service",
				ProjectRoot:     root,
				Definition:      definition,
				Dependencies:    dependencies,
				ExistingService: existing,
				SourceOverride:  test.sourceOverride,
			})

			require.NoError(t, err)
			require.Equal(t, test.wantMutation, plan.Mutation)
			require.Equal(t, test.wantService, plan.DesiredService.AsMap())
			require.Equal(t, dependencies.Warnings, plan.Warnings)
			if existing != nil {
				require.True(t, proto.Equal(beforeExisting, existing))
			}
			require.Equal(t, beforeUses, dependencies.Uses)

			if test.wantMutation == agentServiceMutationAdded {
				require.NotNil(t, plan.NewServiceConfig)
				require.Equal(t, "agent-service", plan.NewServiceConfig.GetName())
				require.Equal(t, AiAgentHost, plan.NewServiceConfig.GetHost())
				require.Equal(t, "definitions", plan.NewServiceConfig.GetRelativePath())
				require.Equal(t, dependencies.Uses, plan.NewServiceConfig.GetUses())
				require.Equal(
					t,
					map[string]any{"$ref": "./definitions/agent.yaml"},
					plan.NewServiceConfig.GetAdditionalProperties().AsMap(),
				)
			} else {
				require.Nil(t, plan.NewServiceConfig)
			}
		})
	}
}

func TestPlanAgentServiceDeclarationRootDefinition(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	definition := loadAgentServiceDeclarationDefinition(
		t,
		root,
		"agent.yaml",
		"agent-service",
	)

	plan, err := planAgentServiceDeclaration(agentServiceDeclarationInput{
		ServiceName:  "agent-service",
		ProjectRoot:  root,
		Definition:   definition,
		Dependencies: agentDependencyPlan{Uses: []string{"ai-project"}},
	})

	require.NoError(t, err)
	require.Equal(t, agentServiceMutationAdded, plan.Mutation)
	require.Equal(t, "./agent.yaml", plan.DesiredService.GetFields()["$ref"].GetStringValue())
	require.Equal(t, ".", plan.DesiredService.GetFields()["project"].GetStringValue())
	require.Equal(t, ".", plan.NewServiceConfig.GetRelativePath())
}

func TestPlanAgentServiceDeclarationIsDeterministic(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	definition := loadAgentServiceDeclarationDefinition(
		t,
		root,
		"definitions/agent.yaml",
		"agent-service",
	)
	existing := agentServiceDeclarationStruct(t, map[string]any{
		"host":    AiAgentHost,
		"project": "definitions",
		"env":     map[string]any{"PROMPT": "${PROMPT}"},
		"$ref":    "./old-agent.yaml",
		"uses":    []any{"old", "manual"},
	})
	input := agentServiceDeclarationInput{
		ServiceName:     "agent-service",
		ProjectRoot:     root,
		Definition:      definition,
		Dependencies:    agentDependencyPlan{Uses: []string{"ai-project", "manual"}},
		ExistingService: existing,
	}

	first, err := planAgentServiceDeclaration(input)
	require.NoError(t, err)
	second, err := planAgentServiceDeclaration(input)
	require.NoError(t, err)

	firstBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(first.DesiredService)
	require.NoError(t, err)
	secondBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(second.DesiredService)
	require.NoError(t, err)
	require.Equal(t, firstBytes, secondBytes)
}

func TestPlanAgentServiceDeclarationRejectsInvalidStateWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	definition := loadAgentServiceDeclarationDefinition(
		t,
		root,
		"definitions/agent.yaml",
		"agent-service",
	)
	tests := []struct {
		name           string
		serviceName    string
		existing       map[string]any
		sourceOverride *string
		wantError      string
	}{
		{
			name:        "conflicting host",
			serviceName: "agent-service",
			existing:    map[string]any{"host": "containerapp"},
			wantError:   `has host "containerapp"`,
		},
		{
			name:        "non-empty nested config",
			serviceName: "agent-service",
			existing: map[string]any{
				"host":   AiAgentHost,
				"config": map[string]any{"kind": "prompt"},
			},
			wantError: "incompatible with a file-backed definition",
		},
		{
			name:        "invalid configured project path",
			serviceName: "agent-service",
			existing: map[string]any{
				"host":    AiAgentHost,
				"project": "../outside",
			},
			wantError: "invalid project source",
		},
		{
			name:           "invalid source override",
			serviceName:    "agent-service",
			existing:       map[string]any{"host": AiAgentHost},
			sourceOverride: new("../outside"),
			wantError:      "invalid project source",
		},
		{
			name:        "invalid service name",
			serviceName: "not/a/service",
			existing:    map[string]any{"host": AiAgentHost},
			wantError:   "cannot be used as an azure.yaml service name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			existing := agentServiceDeclarationStruct(t, test.existing)
			beforeExisting := proto.Clone(existing).(*structpb.Struct)
			dependencies := agentDependencyPlan{
				Uses:     []string{"ai-project", "manual-agent"},
				Warnings: []string{"preserved an unresolved existing dependency"},
			}
			beforeUses := slices.Clone(dependencies.Uses)
			beforeWarnings := slices.Clone(dependencies.Warnings)
			beforeProperties := definition.Properties
			beforeResolvedProperties := definition.ResolvedProperties

			plan, err := planAgentServiceDeclaration(agentServiceDeclarationInput{
				ServiceName:     test.serviceName,
				ProjectRoot:     root,
				Definition:      definition,
				Dependencies:    dependencies,
				ExistingService: existing,
				SourceOverride:  test.sourceOverride,
			})

			require.ErrorContains(t, err, test.wantError)
			require.Nil(t, plan.DesiredService)
			require.Nil(t, plan.NewServiceConfig)
			require.True(t, proto.Equal(beforeExisting, existing))
			require.Equal(t, beforeUses, dependencies.Uses)
			require.Equal(t, beforeWarnings, dependencies.Warnings)
			require.Equal(t, beforeProperties, definition.Properties)
			require.Equal(t, beforeResolvedProperties, definition.ResolvedProperties)
		})
	}
}

func loadAgentServiceDeclarationDefinition(
	t *testing.T,
	projectRoot string,
	relativePath string,
	serviceName string,
) projectpkg.AgentDefinitionFile {
	t.Helper()

	filePath := filepath.Join(projectRoot, filepath.FromSlash(relativePath))
	require.NoError(t, os.MkdirAll(filepath.Dir(filePath), 0o750))
	require.NoError(t, os.WriteFile(filePath, []byte(`kind: prompt
name: new-agent
model: gpt-4.1-mini
instructions: Help the user.
`), 0o600))

	definition, err := projectpkg.LoadAgentDefinitionFile(
		projectRoot,
		relativePath,
		serviceName,
	)
	require.NoError(t, err)
	return definition
}

func agentServiceDeclarationStruct(t *testing.T, values map[string]any) *structpb.Struct {
	t.Helper()

	result, err := structpb.NewStruct(values)
	require.NoError(t, err)
	return result
}
