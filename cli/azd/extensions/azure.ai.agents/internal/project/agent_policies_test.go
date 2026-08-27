// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"maps"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// raiPolicyID is a representative RAI policy ARM resource ID, the value users
// put in `rai_policy_name`.
const raiPolicyID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/" +
	"my-rg/providers/Microsoft.CognitiveServices/accounts/my-account/raiPolicies/Microsoft.DefaultV2"

// inlineAgentService builds an azure.ai.agent service entry whose inline
// (service-level) properties are exactly what a user would author under the
// unified azure.yaml shape.
func inlineAgentService(t *testing.T, values map[string]any) *azdext.ServiceConfig {
	t.Helper()

	props, err := structpb.NewStruct(values)
	require.NoError(t, err)

	return &azdext.ServiceConfig{
		Name:                 "rai-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
	}
}

// TestAgentPoliciesRoundTrip verifies governance policies survive a marshal into
// the inline service properties and back, and that they are persisted under the
// `rai_policy_name` key the service, agent.yaml and azure.yaml all share.
func TestAgentPoliciesRoundTrip(t *testing.T) {
	t.Parallel()

	ca := sampleContainerAgent()
	ca.Policies = []agent_yaml.Policy{
		{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: raiPolicyID},
	}

	props, err := AgentDefinitionToServiceProperties(ca, nil)
	require.NoError(t, err)

	policies := props.GetFields()["policies"].GetListValue().GetValues()
	require.Len(t, policies, 1)
	policy := policies[0].GetStructValue().GetFields()
	require.Equal(t, "rai_policy", policy["type"].GetStringValue())
	require.Equal(t, raiPolicyID, policy["rai_policy_name"].GetStringValue())
	require.NotContains(t, policy, "raiPolicyName",
		"azure.yaml uses the same rai_policy_name key as the service")

	svc := &azdext.ServiceConfig{
		Name:                 "rai-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
	}

	got, isHosted, found, source, err := AgentDefinitionFromService(svc)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, isHosted)
	require.Equal(t, AgentDefinitionSourceInline, source)
	require.Equal(t, ca.Policies, got.Policies)
}

// TestAgentPoliciesReachRaiConfig is the end-to-end regression test for
// https://github.com/Azure/azure-dev/issues/8709: a `policies` entry authored on
// the unified azure.yaml service entry must reach the Foundry data plane as
// `rai_config.rai_policy_name`. It covers both deploy modes, mirroring the two
// mapRaiConfig call sites in agent_yaml/map.go.
func TestAgentPoliciesReachRaiConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		extra   map[string]any
		options []agent_yaml.AgentBuildOption
	}{
		{
			name:    "container deploy",
			options: []agent_yaml.AgentBuildOption{agent_yaml.WithImageURL("myregistry.azurecr.io/img:v1")},
		},
		{
			name: "code deploy",
			extra: map[string]any{
				"codeConfiguration": map[string]any{
					"runtime":    "python_3_12",
					"entryPoint": "agent.py",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			values := map[string]any{
				"kind": "hosted",
				"name": "rai-agent",
				"policies": []any{
					map[string]any{
						"type":            "rai_policy",
						"rai_policy_name": raiPolicyID,
					},
				},
			}
			maps.Copy(values, test.extra)

			agentDef, isHosted, found, _, err := AgentDefinitionFromService(
				inlineAgentService(t, values),
			)
			require.NoError(t, err)
			require.True(t, found)
			require.True(t, isHosted)

			request, err := agent_yaml.CreateAgentAPIRequestFromDefinition(agentDef, test.options...)
			require.NoError(t, err)

			definition, ok := request.Definition.(agent_api.HostedAgentDefinition)
			require.True(t, ok)
			require.NotNil(t, definition.RaiConfig)
			require.Equal(t, raiPolicyID, definition.RaiConfig.RaiPolicyName)
		})
	}
}

// TestAgentPoliciesNoRaiConfigWhenAbsent verifies an agent without policies
// sends no rai_config, so existing projects are unaffected.
func TestAgentPoliciesNoRaiConfigWhenAbsent(t *testing.T) {
	t.Parallel()

	agentDef, _, found, _, err := AgentDefinitionFromService(inlineAgentService(t, map[string]any{
		"kind": "hosted",
		"name": "rai-agent",
	}))
	require.NoError(t, err)
	require.True(t, found)

	request, err := agent_yaml.CreateAgentAPIRequestFromDefinition(
		agentDef,
		agent_yaml.WithImageURL("myregistry.azurecr.io/img:v1"),
	)
	require.NoError(t, err)

	definition, ok := request.Definition.(agent_api.HostedAgentDefinition)
	require.True(t, ok)
	require.Nil(t, definition.RaiConfig)
}

