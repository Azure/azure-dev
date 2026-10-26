// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"encoding/json"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"github.com/stretchr/testify/require"
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
			Output: &VoiceAudioOutput{Voice: "marin"},
		},
		Greeting:         map[string]any{"type": "template", "text": "Hello {{name}}"},
		StructuredInputs: map[string]any{"name": map[string]any{"required": true}},
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

func TestValidateGPTLiveAgent_RejectsInvalidNativeValues(t *testing.T) {
	t.Parallel()
	for _, suffix := range []string{
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
		yaml := "kind: voice\nname: live\nsub_protocol: live\nmodel_type: self_deployed\nmodel:\n  id: gpt-live-1\n" + suffix + "\n"
		require.Error(t, ValidateAgentDefinition([]byte(yaml)))
	}
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
