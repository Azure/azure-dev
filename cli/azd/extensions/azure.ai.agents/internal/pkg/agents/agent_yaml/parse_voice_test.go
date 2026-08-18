// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"strings"
	"testing"
)

// TestExtractAgentDefinition_PromptVoice verifies a prompt-voice manifest parses
// into a VoiceAgent with its author-facing fields populated.
func TestExtractAgentDefinition_PromptVoice(t *testing.T) {
	yamlContent := []byte(`
name: voice-agent
template:
  kind: prompt-voice
  name: voice-agent
  model:
    id: gpt-realtime
  instructions: You are a friendly voice assistant.
  voice: en-US-Ava:DragonHDLatestNeural
  store: true
`)

	agent, err := ExtractAgentDefinition(yamlContent)
	if err != nil {
		t.Fatalf("ExtractAgentDefinition failed: %v", err)
	}

	voiceAgent, ok := agent.(VoiceAgent)
	if !ok {
		t.Fatalf("Expected VoiceAgent, got %T", agent)
	}
	if voiceAgent.Kind != AgentKindPromptVoice {
		t.Errorf("Kind = %q, want prompt-voice", voiceAgent.Kind)
	}
	if voiceAgent.Model == nil || voiceAgent.Model.Id != "gpt-realtime" {
		t.Errorf("Model = %+v", voiceAgent.Model)
	}
	if voiceAgent.Instructions == nil || *voiceAgent.Instructions != "You are a friendly voice assistant." {
		t.Errorf("Instructions = %v", voiceAgent.Instructions)
	}
	if voiceAgent.Voice == nil || *voiceAgent.Voice != "en-US-Ava:DragonHDLatestNeural" {
		t.Errorf("Voice = %v", voiceAgent.Voice)
	}
	if voiceAgent.Store == nil || !*voiceAgent.Store {
		t.Errorf("Store = %v, want true", voiceAgent.Store)
	}
}

func TestExtractAgentDefinition_PromptVoiceAdvancedConfig(t *testing.T) {
	yamlContent := []byte(`
name: voice-agent
template:
  kind: prompt-voice
  name: voice-agent
  model:
    id: gpt-realtime
  structured_inputs:
    agent_persona:
      type: string
      defaultValue: Ada
  audio:
    input:
      format:
        type: audio/pcmu
        rate: 16000
      noise_reduction:
        type: near_field
      turn_detection:
        type: server_vad
        threshold: 0.45
        prefix_padding_ms: 250
        silence_duration_ms: 600
        create_response: false
      transcription:
        model: whisper-1
        language: en-US
        prompt: Contoso terms
    output:
      format:
        type: audio/pcm
        rate: 24000
      voice:
        type: azure_standard
        name: en-US-AvaNeural
      speed: 1.1
  output_modalities: [audio, text]
  tools:
    - type: system
      name: end_conversation
  avatar:
    type: video-avatar
    character: lisa
`)

	agent, err := ExtractAgentDefinition(yamlContent)
	if err != nil {
		t.Fatalf("ExtractAgentDefinition failed: %v", err)
	}
	voiceAgent := agent.(VoiceAgent)
	if voiceAgent.StructuredInputs["agent_persona"] == nil {
		t.Fatalf("StructuredInputs = %+v", voiceAgent.StructuredInputs)
	}
	if voiceAgent.Audio == nil || voiceAgent.Audio.Input == nil || voiceAgent.Audio.Output == nil {
		t.Fatalf("Audio = %+v", voiceAgent.Audio)
	}
	if voiceAgent.Audio.Input.Format.Type != "audio/pcmu" || *voiceAgent.Audio.Input.Format.Rate != 16000 {
		t.Errorf("input format = %+v", voiceAgent.Audio.Input.Format)
	}
	if voiceAgent.Audio.Input.NoiseReduction.Type != "near_field" {
		t.Errorf("noise reduction = %+v", voiceAgent.Audio.Input.NoiseReduction)
	}
	if voiceAgent.Audio.Input.TurnDetection.CreateResponse == nil || *voiceAgent.Audio.Input.TurnDetection.CreateResponse {
		t.Errorf("turn detection = %+v", voiceAgent.Audio.Input.TurnDetection)
	}
	if voiceAgent.Audio.Input.Transcription.Language == nil || *voiceAgent.Audio.Input.Transcription.Language != "en-US" {
		t.Errorf("transcription = %+v", voiceAgent.Audio.Input.Transcription)
	}
	if voiceAgent.Audio.Output.Voice.Name != "en-US-AvaNeural" {
		t.Errorf("output voice = %+v", voiceAgent.Audio.Output.Voice)
	}
	if len(voiceAgent.OutputModalities) != 2 || voiceAgent.OutputModalities[1] != "text" {
		t.Errorf("OutputModalities = %v", voiceAgent.OutputModalities)
	}
	if len(voiceAgent.Tools) != 1 || voiceAgent.Tools[0]["type"] != "system" {
		t.Errorf("Tools = %+v", voiceAgent.Tools)
	}
	if voiceAgent.Avatar["character"] != "lisa" {
		t.Errorf("Avatar = %+v", voiceAgent.Avatar)
	}
}

// TestValidateAgentDefinition_PromptVoice_OK validates a minimal well-formed
// prompt-voice manifest.
func TestValidateAgentDefinition_PromptVoice_OK(t *testing.T) {
	// ValidateAgentDefinition operates on the template body directly.
	yamlContent := []byte(`
kind: prompt-voice
name: voice-agent
model:
  id: gpt-realtime
`)
	if err := ValidateAgentDefinition(yamlContent); err != nil {
		t.Fatalf("expected valid, got error: %v", err)
	}
}

// TestValidateAgentDefinition_PromptVoice_MissingModel rejects a manifest with
// no model.id.
func TestValidateAgentDefinition_PromptVoice_MissingModel(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice-agent
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil || !strings.Contains(err.Error(), "model.id is required") {
		t.Fatalf("expected model.id required error, got: %v", err)
	}
}

func TestValidateAgentDefinition_PromptVoice_BlankModelID(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice-agent
model:
  id: "   "
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil || !strings.Contains(err.Error(), "model.id is required") {
		t.Fatalf("expected model.id required error, got: %v", err)
	}
}

// TestValidateAgentDefinition_PromptVoice_BYOMAccepted accepts self_deployed
// (BYOM) model_type.
func TestValidateAgentDefinition_PromptVoice_BYOMAccepted(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice-agent
model:
  id: my-realtime-deployment
model_type: self_deployed
`)
	if err := ValidateAgentDefinition(yamlContent); err != nil {
		t.Fatalf("expected self_deployed to be valid, got: %v", err)
	}
}

func TestValidateAgentDefinition_PromptVoice_InvalidModelType(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice-agent
model:
  id: gpt-realtime
model_type: unsupported
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil || !strings.Contains(err.Error(), "model_type 'unsupported' is not supported") {
		t.Fatalf("expected invalid model_type error, got: %v", err)
	}
}

func TestValidateAgentDefinition_PromptVoice_InvalidAdvancedConfig(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice-agent
model:
  id: gpt-realtime
audio:
  input:
    format:
      type: ""
      rate: 0
    turn_detection:
      type: server_vad
      threshold: 2
  output:
    speed: 2
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil {
		t.Fatal("expected advanced config validation error")
	}
	for _, want := range []string{"format.type", "format.rate", "threshold", "speed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got: %v", want, err)
		}
	}
}
