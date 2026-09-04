// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	def, isPrompt := promptDefinitionForService(svc, t.TempDir(), "")
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

	_, isPrompt := promptDefinitionForService(svc, t.TempDir(), "")
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

	_, isPrompt := promptDefinitionForService(svc, filepath.Dir(serviceDir), serviceDir)
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

	assert.Equal(t, "renamed-agent", promptAgentNameForService(svc, t.TempDir()))
}
