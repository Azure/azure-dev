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

// TestValidateAgentDefinition_HostedOnlyFieldsRejectedOnPromptVoice rejects
// hosted-agent-only properties on non-hosted kinds (#9623): load/deploy
// conversion drops them, so accepting them would validate configuration
// that silently has no effect.
func TestValidateAgentDefinition_HostedOnlyFieldsRejectedOnPromptVoice(t *testing.T) {
	hostedOnly := []string{
		"codeConfiguration",
		"policies",
		"protocols",
		"agentEndpoint",
		"sessionConfiguration",
	}
	for _, field := range hostedOnly {
		yamlContent := []byte(fmt.Sprintf(`
kind: prompt-voice
name: voice-agent
model:
  id: gpt-realtime
%s: {}
`, field))
		err := ValidateAgentDefinition(yamlContent)
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("'%s' is only supported for 'hosted' agents", field)) {
			t.Fatalf("expected '%s' rejection error, got: %v", field, err)
		}
	}
}

// TestValidateAgentDefinition_HostedOnlySnakeCaseRejectedOnPromptVoice covers
// the standalone agent.yaml authoring spellings (yaml.go ContainerAgent tags).
func TestValidateAgentDefinition_HostedOnlySnakeCaseRejectedOnPromptVoice(t *testing.T) {
	for _, field := range []string{"code_configuration", "agent_endpoint", "session_configuration"} {
		yamlContent := []byte(fmt.Sprintf(`
kind: prompt-voice
name: voice-agent
model:
  id: gpt-realtime
%s: {}
`, field))
		err := ValidateAgentDefinition(yamlContent)
		if err == nil || !strings.Contains(err.Error(), "is only supported for 'hosted' agents") {
			t.Fatalf("expected '%s' rejection error, got: %v", field, err)
		}
	}
}

// TestValidateAgentDefinition_HostedFieldsStillAllowedOnHosted pins that the
// new checks don't fire for the hosted kind.
func TestValidateAgentDefinition_HostedFieldsStillAllowedOnHosted(t *testing.T) {
	yamlContent := []byte(`
kind: hosted
name: hosted-agent
codeConfiguration:
  directory: src
protocols: []
agentEndpoint: https://example.com
sessionConfiguration:
  idleTimeoutSeconds: 300
`)
	if err := ValidateAgentDefinition(yamlContent); err != nil {
		t.Fatalf("expected valid, got error: %v", err)
	}
}
