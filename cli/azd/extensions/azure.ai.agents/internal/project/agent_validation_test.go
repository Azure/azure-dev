// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestValidateAgentServiceDefinitionSupportedKinds(t *testing.T) {
	tests := []struct {
		name   string
		kind   agent_yaml.AgentKind
		values map[string]any
	}{
		{
			name: "hosted",
			kind: agent_yaml.AgentKindHosted,
			values: map[string]any{
				"kind": "hosted",
				"name": "hosted-agent",
			},
		},
		{
			name: "prompt",
			kind: agent_yaml.AgentKindPrompt,
			values: map[string]any{
				"kind":         "prompt",
				"name":         "prompt-agent",
				"model":        "gpt-4.1-mini",
				"instructions": "Help the user.",
				"harness": map[string]any{
					"type": "github_copilot_preview",
				},
				"memory": map[string]any{
					"store":          "conversation-memory",
					"chatModel":      "gpt-4.1-mini",
					"embeddingModel": "text-embedding-3-small",
				},
				"tools": []any{map[string]any{
					"type": "file_search",
				}},
				"skills": []any{map[string]any{
					"name": "research",
				}},
			},
		},
		{
			name: "voice",
			kind: agent_yaml.AgentKindVoice,
			values: map[string]any{
				"kind":  "voice",
				"name":  "voice-agent",
				"model": map[string]any{"id": "gpt-realtime"},
			},
		},
		{
			name: "prompt-voice",
			kind: agent_yaml.AgentKindPromptVoice,
			values: map[string]any{
				"kind":  "prompt-voice",
				"name":  "prompt-voice-agent",
				"model": map[string]any{"id": "gpt-realtime"},
			},
		},
	}

	for _, tt := range tests {
		for _, source := range []string{"inline", "root-ref"} {
			t.Run(tt.name+"/"+source, func(t *testing.T) {
				root := t.TempDir()
				svc := validationTestService(t, root, source, tt.values)

				got, err := ValidateAgentServiceDefinition(svc, root)

				require.NoError(t, err)
				require.Equal(t, tt.kind, got.Kind)
				require.Equal(t, tt.values["name"], got.Name)
				require.Equal(t, AgentDefinitionSourceInline, got.Source)
			})
		}
	}
}

