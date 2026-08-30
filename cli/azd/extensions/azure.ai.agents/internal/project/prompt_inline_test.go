// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

// TestPromptAgentInlineRoundTripPreservesMemory is the regression test for the
// reason PromptAgentInline exists.
//
// agent_yaml.PromptAgent tags Memory json:"-" because the prompt-agent API
// defines no such field. Service properties round-trip through JSON, so
// marshaling a PromptAgent directly would drop an authored memory block with no
// error and no diagnostic — the agent would simply deploy without recall.
func TestPromptAgentInlineRoundTripPreservesMemory(t *testing.T) {
	t.Parallel()

	original := agent_yaml.PromptAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindPrompt,
			Name: "memory-agent",
		},
		Model:        "gpt-4.1-mini",
		Instructions: "You are a helpful AI assistant.",
		Memory: &agent_yaml.PromptMemory{
			Store: "conversation-store",
		},
	}

	props, err := PromptAgentDefinitionToServiceProperties(original)
	require.NoError(t, err)
	require.Contains(t, props.AsMap(), "memory", "memory block must survive into azure.yaml")

	svc := &azdext.ServiceConfig{Name: "memory-agent", AdditionalProperties: props}
	got, found, err := PromptAgentFromResolvedService(svc, t.TempDir())
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, got.Memory, "memory block must survive the round trip")
	require.Equal(t, "conversation-store", got.Memory.Store)
}

// TestPromptAgentInlineRoundTripPreservesDefinition covers the fields the deploy
// path reads, so a marshaling change that silently drops one is caught here
// rather than at deploy time.
func TestPromptAgentInlineRoundTripPreservesDefinition(t *testing.T) {
	t.Parallel()

	original := agent_yaml.PromptAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindPrompt,
			Name: "full-agent",
		},
		Model:        "gpt-4.1-mini",
		Instructions: "Be concise.",
		Harness: &agent_yaml.PromptHarness{
			Type: "github_copilot_preview",
		},
		Tools:       []any{map[string]any{"type": "code_interpreter"}},
		Connections: []string{"search"},
	}

	props, err := PromptAgentDefinitionToServiceProperties(original)
	require.NoError(t, err)

	svc := &azdext.ServiceConfig{Name: "full-agent", AdditionalProperties: props}
	got, found, err := PromptAgentFromResolvedService(svc, t.TempDir())
	require.NoError(t, err)
	require.True(t, found)

	require.Equal(t, agent_yaml.AgentKindPrompt, got.Kind)
	require.Equal(t, "full-agent", got.Name)
	require.Equal(t, "gpt-4.1-mini", got.Model)
	require.Equal(t, "Be concise.", got.Instructions)
	require.NotNil(t, got.Harness)
	require.Equal(t, "github_copilot_preview", got.Harness.Type)
	require.Len(t, got.Tools, 1)
	require.Len(t, got.Connections, 1)
	require.Equal(t, "search", got.Connections[0])

	// Never authored: the deploy graph resolves it from the skills/ folder.
}

// TestPromptAgentFromResolvedServiceIgnoresOtherKinds confirms a hosted or voice
// entry is reported as "not found" rather than as an error, so the hosted
// resolvers keep their turn.
func TestPromptAgentFromResolvedServiceIgnoresOtherKinds(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"hosted", "prompt-voice"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			svc := &azdext.ServiceConfig{
				Name: "other",
				AdditionalProperties: mustStruct(t, map[string]any{
					"kind": kind,
					"name": "other",
				}),
			}
			_, found, err := PromptAgentFromResolvedService(svc, t.TempDir())
			require.NoError(t, err)
			require.False(t, found)
		})
	}
}

// TestPromptAgentFromResolvedServiceNoDefinition confirms an entry carrying no
// definition at all falls through quietly, which is what lets projects that
// still keep their definition in a file reach the file-based path.
func TestPromptAgentFromResolvedServiceNoDefinition(t *testing.T) {
	t.Parallel()

	svc := &azdext.ServiceConfig{
		Name:   "legacy",
		Config: mustStruct(t, map[string]any{"promptAgent": map[string]any{"workspace": "w"}}),
	}
	_, found, err := PromptAgentFromResolvedService(svc, t.TempDir())
	require.NoError(t, err)
	require.False(t, found)
}

