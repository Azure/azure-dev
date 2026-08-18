// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
)

// ---------------------------------------------------------------------------
// isOpenAIVoice / buildVoiceConfig
// ---------------------------------------------------------------------------

func TestIsOpenAIVoice(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"alloy":                          true, // known OpenAI voice
		"verse":                          true,
		"Shimmer":                        true,  // known OpenAI voice, case-insensitive
		"en-US-Ava:DragonHDLatestNeural": false, // Azure Neural locale prefix
		"en-US-JennyNeural":              false,
		"ja-JP-NanamiNeural":             false, // non-en Azure locale prefix
	}
	for name, want := range cases {
		if got := isOpenAIVoice(name); got != want {
			t.Errorf("isOpenAIVoice(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestBuildVoiceConfig_OpenAI(t *testing.T) {
	t.Parallel()
	cfg := buildVoiceConfig("alloy")
	if cfg.Type != "openai" {
		t.Errorf("Type = %q, want openai", cfg.Type)
	}
	if cfg.Name != "alloy" {
		t.Errorf("Name = %q, want alloy", cfg.Name)
	}
}

func TestBuildVoiceConfig_OpenAINormalizesCasing(t *testing.T) {
	t.Parallel()
	// OpenAI wire IDs are lowercase; mixed-case/padded input must normalize.
	cfg := buildVoiceConfig("  Shimmer ")
	if cfg.Type != "openai" {
		t.Errorf("Type = %q, want openai", cfg.Type)
	}
	if cfg.Name != "shimmer" {
		t.Errorf("Name = %q, want shimmer", cfg.Name)
	}
}

func TestBuildVoiceConfig_Azure(t *testing.T) {
	t.Parallel()
	cfg := buildVoiceConfig("en-US-Ava:DragonHDLatestNeural")
	if cfg.Type != "azure_standard" {
		t.Errorf("Type = %q, want azure_standard", cfg.Type)
	}
	if cfg.Name != "en-US-Ava:DragonHDLatestNeural" {
		t.Errorf("Name = %q", cfg.Name)
	}
}

// ---------------------------------------------------------------------------
// CreateVoiceAgentAPIRequest
// ---------------------------------------------------------------------------

// TestCreateVoiceAgentAPIRequest_Defaults verifies that a minimal prompt-voice
// agent (only model.id) is translated to the data-plane "voice" kind with the
// full default audio pipeline and implicit managed model_type.
func TestCreateVoiceAgentAPIRequest_Defaults(t *testing.T) {
	t.Parallel()
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{
			Kind: AgentKindPromptVoice,
			Name: "my-voice-agent",
		},
		Model: &Model{Id: "gpt-realtime"},
	}

	req, err := CreateVoiceAgentAPIRequest(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Name != "my-voice-agent" {
		t.Errorf("Name = %q", req.Name)
	}

	def, ok := req.Definition.(agent_api.VoiceAgentDefinition)
	if !ok {
		t.Fatalf("expected VoiceAgentDefinition, got %T", req.Definition)
	}

	// Authoring kind prompt-voice is translated to service kind voice.
	if def.Kind != agent_api.AgentKindVoice {
		t.Errorf("Kind = %q, want %q", def.Kind, agent_api.AgentKindVoice)
	}
	// v1 is implicitly managed.
	if def.ModelType != agent_api.VoiceModelTypeManaged {
		t.Errorf("ModelType = %q, want managed", def.ModelType)
	}
	if def.Model != "gpt-realtime" {
		t.Errorf("Model = %q", def.Model)
	}
	if def.Instructions != defaultVoiceInstructions {
		t.Errorf("Instructions = %q, want default", def.Instructions)
	}
	if len(def.OutputModalities) != 1 || def.OutputModalities[0] != "audio" {
		t.Errorf("OutputModalities = %v, want [audio]", def.OutputModalities)
	}

	// Audio pipeline defaults.
	if def.Audio == nil || def.Audio.Input == nil || def.Audio.Output == nil {
		t.Fatalf("Audio pipeline not populated: %+v", def.Audio)
	}
	in := def.Audio.Input
	if in.Format == nil || in.Format.Type != defaultVoiceAudioType || in.Format.Rate == nil ||
		*in.Format.Rate != defaultVoiceAudioRate {
		t.Errorf("input format = %+v", in.Format)
	}
	if in.TurnDetection == nil || in.TurnDetection.Type != defaultVoiceTurnDetectionType {
		t.Errorf("turn detection = %+v", in.TurnDetection)
	}
	if in.Transcription == nil || in.Transcription.Model != defaultVoiceInputTranscriptionModel {
		t.Errorf("transcription = %+v", in.Transcription)
	}
	out := def.Audio.Output
	if out.Format == nil || out.Format.Type != defaultVoiceAudioType || out.Format.Rate == nil ||
		*out.Format.Rate != defaultVoiceAudioRate {
		t.Errorf("output format = %+v", out.Format)
	}
	// Default voice is the DragonHD Azure Neural voice.
	if out.Voice == nil || out.Voice.Type != "azure_standard" || out.Voice.Name != defaultVoiceName {
		t.Errorf("output voice = %+v, want azure_standard/%s", out.Voice, defaultVoiceName)
	}
	// Store defaults to nil (service defaults to false).
	if def.Store != nil {
		t.Errorf("Store = %v, want nil", def.Store)
	}
}

// TestCreateVoiceAgentAPIRequest_Overrides verifies author-facing overrides
// (instructions, voice, store) flow through.
func TestCreateVoiceAgentAPIRequest_Overrides(t *testing.T) {
	t.Parallel()
	instructions := "You are a terse concierge."
	voice := "alloy"
	store := true
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "concierge"},
		Model:           &Model{Id: "gpt-realtime"},
		Instructions:    &instructions,
		Voice:           &voice,
		Store:           &store,
	}

	req, err := CreateVoiceAgentAPIRequest(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := req.Definition.(agent_api.VoiceAgentDefinition)
	if def.Instructions != instructions {
		t.Errorf("Instructions = %q", def.Instructions)
	}
	// "alloy" is an OpenAI realtime voice.
	if def.Audio.Output.Voice.Type != "openai" || def.Audio.Output.Voice.Name != "alloy" {
		t.Errorf("voice = %+v, want openai/alloy", def.Audio.Output.Voice)
	}
	if def.Store == nil || !*def.Store {
		t.Errorf("Store = %v, want true", def.Store)
	}
}

func TestCreateVoiceAgentAPIRequest_AdvancedConfig(t *testing.T) {
	t.Parallel()
	inRate := 16000
	outRate := 24000
	threshold := 0.4
	prefixPaddingMs := 250
	silenceDurationMs := 600
	createResponse := false
	language := "en-US"
	prompt := "Contoso product names"
	speed := 1.2
	store := true
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "advanced-voice"},
		Model:           &Model{Id: "gpt-realtime"},
		StructuredInputs: map[string]any{
			"agent_persona": map[string]any{"type": "string", "defaultValue": "Ada"},
		},
		Audio: &VoiceAudio{
			Input: &VoiceAudioInput{
				Format:         &VoiceAudioFormat{Type: "audio/pcmu", Rate: &inRate},
				NoiseReduction: &VoiceNoiseReduction{Type: "near_field"},
				TurnDetection: &VoiceTurnDetection{
					Type:              "server_vad",
					Threshold:         &threshold,
					PrefixPaddingMs:   &prefixPaddingMs,
					SilenceDurationMs: &silenceDurationMs,
					CreateResponse:    &createResponse,
				},
				Transcription: &VoiceTranscription{Model: "whisper-1", Language: &language, Prompt: &prompt},
			},
			Output: &VoiceAudioOutput{
				Format: &VoiceAudioFormat{Type: "audio/pcm", Rate: &outRate},
				Voice:  &VoiceConfig{Type: "azure_standard", Name: "en-US-AvaNeural"},
				Speed:  &speed,
			},
		},
		OutputModalities: []string{"audio", "text"},
		Store:            &store,
		Tools:            []map[string]any{{"type": "system", "name": "end_conversation"}},
		Avatar:           map[string]any{"type": "video-avatar", "character": "lisa"},
	}

	req, err := CreateVoiceAgentAPIRequest(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := req.Definition.(agent_api.VoiceAgentDefinition)
	if def.StructuredInputs["agent_persona"] == nil {
		t.Fatalf("structured inputs not mapped: %+v", def.StructuredInputs)
	}
	if def.Audio.Input.Format.Type != "audio/pcmu" || *def.Audio.Input.Format.Rate != inRate {
		t.Errorf("input format = %+v", def.Audio.Input.Format)
	}
	if def.Audio.Input.NoiseReduction == nil || def.Audio.Input.NoiseReduction.Type != "near_field" {
		t.Errorf("noise reduction = %+v", def.Audio.Input.NoiseReduction)
	}
	if def.Audio.Input.TurnDetection.Threshold != &threshold ||
		def.Audio.Input.TurnDetection.PrefixPaddingMs != &prefixPaddingMs ||
		def.Audio.Input.TurnDetection.SilenceDurationMs != &silenceDurationMs ||
		def.Audio.Input.TurnDetection.CreateResponse != &createResponse {
		t.Errorf("turn detection = %+v", def.Audio.Input.TurnDetection)
	}
	if def.Audio.Input.Transcription.Model != "whisper-1" ||
		def.Audio.Input.Transcription.Language != &language ||
		def.Audio.Input.Transcription.Prompt != &prompt {
		t.Errorf("transcription = %+v", def.Audio.Input.Transcription)
	}
	if def.Audio.Output.Format.Type != "audio/pcm" || *def.Audio.Output.Format.Rate != outRate {
		t.Errorf("output format = %+v", def.Audio.Output.Format)
	}
	if def.Audio.Output.Voice.Type != "azure_standard" || def.Audio.Output.Voice.Name != "en-US-AvaNeural" {
		t.Errorf("voice = %+v", def.Audio.Output.Voice)
	}
	if def.Audio.Output.Speed != &speed {
		t.Errorf("speed = %v", def.Audio.Output.Speed)
	}
	if len(def.OutputModalities) != 2 || def.OutputModalities[1] != "text" {
		t.Errorf("output modalities = %v", def.OutputModalities)
	}
	if len(def.Tools) != 1 || def.Tools[0]["type"] != "system" {
		t.Errorf("tools = %+v", def.Tools)
	}
	if def.Avatar["character"] != "lisa" {
		t.Errorf("avatar = %+v", def.Avatar)
	}
}

