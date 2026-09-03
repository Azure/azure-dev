// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
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

func TestValidateAgentDefinition_PromptVoice_RejectsToolbox(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice-agent
model:
  id: gpt-realtime
toolbox:
  name: support-tools
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil || !strings.Contains(err.Error(), "toolbox is not supported for a prompt-voice agent") {
		t.Fatalf("expected unsupported toolbox error, got: %v", err)
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

func TestValidateAgentDefinition_PromptVoice_RejectsParallelToolCalls(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice-agent
model:
  id: gpt-realtime
parallel_tool_calls: true
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil || !strings.Contains(err.Error(), "parallel_tool_calls is not currently supported") {
		t.Fatalf("expected parallel_tool_calls validation error, got: %v", err)
	}
}

func TestValidateAgentDefinition_PromptVoice_RejectsZeroTurnDetectionThreshold(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice-agent
model:
  id: gpt-realtime
audio:
  input:
    turn_detection:
      type: azure_semantic_vad
      threshold: 0
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil || !strings.Contains(err.Error(), "threshold must be greater than 0") {
		t.Fatalf("expected threshold validation error, got: %v", err)
	}
}

func TestValidateAgentDefinition_PromptVoice_InvalidIncludeTranscriptionModel(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice-agent
model:
  id: gpt-realtime
audio:
  input:
    transcription:
      model: whisper-1
include:
  - item.input_audio_transcription.phrases
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil {
		t.Fatal("expected include/transcription validation error")
	}
	if !strings.Contains(err.Error(), "azure-speech") || !strings.Contains(err.Error(), "azure-fast-transcription") {
		t.Fatalf("expected transcription model guidance in error, got: %v", err)
	}
}

func TestValidateAgentDefinition_PromptVoice_AdvancedValidationBoundaries(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "unsupported format",
			yaml: `audio:
  input:
    format:
      type: audio/opus`,
			want: "audio/pcm",
		},
		{
			name: "invalid rate",
			yaml: `audio:
  input:
    format:
      type: audio/pcm
      rate: 0`,
			want: "rate must be greater than 0",
		},
		{
			name: "negative duration",
			yaml: `audio:
  input:
    turn_detection:
      type: azure_semantic_vad
      speech_duration_ms: -1`,
			want: "speech_duration_ms must be >= 0",
		},
		{
			name: "nan threshold",
			yaml: `audio:
  input:
    turn_detection:
      type: azure_semantic_vad
      threshold: .nan`,
			want: "threshold must be greater than 0",
		},
		{
			name: "blank voice name",
			yaml: `audio:
  output:
    voice:
      type: azure_standard
      name: ""`,
			want: "voice.name must not be blank",
		},
		{
			name: "invalid speed",
			yaml: `audio:
  output:
    speed: 2`,
			want: "speed must be between 0.25 and 1.5",
		},
		{
			name: "nan speed",
			yaml: `audio:
  output:
    speed: .nan`,
			want: "speed must be between 0.25 and 1.5",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yamlContent := []byte("kind: prompt-voice\nname: voice-agent\nmodel:\n  id: gpt-realtime\n" + tt.yaml + "\n")
			err := ValidateAgentDefinition(yamlContent)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q validation error, got: %v", tt.want, err)
			}
		})
	}
}

func TestValidateAgentDefinition_PromptVoice_MaxOutputTokensValidation(t *testing.T) {
	tests := []struct {
		name      string
		valueYaml string
		wantErr   bool
	}{
		{name: "string inf", valueYaml: "inf", wantErr: false},
		{name: "padded string inf", valueYaml: `" inf "`, wantErr: true},
		{name: "unsupported string", valueYaml: "unlimited", wantErr: true},
		{name: "integer", valueYaml: "4096", wantErr: false},
		{name: "zero", valueYaml: "0", wantErr: true},
		{name: "negative", valueYaml: "-1", wantErr: true},
		{name: "int32 max", valueYaml: "2147483647", wantErr: false},
		{name: "above int32 max", valueYaml: "2147483648", wantErr: true},
		{name: "integer valued float", valueYaml: "4096.0", wantErr: false},
		{name: "empty string", valueYaml: `""`, wantErr: true},
		{name: "non integer float", valueYaml: "1.5", wantErr: true},
		{name: "non finite positive infinity", valueYaml: ".inf", wantErr: true},
		{name: "non finite negative infinity", valueYaml: "-.inf", wantErr: true},
		{name: "not a number", valueYaml: ".nan", wantErr: true},
		{name: "bool", valueYaml: "true", wantErr: true},
		{name: "array", valueYaml: "[1]", wantErr: true},
		{name: "object", valueYaml: "{ value: 1 }", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yamlContent := fmt.Appendf(nil, `
kind: prompt-voice
name: voice-agent
model:
  id: gpt-realtime
max_output_tokens: %s
`, tt.valueYaml)
			err := ValidateAgentDefinition(yamlContent)
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "max_output_tokens") {
					t.Fatalf("expected max_output_tokens validation error, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected valid max_output_tokens, got: %v", err)
			}
		})
	}
}

