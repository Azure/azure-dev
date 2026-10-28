// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"encoding/json"
	"fmt"
	"maps"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestCreateGPTLiveAgentAPIRequest_ResponsesDelegation(t *testing.T) {
	t.Parallel()
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "gpt-live"},
		SubProtocol:     VoiceSubProtocolLive,
		ModelType:       VoiceModelTypeSelfDeployed,
		Model:           &Model{Id: "gpt-live-1"},
		Instructions:    new("Teach English."),
		Store:           new(false),
		Audio: &VoiceAudio{
			Format: &VoiceAudioFormat{Type: "audio/pcm", Rate: new(24000)},
			Output: &VoiceAudioOutput{Voice: &VoiceConfig{LiveName: "marin"}},
		},
		Greeting: map[string]any{"type": "template", "text": "Hello {{name}}"},
		StructuredInputs: map[string]any{
			"name":     map[string]any{"required": true, "defaultValue": "Ada"},
			"nullable": map[string]any{"defaultValue": nil},
			"scalar":   map[string]any{"defaultValue": 42},
			"array":    map[string]any{"defaultValue": []any{"a", 2}},
			"object":   map[string]any{"defaultValue": map[string]any{"enabled": true}},
		},
		Delegation: &LiveVoiceDelegation{
			Type: "responses",
			Responses: &LiveResponsesDelegation{
				Model: "gpt-5.6-terra", MaxOutputTokens: new(512),
				Reasoning: &LiveResponsesReasoning{Effort: new("low"), Summary: new("auto")},
				Text:      &LiveResponsesText{Verbosity: new("low")},
			},
		},
	}
	req, err := CreateVoiceAgentAPIRequest(agent)
	require.NoError(t, err)
	require.Equal(t, "gpt-live", req.Name)
	def := req.Definition.(agent_api.VoiceAgentDefinition)
	require.Equal(t, "live", def.SubProtocol)
	require.Equal(t, agent_api.VoiceModelTypeSelfDeployed, def.ModelType)
	require.Nil(t, def.Audio, "realtime audio must not leak into a Live definition")
	require.NotNil(t, def.LiveAudio)

	wire, err := json.Marshal(def)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(wire, &got))
	require.Equal(t, "live", got["sub_protocol"])
	require.Equal(t, false, got["store"])
	require.NotContains(t, got, "output_modalities")
	require.NotContains(t, got, "conversation_engine")
	audio := got["audio"].(map[string]any)
	require.Equal(t, "audio/pcm", audio["format"].(map[string]any)["type"])
	require.Equal(t, "marin", audio["output"].(map[string]any)["voice"])
	delegation := got["delegation"].(map[string]any)
	require.Equal(t, "responses", delegation["type"])
	require.Equal(t, "gpt-5.6-terra", delegation["responses"].(map[string]any)["model"])
	inputs := got["structured_inputs"].(map[string]any)
	require.Equal(t, "Ada", inputs["name"].(map[string]any)["default_value"])
	require.Contains(t, inputs["nullable"].(map[string]any), "default_value")
	require.Nil(t, inputs["nullable"].(map[string]any)["default_value"])
	require.EqualValues(t, 42, inputs["scalar"].(map[string]any)["default_value"])
	require.Equal(t, []any{"a", float64(2)}, inputs["array"].(map[string]any)["default_value"])
	require.Equal(t, true, inputs["object"].(map[string]any)["default_value"].(map[string]any)["enabled"])
}

func TestValidateGPTLiveAgent_RejectsRealtimeAndHostedFields(t *testing.T) {
	t.Parallel()
	invalid := []string{
		"model_type: managed",
		"conversation_engine:\n  type: hosted_agent\n  name: target",
		"audio:\n  input:\n    transcription:\n      model: azure-speech",
		"avatar: {type: video-avatar}",
		"telephony:\n  bindings: []",
		"output_modalities: [audio]",
	}
	for _, field := range invalid {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			yaml := "kind: voice\nname: live\nsub_protocol: live\nmodel_type: self_deployed\nmodel:\n  id: gpt-live-1\n" + field + "\n"
			require.Error(t, ValidateAgentDefinition([]byte(yaml)))
		})
	}
}