// TestCreateVoiceAgentAPIRequest_ExplicitManaged verifies that explicitly
// setting model_type: managed is accepted (idempotent with the default).
func TestCreateVoiceAgentAPIRequest_ExplicitManaged(t *testing.T) {
	t.Parallel()
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "v"},
		Model:           &Model{Id: "gpt-realtime"},
		ModelType:       VoiceModelTypeManaged,
	}
	req, err := CreateVoiceAgentAPIRequest(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Definition.(agent_api.VoiceAgentDefinition).ModelType != agent_api.VoiceModelTypeManaged {
		t.Errorf("ModelType not managed")
	}
}

// TestCreateVoiceAgentAPIRequest_MissingModel verifies model.id is required.
func TestCreateVoiceAgentAPIRequest_MissingModel(t *testing.T) {
	t.Parallel()
	for _, agent := range []VoiceAgent{
		{AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "v"}},
		{AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "v"}, Model: &Model{}},
	} {
		if _, err := CreateVoiceAgentAPIRequest(agent); err == nil {
			t.Errorf("expected error for missing model.id, agent=%+v", agent)
		}
	}
}

func TestCreateVoiceAgentAPIRequest_TrimsModelID(t *testing.T) {
	t.Parallel()
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "v"},
		Model:           &Model{Id: "  my-realtime-deployment  "},
	}
	req, err := CreateVoiceAgentAPIRequest(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := req.Definition.(agent_api.VoiceAgentDefinition)
	if def.Model != "my-realtime-deployment" {
		t.Errorf("Model = %q, want trimmed model id", def.Model)
	}
}

