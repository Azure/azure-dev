// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type failingPromptEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
}

func (s *failingPromptEnvironmentServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	return nil, status.Error(codes.Internal, "environment service unavailable")
}

// TestPromptDefinitionForServiceInline covers the shape `azd ai agent init`
// writes today: the definition lives on the azure.yaml service entry and
// `kind: prompt` is the only marker. Reading it back is what makes
// list/show/invoke/delete recognize the service at all.
func TestPromptDefinitionForServiceInline(t *testing.T) {
	svc := &azdext.ServiceConfig{
		Name: "my-agent",
		Host: AiAgentHost,
		AdditionalProperties: mustStruct(t, map[string]any{
			"kind":         "prompt",
			"name":         "renamed-agent",
			"model":        "gpt-5.6-terra",
			"instructions": "You are a helpful AI assistant.",
			"harness": map[string]any{
				"type": "github_copilot_preview",
			},
		}),
	}

	def, isPrompt, err := promptDefinitionForService(svc, t.TempDir(), "")
	require.NoError(t, err)
	require.True(t, isPrompt)
	assert.Equal(t, "renamed-agent", def.Name)
	assert.Equal(t, "gpt-5.6-terra", def.Model)
	require.NotNil(t, def.Harness)
	assert.Equal(t, "github_copilot_preview", def.Harness.Type)
}

// TestPromptDefinitionForServiceHosted guards the dispatch: a hosted agent must
// fall through to the hosted code path rather than being handed to the harness.
func TestPromptDefinitionForServiceHosted(t *testing.T) {
	svc := &azdext.ServiceConfig{
		Name: "hosted-agent",
		Host: AiAgentHost,
		AdditionalProperties: mustStruct(t, map[string]any{
			"kind": "hosted",
			"name": "hosted-agent",
		}),
	}

	_, isPrompt, err := promptDefinitionForService(svc, t.TempDir(), "")
	require.NoError(t, err)
	assert.False(t, isPrompt)
}

// TestPromptDefinitionForServiceRequiresExplicitKind confirms an on-disk
// definition does not implicitly classify a service.
func TestPromptDefinitionForServiceRequiresExplicitKind(t *testing.T) {
	serviceDir := t.TempDir()
	agentYaml := "kind: prompt\nname: legacy-agent\nmodel: gpt-4o\nharness:\n  type: github_copilot_preview\n"
	require.NoError(t, os.WriteFile(filepath.Join(serviceDir, "agent.yaml"), []byte(agentYaml), 0600))

	svc := &azdext.ServiceConfig{
		Name:   "legacy",
		Host:   AiAgentHost,
		Config: mustStruct(t, map[string]any{"startupCommand": "ignored"}),
	}

	_, isPrompt, err := promptDefinitionForService(svc, filepath.Dir(serviceDir), serviceDir)
	require.NoError(t, err)
	require.False(t, isPrompt)
}

// TestPromptAgentNameForServicePrefersDefinition asserts the down handlers
// delete the agent the definition names, not the azure.yaml service key. The
// two diverge as soon as an agent is renamed.
func TestPromptAgentNameForServicePrefersDefinition(t *testing.T) {
	svc := &azdext.ServiceConfig{
		Name: "service-key",
		Host: AiAgentHost,
		AdditionalProperties: mustStruct(t, map[string]any{
			"kind": "prompt",
			"name": "renamed-agent",
		}),
	}

	name, err := promptAgentNameForService(svc, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "renamed-agent", name)
}

func TestPromptDefinitionForServiceReturnsInvalidDefinitionError(t *testing.T) {
	svc := &azdext.ServiceConfig{
		Name: "invalid-agent",
		Host: AiAgentHost,
		AdditionalProperties: mustStruct(t, map[string]any{
			"kind":    "prompt",
			"harness": "github_copilot_preview",
		}),
	}

	_, _, err := promptDefinitionForService(svc, t.TempDir(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "harness must be a block")
}

func TestResolvePromptAgentServiceReturnsEnvironmentError(t *testing.T) {
	projectServer := &helpersProjectServer{project: &azdext.ProjectConfig{
		Path: t.TempDir(),
		Services: map[string]*azdext.ServiceConfig{
			"assistant": {
				Name: "assistant",
				Host: AiAgentHost,
				AdditionalProperties: mustStruct(t, map[string]any{
					"kind":         "prompt",
					"name":         "assistant",
					"model":        "gpt-5-mini",
					"instructions": "Be helpful.",
				}),
			},
		},
	}}
	azdClient := newHelpersTestAzdClient(
		t,
		projectServer,
		&helpersPromptServer{},
		&failingPromptEnvironmentServer{},
	)

	_, _, err := resolvePromptAgentService(t.Context(), azdClient, "assistant", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading the azd environment")
	assert.Contains(t, err.Error(), "environment service unavailable")
	assert.NotContains(t, err.Error(), "missing required fields")
}
