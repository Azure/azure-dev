// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestDecodeFoundryService_InlineShape(t *testing.T) {
	props, err := structpb.NewStruct(map[string]any{
		"endpoint": "https://acct.services.ai.azure.com/api/projects/p",
		"deployments": []any{
			map[string]any{
				"name":  "gpt-4.1-mini",
				"model": map[string]any{"format": "OpenAI", "name": "gpt-4.1-mini", "version": "2025-04-14"},
			},
		},
		"connections": []any{
			map[string]any{"name": "search-conn", "category": "CognitiveSearch", "target": "https://x"},
		},
		"toolboxes": []any{
			map[string]any{"name": "support-toolbox"},
		},
		"skills":   []any{map[string]any{"name": "triage"}},
		"routines": []any{map[string]any{"name": "nightly"}},
		"agents": []any{
			map[string]any{
				"name":           "basic-agent",
				"kind":           "hosted",
				"project":        "src/basic-agent",
				"startupCommand": "python main.py",
				"protocols":      []any{map[string]any{"protocol": "responses", "version": "1.0.0"}},
				"env":            map[string]any{"FOUNDRY_MODEL_DEPLOYMENT_NAME": "gpt-4.1-mini"},
			},
		},
	})
	require.NoError(t, err)

	cfg, err := decodeFoundryService(props, "")
	require.NoError(t, err)

	assert.Equal(t, "https://acct.services.ai.azure.com/api/projects/p", cfg.Endpoint)

	require.Len(t, cfg.Deployments, 1)
	assert.Equal(t, "gpt-4.1-mini", cfg.Deployments[0].Name)
	assert.Equal(t, "OpenAI", cfg.Deployments[0].Model.Format)

	require.Len(t, cfg.Connections, 1)
	assert.Equal(t, "search-conn", cfg.Connections[0].Name)
	assert.Equal(t, "CognitiveSearch", cfg.Connections[0].Category)

	require.Len(t, cfg.Toolboxes, 1)
	assert.Equal(t, "support-toolbox", cfg.Toolboxes[0].Name)

	require.Len(t, cfg.Skills, 1)
	assert.Equal(t, "triage", cfg.Skills[0].Name)

	require.Len(t, cfg.Routines, 1)
	assert.Equal(t, "nightly", cfg.Routines[0].Name)

	require.Len(t, cfg.Agents, 1)
	agent := cfg.Agents[0]
	assert.Equal(t, "basic-agent", agent.Name)
	assert.Equal(t, agentKindHosted, agent.Kind)
	assert.Equal(t, "src/basic-agent", agent.Project)
	assert.Equal(t, "python main.py", agent.StartupCommand)
	require.Len(t, agent.Protocols, 1)
	assert.Equal(t, "responses", agent.Protocols[0].Protocol)
	assert.Equal(t, "gpt-4.1-mini", agent.Env["FOUNDRY_MODEL_DEPLOYMENT_NAME"])
}

func TestDecodeFoundryService_NilProps(t *testing.T) {
	cfg, err := decodeFoundryService(nil, "")
	require.NoError(t, err)
	assert.Empty(t, cfg.Agents)
	assert.Empty(t, cfg.Deployments)
	assert.Empty(t, cfg.Endpoint)
}

func TestDecodeFoundryService_ResolvesRefsWithRebaseAndOverlay(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "agents"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "toolboxes"), 0o750))
	// project: ../src/support is relative to the agents/ folder; after
	// rebasing it must resolve to the project-root-relative src/support.
	require.NoError(t, os.WriteFile(filepath.Join(root, "agents", "support.yaml"), []byte(
		"name: support-agent\nkind: hosted\nproject: ../src/support\n"+
			"protocols:\n  - protocol: responses\n    version: \"1.0.0\"\n"+
			"env:\n  FOUNDRY_MODEL_DEPLOYMENT_NAME: gpt-4.1\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "toolboxes", "research.yaml"), []byte(
		"name: research-toolbox\n"), 0o600))

	props, err := structpb.NewStruct(map[string]any{
		"agents": []any{
			map[string]any{"name": "inline-agent", "kind": "prompt"},
			// $ref with a sibling overlay key: description should override.
			map[string]any{"$ref": "./agents/support.yaml", "description": "overridden"},
		},
		"toolboxes": []any{
			map[string]any{"$ref": "./toolboxes/research.yaml"},
		},
	})
	require.NoError(t, err)

	cfg, err := decodeFoundryService(props, root)
	require.NoError(t, err)

	require.Len(t, cfg.Agents, 2)
	assert.Equal(t, "inline-agent", cfg.Agents[0].Name)

	resolved := cfg.Agents[1]
	assert.Equal(t, "support-agent", resolved.Name, "$ref agent should be loaded")
	assert.Equal(t, agentKindHosted, resolved.Kind)
	assert.Equal(t, "src/support", resolved.Project, "relative project should rebase to the ref file's dir")
	assert.Equal(t, "overridden", resolved.Description, "sibling key should overlay the loaded file")
	assert.Equal(t, "gpt-4.1", resolved.Env["FOUNDRY_MODEL_DEPLOYMENT_NAME"])

	require.Len(t, cfg.Toolboxes, 1)
	assert.Equal(t, "research-toolbox", cfg.Toolboxes[0].Name, "$ref toolbox should be loaded")
}

func TestDecodeFoundryService_UnresolvableRefDropped(t *testing.T) {
	root := t.TempDir()
	props, err := structpb.NewStruct(map[string]any{
		"agents": []any{
			map[string]any{"name": "keep-me", "kind": "prompt"},
			map[string]any{"$ref": "./agents/missing.yaml"},
		},
	})
	require.NoError(t, err)

	cfg, err := decodeFoundryService(props, root)
	require.NoError(t, err)
	// The unresolvable ref is dropped; the inline agent survives.
	require.Len(t, cfg.Agents, 1)
	assert.Equal(t, "keep-me", cfg.Agents[0].Name)
}