// TestAgentPoliciesValidation verifies malformed policies authored inline in
// azure.yaml are rejected, and that the missing-name error names the
// `rai_policy_name` key.
func TestAgentPoliciesValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		policy       map[string]any
		wantErrSubst string
	}{
		{
			name:         "missing policy name",
			policy:       map[string]any{"type": "rai_policy"},
			wantErrSubst: "requires a policy name ('rai_policy_name')",
		},
		{
			name:         "missing type",
			policy:       map[string]any{"rai_policy_name": raiPolicyID},
			wantErrSubst: "policies[0] requires a type",
		},
		{
			name:         "unsupported type",
			policy:       map[string]any{"type": "network_policy"},
			wantErrSubst: "policies[0] has an unsupported type 'network_policy'",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, _, _, _, err := AgentDefinitionFromService(inlineAgentService(t, map[string]any{
				"kind":     "hosted",
				"name":     "rai-agent",
				"policies": []any{test.policy},
			}))
			require.ErrorContains(t, err, test.wantErrSubst)
		})
	}
}

// sampleInvocationsModeration is a fully-populated moderation block covering both
// the buffered and streaming output paths.
func sampleInvocationsModeration() *agent_yaml.InvocationsModeration {
	return &agent_yaml.InvocationsModeration{
		InputContentType:  agent_yaml.InvocationContentTypeJSON,
		OutputContentType: agent_yaml.InvocationContentTypeJSON,
		ResponseMode:      agent_yaml.InvocationResponseModeBoth,
		InputPaths:        []string{"$.input"},
		OutputPaths:       []string{"$.output"},
		StreamSelectors: []agent_yaml.SseTextSelector{
			{EventType: "response.output_text.delta", TextField: "$.delta"},
		},
	}
}

// TestAgentPoliciesInvocationsModerationRoundTrip verifies the nested moderation
// block survives the inline azure.yaml service-property marshal and back, and is
// persisted under camelCase keys like the rest of the unified azure.yaml shape.
func TestAgentPoliciesInvocationsModerationRoundTrip(t *testing.T) {
	t.Parallel()

	ca := sampleContainerAgent()
	ca.Protocols = []agent_yaml.ProtocolVersionRecord{
		{Protocol: agent_yaml.InvocationsProtocol, Version: "1.0.0"},
	}
	ca.Policies = []agent_yaml.Policy{
		{
			Type:                  agent_yaml.PolicyTypeRai,
			RaiPolicyName:         raiPolicyID,
			InvocationsModeration: sampleInvocationsModeration(),
		},
	}

	props, err := AgentDefinitionToServiceProperties(ca, nil)
	require.NoError(t, err)

	policy := props.GetFields()["policies"].GetListValue().GetValues()[0].GetStructValue().GetFields()
	moderation := policy["invocationsModeration"].GetStructValue().GetFields()
	require.NotEmpty(t, moderation, "invocationsModeration must survive the inline marshal")
	require.Equal(t, "both", moderation["responseMode"].GetStringValue())
	require.NotContains(t, moderation, "response_mode",
		"azure.yaml uses camelCase keys")

	selector := moderation["streamSelectors"].GetListValue().GetValues()[0].GetStructValue().GetFields()
	require.Equal(t, "response.output_text.delta", selector["eventType"].GetStringValue())
	require.NotContains(t, selector, "event_type")

	svc := &azdext.ServiceConfig{
		Name:                 "rai-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
	}

	got, isHosted, found, _, err := AgentDefinitionFromService(svc)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, isHosted)
	require.Equal(t, ca.Policies, got.Policies)
}

// TestAgentPoliciesInvocationsModerationReachesRaiConfig is the end-to-end check
// that a moderation block authored inline in azure.yaml reaches the Foundry data
// plane as snake_case `rai_config.invocations_moderation`.
func TestAgentPoliciesInvocationsModerationReachesRaiConfig(t *testing.T) {
	t.Parallel()

	agentDef, isHosted, found, _, err := AgentDefinitionFromService(inlineAgentService(t, map[string]any{
		"kind":      "hosted",
		"name":      "rai-agent",
		"protocols": []any{map[string]any{"protocol": "invocations", "version": "1.0.0"}},
		"policies": []any{
			map[string]any{
				"type":            "rai_policy",
				"rai_policy_name": raiPolicyID,
				"invocationsModeration": map[string]any{
					"responseMode": "non_streaming",
					"inputPaths":   []any{"$.input"},
					"outputPaths":  []any{"$.output"},
				},
			},
		},
	}))
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, isHosted)

	request, err := agent_yaml.CreateAgentAPIRequestFromDefinition(
		agentDef, agent_yaml.WithImageURL("myregistry.azurecr.io/img:v1"))
	require.NoError(t, err)

	definition, ok := request.Definition.(agent_api.HostedAgentDefinition)
	require.True(t, ok)
	require.NotNil(t, definition.RaiConfig)
	require.NotNil(t, definition.RaiConfig.InvocationsModeration)
	require.Equal(t, raiPolicyID, definition.RaiConfig.RaiPolicyName)
	require.Equal(t, "non_streaming", string(definition.RaiConfig.InvocationsModeration.ResponseMode))
	require.Equal(t, []string{"$.input"}, definition.RaiConfig.InvocationsModeration.InputPaths)
	require.Equal(t, []string{"$.output"}, definition.RaiConfig.InvocationsModeration.OutputPaths)
}