func TestValidateAgentServiceDefinitionRejectsMalformedKinds(t *testing.T) {
	tests := []struct {
		name               string
		values             map[string]any
		serviceEnvironment map[string]string
		wantCode           string
		want               string
	}{
		{
			name: "prompt model",
			values: map[string]any{
				"kind": "prompt", "name": "prompt-agent", "instructions": "Help.",
			},
			wantCode: exterrors.CodeInvalidAgentManifest,
			want:     "non-empty model",
		},
		{
			name: "prompt instructions",
			values: map[string]any{
				"kind": "prompt", "name": "prompt-agent", "model": "gpt-4.1-mini",
			},
			wantCode: exterrors.CodeInvalidAgentManifest,
			want:     "non-empty instructions",
		},
		{
			name: "prompt harness",
			values: map[string]any{
				"kind":         "prompt",
				"name":         "prompt-agent",
				"model":        "gpt-4.1-mini",
				"instructions": "Help.",
				"harness":      "github_copilot_preview",
			},
			wantCode: exterrors.CodeInvalidAgentManifest,
			want:     "harness must be a block",
		},
		{
			name: "prompt memory",
			values: map[string]any{
				"kind":         "prompt",
				"name":         "prompt-agent",
				"model":        "gpt-4.1-mini",
				"instructions": "Help.",
				"memory":       map[string]any{"store": "memory"},
			},
			wantCode: exterrors.CodeInvalidAgentManifest,
			want:     "memory.chatModel",
		},
		{
			name: "prompt tools",
			values: map[string]any{
				"kind":         "prompt",
				"name":         "prompt-agent",
				"model":        "gpt-4.1-mini",
				"instructions": "Help.",
				"tools":        []any{map[string]any{"name": "missing-type"}},
			},
			wantCode: exterrors.CodeInvalidAgentManifest,
			want:     "missing a 'type' key",
		},
		{
			name: "prompt policies",
			values: map[string]any{
				"kind":         "prompt",
				"name":         "prompt-agent",
				"model":        "gpt-4.1-mini",
				"instructions": "Help.",
				"policies": []any{map[string]any{
					"type": "unsupported",
				}},
			},
			wantCode: exterrors.CodeInvalidAgentManifest,
			want:     "unsupported type",
		},
		{
			name: "prompt conflicting authored skill versions",
			values: map[string]any{
				"kind":         "prompt",
				"name":         "prompt-agent",
				"model":        "gpt-4.1-mini",
				"instructions": "Help.",
				"skills": []any{
					map[string]any{"name": " Research ", "version": "1"},
					map[string]any{"name": "research", "version": "2"},
				},
			},
			wantCode: exterrors.CodeInvalidAgentManifest,
			want:     `conflicting authored versions "1" and "2"`,
		},
		{
			name: "hosted session",
			values: map[string]any{
				"kind": "hosted",
				"name": "hosted-agent",
				"sessionConfiguration": map[string]any{
					"idleTimeoutSeconds": 1,
				},
			},
			wantCode: exterrors.CodeInvalidAgentManifest,
			want:     "session idle timeout must be between",
		},
		{
			name: "hosted activity",
			values: map[string]any{
				"kind": "hosted",
				"name": "hosted-agent",
				"protocols": []any{map[string]any{
					"protocol": "activity",
					"version":  "1.0",
				}},
				"activity": map[string]any{
					"digitalWorkerType": "unsupported",
				},
			},
			wantCode: exterrors.CodeInvalidAgentManifest,
			want:     "activity.digitalWorkerType must be",
		},
		{
			name: "hosted registry connection without image",
			values: map[string]any{
				"kind":                 "hosted",
				"name":                 "hosted-agent",
				"registryConnectionId": "private-registry",
			},
			wantCode: exterrors.CodeInvalidServiceConfig,
			want:     "requires a pre-built container image",
		},
		{
			name: "hosted service environment name",
			values: map[string]any{
				"kind": "hosted",
				"name": "hosted-agent",
			},
			serviceEnvironment: map[string]string{"invalid-name": "value"},
			wantCode:           exterrors.CodeInvalidEnvironmentVariableName,
			want:               `"invalid-name"`,
		},
		{
			name: "hosted definition environment name",
			values: map[string]any{
				"kind": "hosted",
				"name": "hosted-agent",
				"environmentVariables": []any{
					map[string]any{"name": "9INVALID", "value": "value"},
				},
			},
			wantCode: exterrors.CodeInvalidEnvironmentVariableName,
			want:     `"9INVALID"`,
		},
		{
			name: "voice",
			values: map[string]any{
				"kind":             "voice",
				"name":             "voice-agent",
				"model":            map[string]any{"id": "gpt-realtime"},
				"outputModalities": []any{""},
			},
			wantCode: exterrors.CodeInvalidAgentManifest,
			want:     "outputModalities[0] must not be blank",
		},
		{
			name: "voice activity use case",
			values: map[string]any{
				"kind":  "voice",
				"name":  "voice-agent",
				"model": map[string]any{"id": "gpt-realtime"},
				"activity": map[string]any{
					"useCase": "digital_worker",
				},
			},
			wantCode: exterrors.CodeInvalidAgentManifest,
			want:     "activity.useCase is not supported",
		},
		{
			name: "voice digital worker type",
			values: map[string]any{
				"kind":  "voice",
				"name":  "voice-agent",
				"model": map[string]any{"id": "gpt-realtime"},
				"activity": map[string]any{
					"digitalWorkerType": "m365",
				},
			},
			wantCode: exterrors.CodeInvalidAgentManifest,
			want:     "requires an Activity-protocol hosted agent",
		},
		{
			name: "missing kind",
			values: map[string]any{
				"name": "missing-kind",
			},
			wantCode: exterrors.CodeInvalidAgentManifest,
			want:     "requires a kind",
		},
		{
			name: "non-string kind",
			values: map[string]any{
				"kind": map[string]any{"type": "prompt"},
				"name": "invalid-kind",
			},
			wantCode: exterrors.CodeInvalidAgentManifest,
			want:     "kind must be a non-empty string",
		},
		{
			name: "unsupported kind",
			values: map[string]any{
				"kind": "future-agent",
				"name": "future-agent",
			},
			wantCode: exterrors.CodeUnsupportedAgentKind,
			want:     "unsupported kind",
		},
	}

	for _, tt := range tests {
		for _, source := range []string{"inline", "root-ref"} {
			t.Run(tt.name+"/"+source, func(t *testing.T) {
				root := t.TempDir()
				svc := validationTestService(t, root, source, tt.values)
				svc.Environment = tt.serviceEnvironment

				_, err := ValidateAgentServiceDefinition(svc, root)

				require.ErrorContains(t, err, tt.want)
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok, "expected LocalError, got %T: %v", err, err)
				require.Equal(t, tt.wantCode, localErr.Code)
			})
		}
	}
}