// TestPromptAgentInlineStrictValidation is the regression test for the
// validation gap the inline shape opens.
//
// The strict checks on harness: and memory: live in UnmarshalYAML, which never
// runs for an inline definition: core azd parses azure.yaml and hands the
// extension protobuf, which is decoded as JSON. Without the explicit validation
// pass these manifests would deploy an agent whose capabilities differ from what
// was authored.
func TestPromptAgentInlineStrictValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{
			name: "harness as a string names the replacement block",
			props: map[string]any{
				"kind":    "prompt",
				"name":    "a",
				"harness": "github_copilot_preview",
			},
			wantErr: "harness must be a block, not a string",
		},
		{
			name: "obsolete harness string is upgraded in the suggestion",
			props: map[string]any{
				"kind":    "prompt",
				"name":    "a",
				"harness": "ghcp",
			},
			wantErr: "type: github_copilot_preview",
		},
		{
			name: "obsolete harness type is rejected by name",
			props: map[string]any{
				"kind":    "prompt",
				"name":    "a",
				"harness": map[string]any{"type": "ghcp"},
			},
			wantErr: "no longer accepted",
		},
		{
			name: "harness typo binds nothing and is rejected",
			props: map[string]any{
				"kind": "prompt",
				"name": "a",
				"harness": map[string]any{
					"type":         "github_copilot_preview",
					"builtin_tool": map[string]any{"excluded": []any{"bash"}},
				},
			},
			wantErr: "builtin_tool",
		},
		{
			name: "memory typo binds nothing and is rejected",
			props: map[string]any{
				"kind":   "prompt",
				"name":   "a",
				"memory": map[string]any{"stores": "s"},
			},
			wantErr: "stores",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := &azdext.ServiceConfig{Name: "a", AdditionalProperties: mustStruct(t, tt.props)}
			_, _, err := PromptAgentFromResolvedService(svc, t.TempDir())
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestPromptAgentInlineAcceptsPassThroughTools confirms the forward-compatible
// fields stay forward-compatible: a tool type newer than this build must not be
// rejected by the strict pass.
func TestPromptAgentInlineAcceptsPassThroughTools(t *testing.T) {
	t.Parallel()

	svc := &azdext.ServiceConfig{
		Name: "a",
		AdditionalProperties: mustStruct(t, map[string]any{
			"kind":  "prompt",
			"name":  "a",
			"model": "gpt-4.1-mini",
			"tools": []any{map[string]any{"type": "some_future_tool_preview", "unknown": true}},
		}),
	}

	got, found, err := PromptAgentFromResolvedService(svc, t.TempDir())
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, got.Tools, 1)
}

// TestResolvePromptAgentSettingsWithoutConfigBlock is the regression test for
// removing the promptAgent block from scaffolding: the harness target must
// resolve from the azd environment alone.
func TestResolvePromptAgentSettingsWithoutConfigBlock(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"AZURE_SUBSCRIPTION_ID":    "sub-1",
		"AZURE_RESOURCE_GROUP":     "rg-1",
		"FOUNDRY_PROJECT_ENDPOINT": "https://proj.services.ai.azure.com/api/projects/p",
	}

	settings, err := ResolvePromptAgentSettings(nil, env)
	require.NoError(t, err)
	require.Equal(t, "sub-1", settings.SubscriptionID)
	require.Equal(t, "rg-1", settings.ResourceGroup)
	require.Equal(t, "https://proj.services.ai.azure.com/api/projects/p", settings.ProjectEndpoint)
}

// TestResolvePromptAgentSettingsConfigBlockWins confirms a hand-authored block
// still overrides the environment, which is what keeps it useful as an escape
// hatch for the advanced knobs the environment does not carry.
func TestResolvePromptAgentSettingsConfigBlockWins(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"AZURE_SUBSCRIPTION_ID":    "sub-from-env",
		"AZURE_RESOURCE_GROUP":     "rg-from-env",
		"FOUNDRY_PROJECT_ENDPOINT": "https://proj.services.ai.azure.com/api/projects/p",
	}
	configured := &PromptAgentSettings{
		ResourceGroup: "rg-pinned",
		APIVersion:    "2099-01-01",
	}

	settings, err := ResolvePromptAgentSettings(configured, env)
	require.NoError(t, err)
	require.Equal(t, "rg-pinned", settings.ResourceGroup, "authored value wins")
	require.Equal(t, "sub-from-env", settings.SubscriptionID, "unset fields still fall back to the environment")
	require.Equal(t, "2099-01-01", settings.APIVersion)
}

// TestResolvePromptAgentSettingsExpandsLegacyRefs confirms projects scaffolded
// before the block was removed — whose every field is a ${VAR} reference — keep
// resolving to the same values.
func TestResolvePromptAgentSettingsExpandsLegacyRefs(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"AZURE_SUBSCRIPTION_ID":    "sub-1",
		"AZURE_RESOURCE_GROUP":     "rg-1",
		"FOUNDRY_PROJECT_ENDPOINT": "https://proj.services.ai.azure.com/api/projects/p",
	}
	legacy := &PromptAgentSettings{
		SubscriptionID:  "${AZURE_SUBSCRIPTION_ID}",
		ResourceGroup:   "${AZURE_RESOURCE_GROUP}",
		ProjectEndpoint: "${FOUNDRY_PROJECT_ENDPOINT}",
	}

	settings, err := ResolvePromptAgentSettings(legacy, env)
	require.NoError(t, err)
	require.Equal(t, "sub-1", settings.SubscriptionID)
	require.Equal(t, "rg-1", settings.ResourceGroup)
	require.Equal(t, env["FOUNDRY_PROJECT_ENDPOINT"], settings.ProjectEndpoint)
}

// TestServiceIsPromptAgent covers both the inline marker and the pre-inline
// promptAgent block, since projects scaffolded before this change declare no
// kind on the service entry.
func TestServiceIsPromptAgent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		svc  *azdext.ServiceConfig
		want bool
	}{
		{
			name: "inline prompt definition",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"kind": "prompt", "name": "a"}),
			},
			want: true,
		},
		{
			name: "inline hosted definition",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"kind": "hosted", "name": "a"}),
			},
			want: false,
		},
		{
			name: "inline voice definition",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"kind": "prompt-voice", "name": "a"}),
			},
			want: false,
		},
		{
			name: "pre-inline promptAgent block",
			svc: &azdext.ServiceConfig{
				Config: mustStruct(t, map[string]any{
					"promptAgent": map[string]any{"workspace": "ws"},
				}),
			},
			want: true,
		},
		{
			name: "no definition and no block",
			svc:  &azdext.ServiceConfig{},
			want: false,
		},
		{
			name: "nil service",
			svc:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, ServiceIsPromptAgent(tt.svc))
		})
	}
}