// TestAgentPoliciesInvocationsModerationInlineValidation verifies the new
// validation rules fire for blocks authored inline in azure.yaml, not just for
// the deprecated on-disk agent.yaml.
func TestAgentPoliciesInvocationsModerationInlineValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		protocols    []any
		moderation   map[string]any
		wantErrSubst string
	}{
		{
			name:      "protocol not exposed",
			protocols: []any{map[string]any{"protocol": "responses", "version": "1.0.0"}},
			moderation: map[string]any{
				"responseMode": "non_streaming",
				"inputPaths":   []any{"$.input"},
				"outputPaths":  []any{"$.output"},
			},
			wantErrSubst: "only supported for agents that expose the 'invocations' protocol",
		},
		{
			name:      "missing response mode",
			protocols: []any{map[string]any{"protocol": "invocations", "version": "1.0.0"}},
			moderation: map[string]any{
				"inputPaths":  []any{"$.input"},
				"outputPaths": []any{"$.output"},
			},
			wantErrSubst: "policies[0] invocationsModeration.responseMode",
		},
		{
			name:      "missing stream selectors",
			protocols: []any{map[string]any{"protocol": "invocations", "version": "1.0.0"}},
			moderation: map[string]any{
				"responseMode": "streaming",
				"inputPaths":   []any{"$.input"},
			},
			wantErrSubst: "streamSelectors is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, _, _, _, err := AgentDefinitionFromService(inlineAgentService(t, map[string]any{
				"kind":      "hosted",
				"name":      "rai-agent",
				"protocols": test.protocols,
				"policies": []any{
					map[string]any{
						"type":                  "rai_policy",
						"rai_policy_name":       raiPolicyID,
						"invocationsModeration": test.moderation,
					},
				},
			}))
			require.ErrorContains(t, err, test.wantErrSubst)
		})
	}
}

// TestAgentPoliciesInvocationsModerationNonHostedInline covers the production entry point for
// the non-hosted kinds. Those services are validated from the raw inline property map, so the
// validator sees the camelCase keys the user authored rather than the snake_case YAML tags —
// a block reaching the service would be dropped instead of enforced.
func TestAgentPoliciesInvocationsModerationNonHostedInline(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"workflow", "prompt-voice"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			_, _, _, _, err := AgentDefinitionFromService(inlineAgentService(t, map[string]any{
				"kind": kind,
				"name": "rai-agent",
				"policies": []any{
					map[string]any{
						"type":            "rai_policy",
						"rai_policy_name": raiPolicyID,
						"invocationsModeration": map[string]any{
							"responseMode": "non_streaming",
							"inputPaths":   []any{"$.input"},
							"outputPaths":  []any{"$.output"},
						},
					},
				},
			}))
			require.ErrorContains(t, err, "invocationsModeration is only supported for 'hosted' agents")
		})
	}
}

// TestAgentPoliciesSingleRaiPolicyInline pins the one-policy rule on the inline shape, where a
// second rai_policy would otherwise validate cleanly and then be dropped by the mapper.
func TestAgentPoliciesSingleRaiPolicyInline(t *testing.T) {
	t.Parallel()

	_, _, _, _, err := AgentDefinitionFromService(inlineAgentService(t, map[string]any{
		"kind":  "hosted",
		"name":  "rai-agent",
		"image": "myregistry.azurecr.io/agent:v1",
		"policies": []any{
			map[string]any{"type": "rai_policy", "rai_policy_name": raiPolicyID},
			map[string]any{"type": "rai_policy", "rai_policy_name": raiPolicyID + "-2"},
		},
	}))
	require.ErrorContains(t, err, "only one is supported")
}

// TestAgentPoliciesLegacyRaiPolicyNameKey covers azure.yaml files written before
// the key was aligned with the service: inline entries used to be marshalled
// through the camelCase JSON tag, so `raiPolicyName` must keep deploying.
func TestAgentPoliciesLegacyRaiPolicyNameKey(t *testing.T) {
	t.Parallel()

	agentDef, _, found, _, err := AgentDefinitionFromService(inlineAgentService(t, map[string]any{
		"kind":  "hosted",
		"name":  "rai-agent",
		"image": "myregistry.azurecr.io/agent:v1",
		"policies": []any{
			map[string]any{"type": "rai_policy", "raiPolicyName": raiPolicyID},
		},
	}))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, raiPolicyID, agentDef.Policies[0].RaiPolicyName)
}