func TestValidateGPTLiveAgent_DelegationVariants(t *testing.T) {
	t.Parallel()
	for name, delegation := range map[string]string{
		"client":    "delegation:\n  type: client",
		"responses": "delegation:\n  type: responses\n  responses:\n    model: gpt-5.6-terra",
		"managed":   "delegation:\n  type: foundry_managed_agent\n  agent_name: teacher\n  agent_version: '3'",
	} {
		t.Run(name, func(t *testing.T) {
			yaml := "kind: voice\nname: live\nsub_protocol: live\nmodel_type: self_deployed\nmodel:\n  id: gpt-live-1\n" + delegation + "\n"
			require.NoError(t, ValidateAgentDefinition([]byte(yaml)))
		})
	}
}

func TestValidateGPTLiveAgent_CustomVoiceAndFormats(t *testing.T) {
	t.Parallel()
	for _, audio := range []string{
		"format: {type: audio/pcm, rate: 16000}\n  output:\n    voice: {id: custom-voice}",
		"format: {type: audio/pcmu, rate: 8000}\n  output:\n    voice: marin",
		"format: {type: audio/pcma, rate: 8000}",
	} {
		yaml := "kind: voice\nname: live\nsub_protocol: live\nmodel_type: self_deployed\nmodel:\n  id: gpt-live-1\naudio:\n  " + audio + "\n"
		require.NoError(t, ValidateAgentDefinition([]byte(yaml)))
	}
}

func TestVoiceConfigGPTLiveScalarAndCustomObjectRoundTrip(t *testing.T) {
	t.Parallel()
	for name, yamlValue := range map[string]string{
		"built-in": "marin",
		"custom":   "{id: custom-voice}",
	} {
		t.Run(name, func(t *testing.T) {
			var audio VoiceAudio
			require.NoError(t, yaml.Unmarshal([]byte("output:\n  voice: "+yamlValue+"\n"), &audio))
			require.NotNil(t, audio.Output.Voice)
			encoded, err := yaml.Marshal(audio)
			require.NoError(t, err)
			var roundTrip VoiceAudio
			require.NoError(t, yaml.Unmarshal(encoded, &roundTrip))
			require.Equal(t, audio.Output.Voice.LiveName, roundTrip.Output.Voice.LiveName)
			require.Equal(t, audio.Output.Voice.ID, roundTrip.Output.Voice.ID)
		})
	}
}

func TestValidateGPTLiveAgent_RejectsInvalidNativeValues(t *testing.T) {
	t.Parallel()
	for i, suffix := range []string{
		"audio:\n  format: {type: audio/pcm, rate: 8000}",
		"audio:\n  format: {type: audio/pcmu, rate: 24000}",
		"audio:\n  output:\n    voice: {id: voice, extra: no}",
		"audio:\n  output:\n    voice: {id: 123}",
		"delegation:\n  type: foundry_managed_agent\n  agent_name: ../other",
		"delegation:\n  type: foundry_managed_agent\n  agent_name: teacher\n  agent_version: '0'",
		"greeting:\n  type: llm_generated\n  text: hello",
		"greeting:\n  type: template",
		"greeting:\n  type: template\n  text: null",
		"greeting:\n  type: template\n  text: 123",
		"greeting:\n  type: template\n  text: '   '",
		"greeting:\n  type: 123\n  text: hello",
	} {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			yaml := "kind: voice\nname: live\nsub_protocol: live\nmodel_type: self_deployed\nmodel:\n  id: gpt-live-1\n" + suffix + "\n"
			require.Error(t, ValidateAgentDefinition([]byte(yaml)), suffix)
		})
	}
}

