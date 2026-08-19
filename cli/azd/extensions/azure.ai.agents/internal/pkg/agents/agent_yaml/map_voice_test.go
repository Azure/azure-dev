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
	if in.Format == nil || in.Format.Type != defaultVoiceAudioType || in.Format.Rate != defaultVoiceAudioRate {
		t.Errorf("input format = %+v", in.Format)
	}
	if in.TurnDetection == nil || in.TurnDetection.Type != defaultVoiceTurnDetectionType {
		t.Errorf("turn detection = %+v", in.TurnDetection)
	}
	if in.Transcription == nil || in.Transcription.Model != defaultVoiceInputTranscriptionModel {
		t.Errorf("transcription = %+v", in.Transcription)
	}
	out := def.Audio.Output
	if out.Format == nil || out.Format.Type != defaultVoiceAudioType || out.Format.Rate != defaultVoiceAudioRate {
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

func TestCreateVoiceAgentAPIRequestFlat_UsesFlatOutputShape(t *testing.T) {
	t.Parallel()
	voice := "alloy"
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "voice-flat"},
		Model:           &Model{Id: "gpt-realtime"},
		Voice:           &voice,
	}

	req, err := CreateVoiceAgentAPIRequestFlat(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := req.Definition.(agent_api.VoiceAgentDefinitionFlat)
	if def.Audio.Output.Voice != "alloy" {
		t.Errorf("Voice = %q, want alloy", def.Audio.Output.Voice)
	}
	if def.Audio.Output.VoiceType != "openai" {
		t.Errorf("VoiceType = %q, want openai", def.Audio.Output.VoiceType)
	}
	if def.Audio.Output.VoiceLocale != "" {
		t.Errorf("VoiceLocale = %q, want empty", def.Audio.Output.VoiceLocale)
	}
}

func TestCreateVoiceAgentAPIRequestFlat_AzureVoiceLocale(t *testing.T) {
	t.Parallel()
	voice := "en-US-Ava:DragonHDLatestNeural"
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "voice-flat"},
		Model:           &Model{Id: "gpt-realtime"},
		Voice:           &voice,
	}

	req, err := CreateVoiceAgentAPIRequestFlat(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := req.Definition.(agent_api.VoiceAgentDefinitionFlat)
	if def.Audio.Output.Voice != voice {
		t.Errorf("Voice = %q, want %q", def.Audio.Output.Voice, voice)
	}
	if def.Audio.Output.VoiceType != "azure-standard" {
		t.Errorf("VoiceType = %q, want azure-standard", def.Audio.Output.VoiceType)
	}
	if def.Audio.Output.VoiceLocale != "en-US" {
		t.Errorf("VoiceLocale = %q, want en-US", def.Audio.Output.VoiceLocale)
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
