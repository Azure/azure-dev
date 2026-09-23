// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"strings"
	"testing"
)

func TestValidateAgentDefinition_RaiConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		yaml         string
		wantErrSubst string
	}{
		{
			name: "valid rai_policy",
			yaml: `kind: hosted
name: rai-agent
policies:
  - type: rai_policy
    rai_policy_name: /subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/my-rg/providers/Microsoft.CognitiveServices/accounts/my-account/raiPolicies/Microsoft.DefaultV2
protocols:
  - protocol: responses
    version: "1.0.0"
`,
		},
		{
			name: "rai_policy missing policy name",
			yaml: `kind: hosted
name: rai-agent
policies:
  - type: rai_policy
protocols:
  - protocol: responses
    version: "1.0.0"
`,
			wantErrSubst: "policies[0] of type 'rai_policy' requires a policy name",
		},
		{
			name: "policy missing type",
			yaml: `kind: hosted
name: rai-agent
policies:
  - rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/p
protocols:
  - protocol: responses
    version: "1.0.0"
`,
			wantErrSubst: "policies[0] requires a type",
		},
		{
			name: "unsupported policy type",
			yaml: `kind: hosted
name: rai-agent
policies:
  - type: network_policy
protocols:
  - protocol: responses
    version: "1.0.0"
`,
			wantErrSubst: "policies[0] has an unsupported type 'network_policy'",
		},
		{
			name: "no policies",
			yaml: `kind: hosted
name: rai-agent
protocols:
  - protocol: responses
    version: "1.0.0"
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateAgentDefinition([]byte(tc.yaml))
			if tc.wantErrSubst == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSubst)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubst) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErrSubst, err.Error())
			}
		})
	}
}

func TestValidateAgentDefinition_InvocationsModeration(t *testing.T) {
	t.Parallel()

	// invocationsAgent wraps a moderation block in an otherwise-valid hosted agent that
	// exposes the invocations protocol, so each case isolates the moderation rule under test.
	// The caller's fragment must end with a newline; the guard below keeps a missing one from
	// silently nesting `protocols` under the moderation block and skewing every assertion.
	invocationsAgent := func(moderation string) string {
		if !strings.HasSuffix(moderation, "\n") {
			t.Fatalf("moderation fragment must end with a newline, got %q", moderation)
		}
		return `kind: hosted
name: rai-agent
policies:
  - type: rai_policy
    rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/p
    invocations_moderation:
` + moderation + `protocols:
  - protocol: invocations
    version: "1.0.0"
`
	}

	tests := []struct {
		name string
		yaml string
		// wantErrSubst is the substring the validation error must contain, or "" when the
		// definition is expected to validate cleanly.
		wantErrSubst string
		// notWantErrSubst, when set, must NOT appear in the error. It locks in the
		// deliberate suppression of cascading follow-on errors.
		notWantErrSubst string
	}{
		{
			name: "valid non_streaming json",
			yaml: invocationsAgent(`      response_mode: non_streaming
      input_paths: ["$.input"]
      output_paths: ["$.output"]
`),
		},
		{
			name: "valid streaming json",
			yaml: invocationsAgent(`      response_mode: streaming
      input_paths: ["$.input"]
      stream_selectors:
        - event_type: response.output_text.delta
          text_field: $.delta
`),
		},
		{
			name: "valid both requires output_paths and stream_selectors",
			yaml: invocationsAgent(`      response_mode: both
      input_paths: ["$.input"]
      output_paths: ["$.output"]
      stream_selectors:
        - event_type: response.output_text.delta
          text_field: $.delta
`),
		},
		{
			name: "valid text content types need no paths",
			yaml: invocationsAgent(`      response_mode: both
      input_content_type: text
      output_content_type: text
`),
		},
		{
			name: "response_mode is required",
			yaml: invocationsAgent(`      input_paths: ["$.input"]
`),
			wantErrSubst: "policies[0] invocationsModeration.responseMode is required",
		},
		{
			name: "response_mode must be a known value",
			yaml: invocationsAgent(`      response_mode: sometimes
      input_paths: ["$.input"]
`),
			wantErrSubst: "policies[0] invocationsModeration.responseMode must be one of",
		},
		{
			name: "input_content_type must be json or text",
			yaml: invocationsAgent(`      response_mode: non_streaming
      input_content_type: xml
      output_paths: ["$.output"]
`),
			wantErrSubst: "policies[0] invocationsModeration.inputContentType must be 'json' or 'text'",
			// An unusable content type must not also demand inputPaths: the corrected value
			// decides whether paths are needed at all.
			notWantErrSubst: "inputPaths is required",
		},
		{
			name: "output_content_type must be json or text",
			yaml: invocationsAgent(`      response_mode: non_streaming
      input_paths: ["$.input"]
      output_content_type: xml
`),
			wantErrSubst: "policies[0] invocationsModeration.outputContentType must be 'json' or 'text'",
		},
		{
			name: "input_paths required when input content type defaults to json",
			yaml: invocationsAgent(`      response_mode: non_streaming
      output_paths: ["$.output"]
`),
			wantErrSubst: "policies[0] invocationsModeration.inputPaths is required when inputContentType is 'json'",
		},
		{
			name: "output_paths required for non-streaming json",
			yaml: invocationsAgent(`      response_mode: non_streaming
      input_paths: ["$.input"]
`),
			wantErrSubst: "policies[0] invocationsModeration.outputPaths is required when responseMode " +
				"includes non-streaming",
		},
		{
			name: "stream_selectors required for streaming json",
			yaml: invocationsAgent(`      response_mode: streaming
      input_paths: ["$.input"]
`),
			wantErrSubst: "policies[0] invocationsModeration.streamSelectors is required when responseMode " +
				"includes streaming",
		},
		{
			name: "stream selector event_type must be non-empty",
			yaml: invocationsAgent(`      response_mode: streaming
      input_paths: ["$.input"]
      stream_selectors:
        - text_field: $.delta
`),
			wantErrSubst: "policies[0] invocationsModeration.streamSelectors[0].eventType is required",
		},
		{
			name: "rejected on an agent that does not expose the invocations protocol",
			yaml: `kind: hosted
name: rai-agent
policies:
  - type: rai_policy
    rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/p
    invocations_moderation:
      response_mode: non_streaming
      input_paths: ["$.input"]
      output_paths: ["$.output"]
protocols:
  - protocol: responses
    version: "1.0.0"
`,
			wantErrSubst: "policies[0] invocationsModeration is only supported for agents that expose " +
				"the 'invocations' protocol",
			// The rest of the block is irrelevant on a non-invocations agent, so the protocol
			// error must be reported alone rather than buried under field-level noise.
			notWantErrSubst: "responseMode",
		},
		{
			name: "both requires stream_selectors as well as output_paths",
			yaml: invocationsAgent(`      response_mode: both
      input_paths: ["$.input"]
      output_paths: ["$.output"]
`),
			wantErrSubst: "policies[0] invocationsModeration.streamSelectors is required when responseMode " +
				"includes streaming",
		},
		{
			name: "both requires output_paths as well as stream_selectors",
			yaml: invocationsAgent(`      response_mode: both
      input_paths: ["$.input"]
      stream_selectors:
        - event_type: response.output_text.delta
`),
			wantErrSubst: "policies[0] invocationsModeration.outputPaths is required when responseMode " +
				"includes non-streaming",
		},
		{
			name: "text output needs neither output_paths nor stream_selectors",
			yaml: invocationsAgent(`      response_mode: both
      output_content_type: text
      input_paths: ["$.input"]
`),
		},
		{
			name: "text input needs no input_paths but json output still needs its own",
			yaml: invocationsAgent(`      response_mode: non_streaming
      input_content_type: text
      output_paths: ["$.output"]
`),
		},
		{
			name: "explicit json content types behave like the defaults",
			yaml: invocationsAgent(`      response_mode: non_streaming
      input_content_type: json
      output_content_type: json
      input_paths: ["$.input"]
      output_paths: ["$.output"]
`),
		},
		{
			name: "stream selector event_type may not be whitespace only",
			yaml: invocationsAgent(`      response_mode: streaming
      input_paths: ["$.input"]
      stream_selectors:
        - event_type: "   "
`),
			wantErrSubst: "policies[0] invocationsModeration.streamSelectors[0].eventType is required",
		},
		{
			name: "each policy is validated under its own index",
			yaml: `kind: hosted
name: rai-agent
policies:
  - type: rai_policy
    rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/first
    invocations_moderation:
      response_mode: non_streaming
      input_paths: ["$.input"]
      output_paths: ["$.output"]
  - type: rai_policy
    rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/second
    invocations_moderation:
      input_paths: ["$.input"]
      output_paths: ["$.output"]
protocols:
  - protocol: invocations
    version: "1.0.0"
`,
			wantErrSubst: "policies[1] invocationsModeration.responseMode is required",
		},
		{
			name: "invocations_ws alone does not satisfy the protocol requirement",
			yaml: `kind: hosted
name: rai-agent
policies:
  - type: rai_policy
    rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/p
    invocations_moderation:
      response_mode: non_streaming
      input_paths: ["$.input"]
      output_paths: ["$.output"]
protocols:
  - protocol: invocations_ws
    version: "1.0.0"
`,
			wantErrSubst: "policies[0] invocationsModeration is only supported for agents that expose " +
				"the 'invocations' protocol",
		},
		{
			name: "omitting the block leaves an invocations agent valid",
			yaml: `kind: hosted
name: rai-agent
policies:
  - type: rai_policy
    rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/p
protocols:
  - protocol: invocations
    version: "1.0.0"
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateAgentDefinition([]byte(tc.yaml))
			if tc.wantErrSubst == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSubst)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubst) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErrSubst, err.Error())
			}
			if tc.notWantErrSubst != "" && strings.Contains(err.Error(), tc.notWantErrSubst) {
				t.Fatalf("expected error NOT to contain %q, got %q", tc.notWantErrSubst, err.Error())
			}
		})
	}
}