func TestValidateGPTLiveAgent_RejectsRawForbiddenProperties(t *testing.T) {
	t.Parallel()
	for name, suffix := range map[string]string{
		"root reasoning":          "reasoning: {effort: low}",
		"root client":             "client: {}",
		"root input":              "input: {}",
		"root reasoning effort":   "reasoning_effort: low",
		"root soundscape":         "soundscape: {}",
		"audio output volume":     "audio:\n  output:\n    volume: loud",
		"reasoning temperature":   "delegation:\n  type: responses\n  responses:\n    model: terra\n    reasoning:\n      temperature: 0.5",
		"model provider":          "model: {id: gpt-live-1, provider: custom}",
		"policy extra":            "policies:\n  - type: rai_policy\n    rai_policy_name: policy\n    extra: no",
		"structured input scalar": "structured_inputs:\n  learner: Ada",
		"structured description":  "structured_inputs:\n  learner: {description: 1}",
		"structured schema":       "structured_inputs:\n  learner: {schema: []}",
		"structured required":     "structured_inputs:\n  learner: {required: yes}",
		"empty live config":       "live_config: {}",
		"empty tools":             "tools: []",
		"empty avatar":            "avatar: {}",
		"empty greeting":          "greeting: {}",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			definition := "kind: voice\nname: live\nsub_protocol: live\nmodel_type: self_deployed\n"
			if name != "model provider" {
				definition += "model: {id: gpt-live-1}\n"
			}
			definition += suffix + "\n"
			require.Error(t, ValidateAgentDefinition([]byte(definition)))
		})
	}
}

func TestValidateGPTLiveAgent_RejectsRawPolicyModerationAliases(t *testing.T) {
	t.Parallel()
	const policyID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/" +
		"providers/Microsoft.CognitiveServices/accounts/account/raiPolicies/policy"
	for _, key := range []string{"invocationsModeration", "invocations_moderation"} {
		t.Run(key, func(t *testing.T) {
			definition := "kind: voice\nname: live\nsub_protocol: live\nmodel_type: self_deployed\n" +
				"model: {id: gpt-live-1}\npolicies:\n  - type: rai_policy\n    rai_policy_name: " + policyID +
				"\n    " + key + ": {}\n"
			require.ErrorContains(t, ValidateAgentDefinition([]byte(definition)),
				"invocationsModeration is not supported for GPT Live")
		})
	}
}

func TestValidateVoiceRawProperties_GPTLiveRawContracts(t *testing.T) {
	t.Parallel()
	base := map[string]any{"subProtocol": "live"}
	for name, invalid := range map[string]map[string]any{
		"live config empty": {"live_config": map[string]any{}},
		"tools empty":       {"tools": []any{}},
		"avatar empty":      {"avatar": map[string]any{}},
		"greeting empty":    {"greeting": map[string]any{}},
		"greeting scalar":   {"greeting": "hello"},
		"greeting type":     {"greeting": map[string]any{"type": "other", "text": "hello"}},
		"greeting text type": {"greeting": map[string]any{
			"type": "template", "text": 1,
		}},
		"greeting blank":   {"greeting": map[string]any{"type": "template", "text": "  "}},
		"greeting extra":   {"greeting": map[string]any{"type": "template", "text": "hi", "extra": true}},
		"structured entry": {"structuredInputs": map[string]any{"value": "bad"}},
		"description type": {"structuredInputs": map[string]any{"value": map[string]any{"description": 1}}},
		"schema type":      {"structuredInputs": map[string]any{"value": map[string]any{"schema": []any{}}}},
		"required type":    {"structuredInputs": map[string]any{"value": map[string]any{"required": "yes"}}},
	} {
		t.Run(name, func(t *testing.T) {
			values := map[string]any{}
			maps.Copy(values, base)
			maps.Copy(values, invalid)
			require.NotEmpty(t, ValidateVoiceRawProperties(values))
		})
	}

	for _, key := range []string{"invocationsModeration", "invocations_moderation"} {
		values := map[string]any{
			"subProtocol": "live",
			"policies": []any{map[string]any{
				"type": "rai_policy", "raiPolicyName": "policy", key: nil,
			}},
		}
		require.Contains(t, ValidateVoiceRawProperties(values),
			"policies[0].invocationsModeration is not supported for GPT Live")
	}

	for _, defaultValue := range []any{nil, "text", 42, []any{1}, map[string]any{"enabled": true}} {
		values := map[string]any{
			"subProtocol": "live",
			"structuredInputs": map[string]any{"value": map[string]any{
				"description": "input", "schema": map[string]any{"type": "string"},
				"required": false, "defaultValue": defaultValue,
			}},
			"greeting": map[string]any{"type": "template", "text": "Hello"},
		}
		require.Empty(t, ValidateVoiceRawProperties(values))
	}
	require.Empty(t, ValidateVoiceRawProperties(base), "omitting optional Live fields remains valid")
}