func TestCreateVoiceAgentAPIRequest_BlankModelID(t *testing.T) {
	t.Parallel()
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "v"},
		Model:           &Model{Id: "   "},
	}
	if _, err := CreateVoiceAgentAPIRequest(agent); err == nil {
		t.Error("expected error for blank model.id")
	}
}

// TestCreateVoiceAgentAPIRequest_SelfDeployedMapped verifies BYOM model_type is
// passed through to the voice agent API request.
func TestCreateVoiceAgentAPIRequest_SelfDeployedMapped(t *testing.T) {
	t.Parallel()
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "v"},
		Model:           &Model{Id: "my-realtime-deployment"},
		ModelType:       VoiceModelTypeSelfDeployed,
	}
	req, err := CreateVoiceAgentAPIRequest(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := req.Definition.(agent_api.VoiceAgentDefinition)
	if def.ModelType != agent_api.VoiceModelTypeSelfDeployed {
		t.Errorf("ModelType = %q, want self_deployed", def.ModelType)
	}
	if def.Model != "my-realtime-deployment" {
		t.Errorf("Model = %q, want deployment name", def.Model)
	}
}

func TestCreateVoiceAgentAPIRequest_InvalidModelType(t *testing.T) {
	t.Parallel()
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "v"},
		Model:           &Model{Id: "gpt-realtime"},
		ModelType:       VoiceModelType("unsupported"),
	}
	if _, err := CreateVoiceAgentAPIRequest(agent); err == nil {
		t.Error("expected error for unsupported model_type")
	}
}

func TestCreateVoiceAgentAPIRequest_InvalidAdvancedConfig(t *testing.T) {
	t.Parallel()
	badThreshold := 2.0
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "v"},
		Model:           &Model{Id: "gpt-realtime"},
		Audio: &VoiceAudio{Input: &VoiceAudioInput{
			TurnDetection: &VoiceTurnDetection{Type: "server_vad", Threshold: &badThreshold},
		}},
	}
	if _, err := CreateVoiceAgentAPIRequest(agent); err == nil {
		t.Error("expected error for invalid advanced config")
	}
}
