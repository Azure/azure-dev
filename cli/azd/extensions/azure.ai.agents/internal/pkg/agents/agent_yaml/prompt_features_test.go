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

// TestPromptAgent_Declares verifies each capability is detected from the
// primitive that actually carries it, since none of them is a field of its own.
func TestPromptAgent_Declares(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		agent   PromptAgent
		feature PromptFeature
		want    bool
	}{
		{
			name:    "memory block",
			agent:   PromptAgent{Memory: &PromptMemory{Store: "s"}},
			feature: PromptFeatureMemory,
			want:    true,
		},
		{
			name:    "no memory block",
			agent:   PromptAgent{},
			feature: PromptFeatureMemory,
			want:    false,
		},
		{
			name: "rai policy is a guardrail",
			agent: PromptAgent{
				Policies: []Policy{{Type: PolicyTypeRai, RaiPolicyName: testRaiPolicyID}},
			},
			feature: PromptFeatureGuardrails,
			want:    true,
		},
		{
			// A policy entry with no name maps to no rai_config, so it configures
			// nothing and must not read as a guardrail.
			name:    "rai policy without a name is not a guardrail",
			agent:   PromptAgent{Policies: []Policy{{Type: PolicyTypeRai}}},
			feature: PromptFeatureGuardrails,
			want:    false,
		},
		{
			name: "file_search is knowledge",
			agent: PromptAgent{
				Tools: []any{map[string]any{"type": "file_search"}},
			},
			feature: PromptFeatureKnowledge,
			want:    true,
		},
		{
			name: "azure_ai_search is knowledge",
			agent: PromptAgent{
				Tools: []any{map[string]any{"type": "azure_ai_search"}},
			},
			feature: PromptFeatureKnowledge,
			want:    true,
		},
		{
			name: "a non-grounding tool is not knowledge",
			agent: PromptAgent{
				Tools: []any{map[string]any{"type": "code_interpreter"}},
			},
			feature: PromptFeatureKnowledge,
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.agent.declares(tc.feature))
		})
	}
}

// TestPromptAgent_ValidateHarness pins the removed-value check. The list is
// deliberately a rejection list rather than an allowlist: a harness the service
// adds after this build shipped must keep working, so only spellings we know
// were withdrawn are refused.
func TestPromptAgent_ValidateHarness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		harness     string
		wantErrPart string
	}{
		{name: "absent harness is a plain prompt agent", harness: ""},
		{name: "whitespace is treated as absent", harness: "   "},
		{name: "current spelling is accepted", harness: "github-copilot"},
		{
			name:        "removed spelling names its replacement",
			harness:     "ghcp",
			wantErrPart: "github-copilot",
		},
		{
			name:    "unknown harness is left to the service",
			harness: "some-future-harness",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := PromptAgent{Harness: tc.harness}.ValidateHarness()
			if tc.wantErrPart == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErrPart)
		})
	}
}

// TestValidateHarnessFeatures verifies the harness capability switch. Only
// guardrails is enabled for harnessed agents -- the harness spec documents RAI
// policy attachment but puts grounding out of scope and never describes memory
// -- so a harnessed agent declaring memory or knowledge is rejected up front
// rather than deploying with the capability silently dropped. The test pins
// each entry so flipping one in harnessedPromptFeatures is a deliberate,
// visible change.
func TestValidateHarnessFeatures(t *testing.T) {
	t.Parallel()

	fullyFeatured := PromptAgent{
		Memory:   &PromptMemory{Store: "s"},
		Policies: []Policy{{Type: PolicyTypeRai, RaiPolicyName: testRaiPolicyID}},
		Tools:    []any{map[string]any{"type": "file_search"}},
	}

	tests := []struct {
		name         string
		harness      string
		agent        PromptAgent
		wantRejected []PromptFeature
	}{
		{
			name:  "harness-less agent accepts every capability",
			agent: fullyFeatured,
		},
		{
			name:    "harnessed agent without capabilities is fine",
			harness: "github-copilot",
			agent:   PromptAgent{},
		},
		{
			name:    "harnessed agent accepts guardrails",
			harness: "github-copilot",
			agent: PromptAgent{
				Policies: []Policy{{Type: PolicyTypeRai, RaiPolicyName: testRaiPolicyID}},
			},
		},
		{
			name:         "harnessed agent rejects memory",
			harness:      "github-copilot",
			agent:        PromptAgent{Memory: &PromptMemory{Store: "s"}},
			wantRejected: []PromptFeature{PromptFeatureMemory},
		},
		{
			name:         "harnessed agent rejects knowledge",
			harness:      "github-copilot",
			agent:        PromptAgent{Tools: []any{map[string]any{"type": "file_search"}}},
			wantRejected: []PromptFeature{PromptFeatureKnowledge},
		},
		{
			name:         "harnessed agent reports memory and knowledge together",
			harness:      "github-copilot",
			agent:        fullyFeatured,
			wantRejected: []PromptFeature{PromptFeatureMemory, PromptFeatureKnowledge},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			agent := tc.agent
			agent.Harness = tc.harness

			if len(tc.wantRejected) == 0 {
				require.NoError(t, agent.ValidateHarnessFeatures())
				require.Empty(t, agent.UnsupportedHarnessFeatures())
				return
			}
			require.Error(t, agent.ValidateHarnessFeatures())
			require.Equal(t, tc.wantRejected, agent.UnsupportedHarnessFeatures())
		})
	}
}

// TestUnsupportedHarnessFeatures_ReportingOrder verifies that when a capability
// is disabled the report is deterministic and harness-less agents stay exempt.
// It drives the switch directly rather than relying on the shipped values, so
// the ordering guarantee holds whichever entries are enabled.
func TestUnsupportedHarnessFeatures_ReportingOrder(t *testing.T) {
	// Not parallel: this mutates the package-level switch.
	original := harnessedPromptFeatures
	t.Cleanup(func() { harnessedPromptFeatures = original })

	harnessedPromptFeatures = map[PromptFeature]bool{
		PromptFeatureMemory:     false,
		PromptFeatureGuardrails: false,
		PromptFeatureKnowledge:  false,
	}

	agent := PromptAgent{
		Memory:   &PromptMemory{Store: "s"},
		Policies: []Policy{{Type: PolicyTypeRai, RaiPolicyName: testRaiPolicyID}},
		Tools:    []any{map[string]any{"type": "file_search"}},
	}

	// A harness-less agent is never gated, whatever the switch says.
	require.NoError(t, agent.ValidateHarnessFeatures())

	agent.Harness = "github-copilot"
	err := agent.ValidateHarnessFeatures()
	require.Error(t, err)
	require.Contains(t, err.Error(), "github-copilot")

	names := make([]string, 0, 3)
	for _, feature := range agent.UnsupportedHarnessFeatures() {
		names = append(names, string(feature))
	}
	// Order is asserted, not just membership: promptFeatureOrder exists so
	// repeated runs cannot produce differently-ordered messages.
	require.Equal(t, []string{"memory", "guardrails", "knowledge"}, names)
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
		{name: "short but well formed arm id", policy: "/subscriptions/s/raiPolicies/p", wantErr: false},
		{name: "surrounding whitespace tolerated", policy: "  " + testRaiPolicyID + "  ", wantErr: false},
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