func TestVoiceConfigGPTLiveCustomObjectMarshalUsesOnlyID(t *testing.T) {
	t.Parallel()
	encoded, err := yaml.Marshal(VoiceConfig{ID: "custom-voice"})
	require.NoError(t, err)
	require.YAMLEq(t, "id: custom-voice\n", string(encoded))
	encoded, err = json.Marshal(VoiceConfig{ID: "custom-voice"})
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"custom-voice"}`, string(encoded))
}

func TestValidateVoiceAgent_SubProtocolPresence(t *testing.T) {
	t.Parallel()
	base := "kind: voice\nname: voice\nmodel: {id: gpt-realtime}\n"
	for name, value := range map[string]string{
		"null":    "null",
		"blank":   "''",
		"unknown": "Live",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Error(t, ValidateAgentDefinition([]byte(base+"sub_protocol: "+value+"\n")))
		})
	}
	require.NoError(t, ValidateAgentDefinition([]byte(base)))
	require.NoError(t, ValidateAgentDefinition([]byte(base+"sub_protocol: realtime\n")))
	require.Error(t, ValidateAgentDefinition([]byte(base+"subProtocol: live\n")))
	require.NoError(t, ValidateAgentDefinition([]byte(
		"kind: voice\nname: live\nsub_protocol: live\nmodel_type: self_deployed\nmodel: {id: gpt-live-1}\n")))
}

func TestValidateGPTLiveAgent_RejectsExplicitEmptyPolicies(t *testing.T) {
	t.Parallel()
	definition := []byte(
		"kind: voice\nname: live\nsub_protocol: live\nmodel_type: self_deployed\n" +
			"model: {id: gpt-live-1}\npolicies: []\n")
	require.ErrorContains(t, ValidateAgentDefinition(definition), "exactly one named rai_policy")
}

func TestCreateVoiceAgentAPIRequest_ExplicitRealtime(t *testing.T) {
	t.Parallel()
	req, err := CreateVoiceAgentAPIRequest(VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "realtime"},
		SubProtocol:     VoiceSubProtocolRealtime,
		Model:           &Model{Id: "gpt-realtime"},
	})
	require.NoError(t, err)
	def := req.Definition.(agent_api.VoiceAgentDefinition)
	require.Equal(t, "realtime", def.SubProtocol)
	require.NotNil(t, def.Audio)
}

func TestCreateVoiceAgentAPIRequest_RejectsLiveFieldsWithoutDiscriminator(t *testing.T) {
	t.Parallel()
	_, err := CreateVoiceAgentAPIRequest(VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "bad"},
		Model:           &Model{Id: "gpt-realtime"},
		Audio:           &VoiceAudio{Format: &VoiceAudioFormat{Type: "audio/pcm", Rate: new(24000)}},
	})
	require.ErrorContains(t, err, "require subProtocol 'live'")
}

func TestCreateGPTLiveAgentAPIRequest_MapsRaiAndMCPToolChoice(t *testing.T) {
	t.Parallel()
	req, err := CreateVoiceAgentAPIRequest(VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "live"},
		SubProtocol:     VoiceSubProtocolLive,
		ModelType:       VoiceModelTypeSelfDeployed,
		Model:           &Model{Id: "gpt-live-1"},
		Policies:        []Policy{{Type: PolicyTypeRai, RaiPolicyName: "policy"}},
		Delegation: &LiveVoiceDelegation{Type: "responses", Responses: &LiveResponsesDelegation{
			Model: "terra", ToolChoice: map[string]any{"type": "mcp", "name": "lookup", "serverLabel": "server"},
			Tools: []map[string]any{{"type": "function", "name": "check"}, {"type": "web_search"}},
		}},
	})
	require.NoError(t, err)
	def := req.Definition.(agent_api.VoiceAgentDefinition)
	require.Equal(t, "policy", def.RaiConfig.RaiPolicyName)
	choice := def.Delegation.Responses.ToolChoice.(map[string]any)
	require.Equal(t, "server", choice["server_label"])
	require.NotContains(t, choice, "serverLabel")
}