func TestValidateAgentServiceDefinitionRejectsWorkflow(t *testing.T) {
	values := map[string]any{
		"kind": "workflow",
		"name": "workflow-agent",
	}

	for _, source := range []string{"inline", "root-ref"} {
		t.Run(source, func(t *testing.T) {
			root := t.TempDir()
			svc := validationTestService(t, root, source, values)

			_, err := ValidateAgentServiceDefinition(svc, root)

			require.ErrorContains(t, err, `declares unsupported kind "workflow"`)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected LocalError, got %T: %v", err, err)
			require.Equal(t, exterrors.CodeUnsupportedAgentKind, localErr.Code)
			require.Equal(t,
				"set kind to one of: hosted, prompt, prompt-voice, voice",
				localErr.Suggestion)
		})
	}
}

func TestValidateAgentServiceDefinitionClassifiesRootRefErrors(t *testing.T) {
	t.Run("preserves file ref diagnostics", func(t *testing.T) {
		root := t.TempDir()
		props, err := structpb.NewStruct(map[string]any{"$ref": "./missing-agent.yaml"})
		require.NoError(t, err)
		svc := &azdext.ServiceConfig{
			Name:                 "agent-service",
			Host:                 "azure.ai.agent",
			AdditionalProperties: props,
		}

		_, err = ValidateAgentServiceDefinition(svc, root)

		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok, "expected LocalError, got %T: %v", err, err)
		require.Equal(t, foundry.CodeInvalidFileRef, localErr.Code)
		require.Equal(t, "Check that the path is correct and the file exists and is readable.", localErr.Suggestion)
	})

	t.Run("classifies plain resolver errors", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(root, "agent.yaml"),
			[]byte("kind: prompt\nname: prompt-agent\nproject: ./src\n"),
			0o600,
		))
		props, err := structpb.NewStruct(map[string]any{"$ref": "./agent.yaml"})
		require.NoError(t, err)
		svc := &azdext.ServiceConfig{
			Name:                 "agent-service",
			Host:                 "azure.ai.agent",
			AdditionalProperties: props,
		}

		_, err = ValidateAgentServiceDefinition(svc, root)

		require.ErrorContains(t, err, `root $ref must not provide core field "project"`)
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok, "expected LocalError, got %T: %v", err, err)
		require.Equal(t, exterrors.CodeInvalidAgentManifest, localErr.Code)
		require.Equal(t,
			"fix the agent definition in azure.yaml or its explicitly referenced file",
			localErr.Suggestion)
	})
}

func TestValidateAgentServiceDefinitionRejectsMalformedSkill(t *testing.T) {
	values := map[string]any{
		"kind":         "prompt",
		"name":         "prompt-agent",
		"model":        "gpt-4.1-mini",
		"instructions": "Help.",
	}

	for _, source := range []string{"inline", "root-ref"} {
		t.Run(source, func(t *testing.T) {
			root := t.TempDir()
			svc := validationTestService(t, root, source, values)
			agentDir := filepath.Join(root, svc.GetRelativePath())
			if source == "root-ref" {
				agentDir = filepath.Join(root, "definitions")
			}
			skillDir := filepath.Join(agentDir, "skills", "broken")
			require.NoError(t, os.MkdirAll(skillDir, 0o700))
			require.NoError(t, os.WriteFile(
				filepath.Join(skillDir, "SKILL.md"),
				[]byte("---\nname: broken\n---\n"),
				0o600,
			))

			_, err := ValidateAgentServiceDefinition(svc, root)

			require.ErrorContains(t, err, "is missing 'description'")
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeInvalidAgentManifest, localErr.Code)
		})
	}
}

func validationTestService(
	t *testing.T,
	root string,
	source string,
	values map[string]any,
) *azdext.ServiceConfig {
	t.Helper()

	propsValues := values
	if source == "root-ref" {
		definitionDir := filepath.Join(root, "definitions")
		require.NoError(t, os.MkdirAll(definitionDir, 0o700))
		data, err := yaml.Marshal(values)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(
			filepath.Join(definitionDir, "agent.yaml"),
			data,
			0o600,
		))
		propsValues = map[string]any{"$ref": "./definitions/agent.yaml"}
	}

	props, err := structpb.NewStruct(propsValues)
	require.NoError(t, err)
	return &azdext.ServiceConfig{
		Name:                 "agent-service",
		Host:                 "azure.ai.agent",
		RelativePath:         "src/agent",
		AdditionalProperties: props,
	}
}