func TestValidateAgentDefinition_PromptVoice_ValidIncludeTranscriptionModel(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice-agent
model:
  id: gpt-realtime
audio:
  input:
    transcription:
      model: azure-speech
include:
  - item.input_audio_transcription.phrases
`)
	if err := ValidateAgentDefinition(yamlContent); err != nil {
		t.Fatalf("expected azure-speech include config to be valid, got: %v", err)
	}
}

func TestValidateAgentDefinition_HostedVoiceAccepted(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice-wrapper
model_type: hosted_agent
target_agent:
  service: voice-target
  version: deployed
`)
	if err := ValidateAgentDefinition(yamlContent); err != nil {
		t.Fatalf("expected hosted voice definition to be valid, got: %v", err)
	}
}

func TestValidateAgentDefinition_HostedAgentRejectsHostedVoiceModelType(t *testing.T) {
	yamlContent := []byte(`
kind: hosted
name: hosted-target
model_type: hosted_agent
target_agent:
  service: target
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil || !strings.Contains(err.Error(), "model_type 'hosted_agent' is only valid") ||
		!strings.Contains(err.Error(), "target_agent is only valid") {
		t.Fatalf("expected hosted voice fields on hosted kind to fail, got: %v", err)
	}
}

func TestValidateAgentDefinition_HostedVoiceRequiresTarget(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice-wrapper
model_type: hosted_agent
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil || !strings.Contains(err.Error(), "target_agent.service is required") {
		t.Fatalf("expected target agent validation error, got: %v", err)
	}
}

func TestValidateAgentDefinition_HostedVoiceRejectsTargetOwnedFields(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice-wrapper
model_type: hosted_agent
target_agent:
  service: voice-target
model:
  id: gpt-realtime
instructions: not allowed
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil || !strings.Contains(err.Error(), "belong to the target hosted agent") ||
		!strings.Contains(err.Error(), "model is not allowed") {
		t.Fatalf("expected target-owned field validation errors, got: %v", err)
	}
}

func TestValidateAgentDefinition_HostedVoiceRejectsSchemas(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice-wrapper
model_type: hosted_agent
target_agent:
  service: voice-target
inputSchema:
  properties: []
outputSchema:
  properties: []
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil || !strings.Contains(err.Error(), "input_schema, output_schema") {
		t.Fatalf("expected target-owned schema validation error, got: %v", err)
	}
}

func TestValidateAgentDefinition_PromptVoiceRejectsProtocols(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice
model:
  id: gpt-realtime
protocols:
  - protocol: invocations_ws
    version: 1.0.0
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil || !strings.Contains(err.Error(), "protocols is not supported") {
		t.Fatalf("expected prompt voice protocols validation error, got: %v", err)
	}
}

func TestValidateAgentDefinition_PromptVoiceRejectsPolicies(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice
model:
  id: gpt-realtime
policies:
  - type: rai_policy
    rai_policy_name: policy
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil || !strings.Contains(err.Error(), "policies is not supported") {
		t.Fatalf("expected prompt voice policy validation error, got: %v", err)
	}
}

func TestValidateAgentDefinition_PromptVoiceRejectsMalformedPolicies(t *testing.T) {
	yamlContent := []byte(`
kind: prompt-voice
name: voice
model:
  id: gpt-realtime
policies: invalid
`)
	err := ValidateAgentDefinition(yamlContent)
	if err == nil || !strings.Contains(err.Error(), "template.policies is not valid") {
		t.Fatalf("expected malformed policy validation error, got: %v", err)
	}
}