// TestValidateAgentDefinition_InvocationsModerationRequiresHostedKind covers the kinds that
// have no policies field of their own. Without an explicit check they would parse cleanly and
// the moderation block would be dropped on the way to the service rather than enforced.
func TestValidateAgentDefinition_InvocationsModerationRequiresHostedKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		yaml         string
		wantErrSubst string
	}{
		{
			name: "prompt-voice agent",
			yaml: `kind: prompt-voice
name: voice-agent
model:
  id: gpt-4o-realtime-preview
policies:
  - type: rai_policy
    rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/p
    invocations_moderation:
      response_mode: non_streaming
      input_paths: ["$.input"]
      output_paths: ["$.output"]
`,
			wantErrSubst: "policies[0] invocationsModeration is only supported for 'hosted' agents, " +
				"got kind 'prompt-voice'",
		},
		{
			name: "workflow agent",
			yaml: `kind: workflow
name: workflow-agent
policies:
  - type: rai_policy
    rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/p
    invocations_moderation:
      response_mode: non_streaming
      input_paths: ["$.input"]
      output_paths: ["$.output"]
`,
			wantErrSubst: "policies[0] invocationsModeration is only supported for 'hosted' agents, " +
				"got kind 'workflow'",
		},
		{
			name: "reported under the offending policy index",
			yaml: `kind: workflow
name: workflow-agent
policies:
  - type: rai_policy
    rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/first
  - type: rai_policy
    rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/second
    invocations_moderation:
      response_mode: non_streaming
      input_paths: ["$.input"]
      output_paths: ["$.output"]
`,
			wantErrSubst: "policies[1] invocationsModeration is only supported for 'hosted' agents",
		},
		{
			name: "a non-hosted agent without the block stays valid",
			yaml: `kind: workflow
name: workflow-agent
policies:
  - type: rai_policy
    rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/p
`,
		},
		{
			name: "camelCase block on a non-hosted agent",
			yaml: `kind: workflow
name: workflow-agent
policies:
  - type: rai_policy
    raiPolicyName: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/p
    invocationsModeration:
      responseMode: non_streaming
      inputPaths: ["$.input"]
      outputPaths: ["$.output"]
`,
			wantErrSubst: "policies[0] invocationsModeration is only supported for 'hosted' agents",
		},
	}

	runValidateAgentDefinitionCases(t, tests)
}

