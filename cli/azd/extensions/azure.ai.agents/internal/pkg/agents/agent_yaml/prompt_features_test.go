// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"encoding/json"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/require"
	yaml "go.yaml.in/yaml/v3"
)

// testRaiPolicyID is a syntactically valid RAI policy ARM resource ID. The
// service (and now azd) rejects a bare policy name, so fixtures have to carry
// the full ID.
const testRaiPolicyID = "/subscriptions/sub/resourceGroups/rg/providers/" +
	"Microsoft.CognitiveServices/accounts/acct/raiPolicies/strict"

// TestPromptAgent_MemoryRoundTrip verifies the memory block decodes into its
// typed shape. Memory is not passthrough — azd has to read the store name and
// models to provision the store — so the field names must bind, not just parse.
func TestPromptAgent_MemoryRoundTrip(t *testing.T) {
	t.Parallel()

	content := []byte(`
kind: prompt
name: full-featured
model: gpt-4.1-mini
instructions: Be helpful.
memory:
  store: support-memory
  chat_model: gpt-4.1-mini
  embedding_model: text-embedding-3-small
  scope: user_123
  update_delay: 300
  max_memories: 5
  options:
    user_profile_enabled: true
    chat_summary_enabled: false
`)

	var agent PromptAgent
	require.NoError(t, yaml.Unmarshal(content, &agent))

	require.NotNil(t, agent.Memory)
	require.Equal(t, "support-memory", agent.Memory.Store)
	require.Equal(t, "gpt-4.1-mini", agent.Memory.ChatModel)
	require.Equal(t, "text-embedding-3-small", agent.Memory.EmbeddingModel)
	require.Equal(t, "user_123", agent.Memory.Scope)
	require.Equal(t, 300, *agent.Memory.UpdateDelay)
	require.Equal(t, 5, *agent.Memory.MaxMemories)

	require.NotNil(t, agent.Memory.Options)
	require.True(t, *agent.Memory.Options.UserProfileEnabled)
	// Explicit false must survive as false rather than collapsing into "unset",
	// which is why the option toggles are pointers.
	require.False(t, *agent.Memory.Options.ChatSummaryEnabled)
	require.Nil(t, agent.Memory.Options.ProceduralMemoryEnabled)
}

// TestPromptAgent_ValidateHarness keeps harness names open-ended so service
// additions do not require an azd release.
func TestPromptAgent_ValidateHarness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		harness string
	}{
		{name: "absent harness is a plain prompt agent", harness: ""},
		{name: "whitespace is treated as absent", harness: "   "},
		{name: "current spelling is accepted", harness: "github_copilot_preview"},
		{
			name:    "unknown harness is left to the service",
			harness: "some-future-harness",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.NoError(t, PromptAgent{Harness: NewPromptHarness(tc.harness)}.ValidateHarness())
		})
	}
}

// TestCreatePromptAgentAPIRequest_FeatureCarriers verifies each capability
// reaches the payload through its real carrier — and, critically, that `memory`
// is NOT emitted as a top-level field. The prompt-agent API defines no such
// field, so sending one would be silently dropped by the service and the agent
// would deploy "successfully" with no memory at all.
func TestCreatePromptAgentAPIRequest_FeatureCarriers(t *testing.T) {
	t.Parallel()

	base := PromptAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPrompt, Name: "a"},
		Model:           "gpt-4.1-mini",
		Instructions:    "Be helpful.",
		Memory:          &PromptMemory{Store: "s", ChatModel: "c", EmbeddingModel: "e"},
		Policies:        []Policy{{Type: PolicyTypeRai, RaiPolicyName: testRaiPolicyID}},
		Tools: []any{
			map[string]any{"type": "azure_ai_search", "index_name": "handbook"},
		},
	}

	req, err := CreatePromptAgentAPIRequest(base, nil)
	require.NoError(t, err)

	def, ok := req.Definition.(agent_api.ManagedAgentDefinition)
	require.True(t, ok, "definition: got %T", req.Definition)

	// Guardrails ride on rai_config.
	require.NotNil(t, def.RaiConfig)
	require.Equal(t, testRaiPolicyID, def.RaiConfig.RaiPolicyName)

	// Knowledge rides on tools, forwarded verbatim.
	require.Equal(t, base.Tools, def.Tools)

	// Memory must not leak into the payload as a field of its own. The deploy
	// graph injects a memory_search_preview tool instead.
	encoded, err := json.Marshal(def)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(encoded, &payload))
	require.NotContains(t, payload, "memory")
	require.NotContains(t, payload, "guardrails")
	require.NotContains(t, payload, "knowledge")
}

// TestValidateRaiPolicyName covers the bare-name mistake that the service
// reports as "invalid or does not exist" — a message that sends authors looking
// for a missing policy when the value's shape is the actual problem.
func TestValidateRaiPolicyName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		policy  string
		wantErr bool
	}{
		{name: "empty is left to the required-field check", policy: "", wantErr: false},
		{name: "whitespace only", policy: "   ", wantErr: false},
		{name: "full arm id", policy: testRaiPolicyID, wantErr: false},
		{name: "incomplete arm id", policy: "/subscriptions/s/raiPolicies/p", wantErr: true},
		{name: "surrounding whitespace tolerated", policy: "  " + testRaiPolicyID + "  ", wantErr: false},
		// The scaffold writes ${RAI_POLICY_ID}; the concrete ID is substituted
		// on the deploy path and re-validated there.
		{name: "unexpanded reference is deferred", policy: "${RAI_POLICY_ID}", wantErr: false},
		{name: "reference embedded in a path is deferred", policy: "/subscriptions/${SUB}/x", wantErr: false},
		{name: "bare built-in name", policy: "Microsoft.DefaultV2", wantErr: true},
		{name: "bare custom name", policy: "strict", wantErr: true},
		{name: "missing raiPolicies segment", policy: "/subscriptions/s/resourceGroups/rg", wantErr: true},
		{name: "missing subscriptions prefix", policy: "/resourceGroups/rg/raiPolicies/p", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateRaiPolicyName(tt.policy)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "full ARM resource ID")
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestPromptAgent_ValidatePolicies checks the per-entry wiring: the index is
// reported, and a policy of another type is not held to the RAI rule.
func TestPromptAgent_ValidatePolicies(t *testing.T) {
	t.Parallel()

	agent := PromptAgent{
		Policies: []Policy{
			{Type: PolicyTypeRai, RaiPolicyName: testRaiPolicyID},
			{Type: PolicyTypeRai, RaiPolicyName: "Microsoft.DefaultV2"},
		},
	}
	err := agent.ValidatePolicies()
	require.Error(t, err)
	require.Contains(t, err.Error(), "policies[1]")

	other := PromptAgent{Policies: []Policy{{Type: "other", RaiPolicyName: "strict"}}}
	require.NoError(t, other.ValidatePolicies())
}