// TestValidateAgentDefinition_SingleRaiPolicy pins the one-policy rule. rai_config is a single
// object on the wire, so a second rai_policy (and any moderation block it carries) would be
// dropped by the mapper after passing validation rather than enforced.
func TestValidateAgentDefinition_SingleRaiPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		yaml         string
		wantErrSubst string
	}{
		{
			name: "a single rai policy stays valid",
			yaml: `kind: hosted
name: hosted-agent
image: myregistry.azurecr.io/agent:v1
policies:
  - type: rai_policy
    rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/p1
`,
		},
		{
			name: "two rai policies are rejected",
			yaml: `kind: hosted
name: hosted-agent
image: myregistry.azurecr.io/agent:v1
policies:
  - type: rai_policy
    rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/p1
  - type: rai_policy
    rai_policy_name: /subscriptions/x/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/p2
`,
			wantErrSubst: "policies declares 2 policies of type 'rai_policy', but only one is supported",
		},
	}

	runValidateAgentDefinitionCases(t, tests)
}

// runValidateAgentDefinitionCases runs a table of definitions through ValidateAgentDefinition,
// asserting either success or that the error mentions the expected substring.
func runValidateAgentDefinitionCases(t *testing.T, tests []struct {
	name         string
	yaml         string
	wantErrSubst string
},
) {
	t.Helper()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateAgentDefinition([]byte(tc.yaml))
			if tc.wantErrSubst == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSubst)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubst) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErrSubst, err.Error())
			}
		})
	}
}
