// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"github.com/stretchr/testify/require"
)

func TestCreateVoiceAgentAPIRequest_DefinitionGoldens(t *testing.T) {
	t.Parallel()
	speed := 1.1
	parallel := true
	tests := []struct {
		name   string
		build  func() (*agent_api.CreateAgentRequest, error)
		golden string
	}{
		{
			name: "managed",
			build: func() (*agent_api.CreateAgentRequest, error) {
				return CreateVoiceAgentAPIRequest(VoiceAgent{
					AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "managed-voice"},
					ModelType:       VoiceModelTypeManaged,
					Model:           &Model{Id: "gpt-realtime"},
					Audio: &VoiceAudio{Output: &VoiceAudioOutput{
						Format: &VoiceAudioFormat{Type: "audio/pcm", Rate: new(24000)},
						Speed:  &speed,
					}},
				})
			},
			golden: "managed.json",
		},
		{
			name: "byom",
			build: func() (*agent_api.CreateAgentRequest, error) {
				return CreateVoiceAgentAPIRequest(VoiceAgent{
					AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "byom-voice"},
					ModelType:       VoiceModelTypeSelfDeployed,
					Model:           &Model{Id: "realtime-deployment"},
					Voice:           new("alloy"),
					Audio: &VoiceAudio{
						Input:  &VoiceAudioInput{Format: &VoiceAudioFormat{Type: "audio/pcmu"}},
						Output: &VoiceAudioOutput{Format: &VoiceAudioFormat{Type: "audio/pcma"}},
					},
				})
			},
			golden: "byom.json",
		},
		{
			name: "hosted wrapper",
			build: func() (*agent_api.CreateAgentRequest, error) {
				return CreateHostedVoiceAgentAPIRequest(VoiceAgent{
					AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "hosted-wrapper"},
					ConversationEngine: &VoiceConversationEngine{
						Type: "hosted_agent", Name: "target-service",
					},
					Greeting:          map[string]any{"type": "template", "text": "Hello"},
					StructuredInputs:  map[string]any{"tenant": map[string]any{"defaultValue": "contoso"}},
					ParallelToolCalls: &parallel,
					MaxOutputTokens:   1024,
					Include:           []string{"item.input_audio_transcription.phrases"},
				}, agent_api.VoiceTargetAgentReference{Name: "target-agent", Version: "7"})
			},
			golden: "hosted-wrapper.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := tt.build()
			require.NoError(t, err)
			actual, err := json.MarshalIndent(req.Definition, "", "  ")
			require.NoError(t, err)
			expected, err := os.ReadFile(filepath.Join("testdata", "voice-definition-goldens", tt.golden))
			require.NoError(t, err)
			require.JSONEq(t, string(expected), string(actual))
		})
	}
}

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
	// Default voice is the DragonHD Azure Neural voice in the flat unified shape.
	if out.Voice != defaultVoiceName || out.VoiceType != "azure-standard" || out.VoiceLocale != "en-US" {
		t.Errorf("output voice = %+v, want azure-standard/%s", out, defaultVoiceName)
	}
	// Store defaults to nil (service defaults to false).
	if def.Store != nil {
		t.Errorf("Store = %v, want nil", def.Store)
	}
}

func TestCreateVoiceAgentAPIRequest_DefinitionOverlay(t *testing.T) {
	t.Parallel()
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "voice"},
		Model:           &Model{Id: "gpt-realtime"},
		DefinitionOverlay: map[string]any{
			"audio":     map[string]any{"input": map[string]any{"new_setting": true}},
			"new_array": []any{"one", map[string]any{"two": 2}},
		},
	}
	req, err := CreateVoiceAgentAPIRequest(agent)
	require.NoError(t, err)
	definition, ok := req.Definition.(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, definition["audio"].(map[string]any)["input"].(map[string]any)["new_setting"])
	require.Equal(t, agent.DefinitionOverlay["new_array"], definition["new_array"])
	definition["new_array"].([]any)[1].(map[string]any)["two"] = 3
	require.Equal(t, 2, agent.DefinitionOverlay["new_array"].([]any)[1].(map[string]any)["two"])
}

func TestCreateVoiceAgentAPIRequest_DefinitionOverlayModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		agent VoiceAgent
		build func(VoiceAgent) (*agent_api.CreateAgentRequest, error)
	}{
		{
			name: "managed",
			agent: VoiceAgent{AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "voice"},
				Model: &Model{Id: "gpt-realtime"}},
			build: CreateVoiceAgentAPIRequest,
		},
		{
			name: "byom",
			agent: VoiceAgent{AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "voice"},
				ModelType: VoiceModelTypeSelfDeployed, Model: &Model{Id: "deployment"}},
			build: CreateVoiceAgentAPIRequest,
		},
		{
			name: "hosted",
			agent: VoiceAgent{AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "voice"},
				ConversationEngine: &VoiceConversationEngine{Type: "hosted_agent", Name: "target"}},
			build: func(agent VoiceAgent) (*agent_api.CreateAgentRequest, error) {
				return CreateHostedVoiceAgentAPIRequest(agent,
					agent_api.VoiceTargetAgentReference{Name: "target", Version: "1"})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.agent.DefinitionOverlay = map[string]any{"future": true}
			req, err := tt.build(tt.agent)
			require.NoError(t, err)
			require.Equal(t, true, req.Definition.(map[string]any)["future"])
		})
	}
}

func TestCreateVoiceAgentAPIRequest_EmptyDefinitionOverlayPreservesTypedDefinition(t *testing.T) {
	t.Parallel()
	var baseline []byte
	for _, overlay := range []map[string]any{nil, {}} {
		req, err := CreateVoiceAgentAPIRequest(VoiceAgent{
			AgentDefinition:   AgentDefinition{Kind: AgentKindVoice, Name: "voice"},
			Model:             &Model{Id: "gpt-realtime"},
			DefinitionOverlay: overlay,
		})
		require.NoError(t, err)
		require.IsType(t, agent_api.VoiceAgentDefinition{}, req.Definition)
		encoded, err := json.Marshal(req.Definition)
		require.NoError(t, err)
		if baseline == nil {
			baseline = encoded
		} else {
			require.JSONEq(t, string(baseline), string(encoded))
		}
	}
}

func TestCreateVoiceAgentAPIRequest_DefinitionOverlayRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		overlay map[string]any
		path    string
	}{
		{name: "equal scalar conflict", overlay: map[string]any{"instructions": defaultVoiceInstructions}, path: "$.instructions"},
		{name: "array conflict", overlay: map[string]any{"output_modalities": []any{"audio"}}, path: "$.output_modalities"},
		{name: "object scalar conflict", overlay: map[string]any{"audio": map[string]any{"input": "bad"}}, path: "$.audio.input"},
		{name: "reserved", overlay: map[string]any{"kind": "voice"}, path: "$.kind"},
		{name: "null", overlay: map[string]any{"future": map[string]any{"value": nil}}, path: "$.future.value"},
		{name: "array null", overlay: map[string]any{"future": []any{"ok", nil}}, path: "$.future[1]"},
		{name: "unsafe float", overlay: map[string]any{"future": float64(1 << 53)}, path: "$.future"},
		{name: "unsafe int", overlay: map[string]any{"future": int64(1 << 53)}, path: "$.future"},
		{name: "unsafe uint", overlay: map[string]any{"future": uint64(1 << 53)}, path: "$.future"},
		{name: "nan", overlay: map[string]any{"future": math.NaN()}, path: "$.future"},
		{name: "infinity", overlay: map[string]any{"future": math.Inf(1)}, path: "$.future"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CreateVoiceAgentAPIRequest(VoiceAgent{
				AgentDefinition:   AgentDefinition{Kind: AgentKindVoice, Name: "voice"},
				Model:             &Model{Id: "gpt-realtime"},
				DefinitionOverlay: tt.overlay,
			})
			require.ErrorContains(t, err, tt.path)
			require.ErrorContains(t, err, "definitionOverlay")
		})
	}
}

func TestCreateVoiceAgentAPIRequest_DefinitionOverlayCaseSafety(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"kind", "KIND", "model_type", "MODEL_TYPE", "model", "MODEL",
		"conversation_engine", "CONVERSATION_ENGINE"} {
		t.Run(key, func(t *testing.T) {
			_, err := CreateVoiceAgentAPIRequest(VoiceAgent{
				AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "voice"},
				Model:           &Model{Id: "gpt-realtime"}, DefinitionOverlay: map[string]any{key: true},
			})
			require.ErrorContains(t, err, "reserved root key")
		})
	}
	_, err := CreateVoiceAgentAPIRequest(VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "voice"}, Model: &Model{Id: "gpt-realtime"},
		DefinitionOverlay: map[string]any{"audio": map[string]any{"INPUT": map[string]any{"future": true}}},
	})
	require.ErrorContains(t, err, "$.audio.INPUT")
}

func TestCreateVoiceAgentAPIRequest_DefinitionOverlayDeterministicError(t *testing.T) {
	t.Parallel()
	for range 20 {
		_, err := CreateVoiceAgentAPIRequest(VoiceAgent{
			AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "voice"}, Model: &Model{Id: "gpt-realtime"},
			DefinitionOverlay: map[string]any{"z": nil, "a": nil},
		})
		require.ErrorContains(t, err, "$.a")
	}
}

func TestCreateVoiceAgentAPIRequest_GPTLiveDefinitionOverlay(t *testing.T) {
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "live"},
		SubProtocol:     VoiceSubProtocolLive,
		ModelType:       VoiceModelTypeSelfDeployed,
		Model:           &Model{Id: "gpt-live-1"},
		DefinitionOverlay: map[string]any{
			"future_live_setting": map[string]any{"enabled": true},
		},
	}
	require.NoError(t, ValidateVoiceDefinitionOverlay(agent))
	request, err := CreateVoiceAgentAPIRequest(agent)
	require.NoError(t, err)
	definition, ok := request.Definition.(map[string]any)
	require.True(t, ok)
	require.Equal(t, map[string]any{"enabled": true}, definition["future_live_setting"])
	require.Equal(t, "live", definition["sub_protocol"])
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
	if def.Audio.Output.VoiceType != "openai" || def.Audio.Output.Voice != "alloy" {
		t.Errorf("voice = %+v, want openai/alloy", def.Audio.Output.Voice)
	}
	if def.Store == nil || !*def.Store {
		t.Errorf("Store = %v, want true", def.Store)
	}
}

func TestCreateVoiceAgentAPIRequest_UsesServiceOutputShape(t *testing.T) {
	t.Parallel()
	voice := "alloy"
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "voice-agent"},
		Model:           &Model{Id: "gpt-realtime"},
		Voice:           &voice,
	}

	req, err := CreateVoiceAgentAPIRequest(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := req.Definition.(agent_api.VoiceAgentDefinition)
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

func TestCreateVoiceAgentAPIRequest_UsesAzureVoiceLocale(t *testing.T) {
	t.Parallel()
	voice := "en-US-Ava:DragonHDLatestNeural"
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "voice-agent"},
		Model:           &Model{Id: "gpt-realtime"},
		Voice:           &voice,
	}

	req, err := CreateVoiceAgentAPIRequest(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := req.Definition.(agent_api.VoiceAgentDefinition)
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

func TestCreateVoiceAgentAPIRequest_UsesAzureVoiceLocaleVariants(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		voice      string
		wantLocale string
	}{
		{name: "script locale", voice: "az-Latn-AZ-BanuNeural", wantLocale: "az-Latn-AZ"},
		{name: "numeric region", voice: "es-419-AnaNeural", wantLocale: "es-419"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			agent := VoiceAgent{
				AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "voice-agent"},
				Model:           &Model{Id: "gpt-realtime"},
				Voice:           &tt.voice,
			}

			req, err := CreateVoiceAgentAPIRequest(agent)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			def := req.Definition.(agent_api.VoiceAgentDefinition)
			if def.Audio.Output.Voice != tt.voice {
				t.Errorf("Voice = %q, want %q", def.Audio.Output.Voice, tt.voice)
			}
			if def.Audio.Output.VoiceType != "azure-standard" {
				t.Errorf("VoiceType = %q, want azure-standard", def.Audio.Output.VoiceType)
			}
			if def.Audio.Output.VoiceLocale != tt.wantLocale {
				t.Errorf("VoiceLocale = %q, want %q", def.Audio.Output.VoiceLocale, tt.wantLocale)
			}
		})
	}
}

func TestCreateVoiceAgentAPIRequest_PrefersExplicitVoiceLocale(t *testing.T) {
	t.Parallel()
	voiceLocale := "fr-FR"
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "voice-agent"},
		Model:           &Model{Id: "gpt-realtime"},
		Audio: &VoiceAudio{Output: &VoiceAudioOutput{Voice: &VoiceConfig{
			Type: "azure_standard", Name: "en-US-AvaNeural", Locale: &voiceLocale,
		}}},
	}

	req, err := CreateVoiceAgentAPIRequest(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := req.Definition.(agent_api.VoiceAgentDefinition)
	if def.Audio.Output.VoiceLocale != voiceLocale {
		t.Errorf("VoiceLocale = %q, want explicit %q", def.Audio.Output.VoiceLocale, voiceLocale)
	}
}

func TestCreateVoiceAgentAPIRequest_ExplicitOpenAIVoiceLowercasesName(t *testing.T) {
	t.Parallel()
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "voice-agent"},
		Model:           &Model{Id: "gpt-realtime"},
		Audio: &VoiceAudio{Output: &VoiceAudioOutput{Voice: &VoiceConfig{
			Type: "openai", Name: "Shimmer",
		}}},
	}

	req, err := CreateVoiceAgentAPIRequest(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := req.Definition.(agent_api.VoiceAgentDefinition)
	if def.Audio.Output.Voice != "shimmer" {
		t.Errorf("Voice = %q, want shimmer", def.Audio.Output.Voice)
	}
}

func TestCreateVoiceAgentAPIRequest_RejectsInvalidAdvancedConfig(t *testing.T) {
	t.Parallel()
	handoff := map[string]any{"target": "another-agent"}
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "voice-agent"},
		Model:           &Model{Id: "gpt-realtime"},
		Handoff:         handoff,
	}
	_, err := CreateVoiceAgentAPIRequest(agent)
	if err == nil || !strings.Contains(err.Error(), "handoff is not supported by the GA voice agent API") {
		t.Fatalf("expected handoff validation error, got: %v", err)
	}
}

func TestCreateVoiceAgentAPIRequest_LegacyHandoffAndRateBoundaries(t *testing.T) {
	t.Parallel()
	blank := ""
	base := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "voice-agent"},
		Model:           &Model{Id: "gpt-realtime"},
		Handoff:         map[string]any{},
		Audio: &VoiceAudio{Output: &VoiceAudioOutput{Voice: &VoiceConfig{
			Type: "openai", Name: "alloy", Rate: &blank,
		}}},
	}
	_, err := CreateVoiceAgentAPIRequest(base)
	require.NoError(t, err)

	base.Handoff = map[string]any{"target": "agent"}
	_, err = CreateVoiceAgentAPIRequest(base)
	require.ErrorContains(t, err, "handoff is not supported")

	base.Handoff = nil
	base.Audio.Output.Voice.Rate = new("+10%")
	_, err = CreateVoiceAgentAPIRequest(base)
	require.ErrorContains(t, err, "use audio.output.speed")
}

func TestCreateVoiceAgentAPIRequest_DoesNotInheritPcmRateForG711Formats(t *testing.T) {
	t.Parallel()
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "voice-agent"},
		Model:           &Model{Id: "gpt-realtime"},
		Audio: &VoiceAudio{
			Input:  &VoiceAudioInput{Format: &VoiceAudioFormat{Type: "audio/pcmu"}},
			Output: &VoiceAudioOutput{Format: &VoiceAudioFormat{Type: "audio/pcma"}},
		},
	}

	req, err := CreateVoiceAgentAPIRequest(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := req.Definition.(agent_api.VoiceAgentDefinition)
	if def.Audio.Input.Format.Rate != nil {
		t.Fatalf("input G.711 rate = %v, want nil", *def.Audio.Input.Format.Rate)
	}
	if def.Audio.Output.Format.Rate != nil {
		t.Fatalf("output G.711 rate = %v, want nil", *def.Audio.Output.Format.Rate)
	}
}

func TestCreateVoiceAgentAPIRequest_MarshalServiceWireShape(t *testing.T) {
	t.Parallel()
	voice := "en-US-Ava:DragonHDLatestNeural"
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "voice-agent"},
		Model:           &Model{Id: "gpt-realtime"},
		Voice:           &voice,
	}

	req, err := CreateVoiceAgentAPIRequest(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	definition, ok := wire["definition"].(map[string]any)
	if !ok {
		t.Fatalf("definition = %#v, want object", wire["definition"])
	}
	audio, ok := definition["audio"].(map[string]any)
	if !ok {
		t.Fatalf("definition.audio = %#v, want object", definition["audio"])
	}
	output, ok := audio["output"].(map[string]any)
	if !ok {
		t.Fatalf("definition.audio.output = %#v, want object", audio["output"])
	}

	if got, ok := output["voice"].(string); !ok || got != voice {
		t.Fatalf("audio.output.voice = %#v, want string %q", output["voice"], voice)
	}
	if got := output["voice_type"]; got != "azure-standard" {
		t.Fatalf("audio.output.voice_type = %#v, want azure-standard", got)
	}
	if got := output["voice_locale"]; got != "en-US" {
		t.Fatalf("audio.output.voice_locale = %#v, want en-US", got)
	}
	if _, exists := output["type"]; exists {
		t.Fatalf("audio.output.type should not be present in service wire shape: %#v", output)
	}
}

func TestCreateVoiceAgentAPIRequest_AdvancedSettingsWireShape(t *testing.T) {
	t.Parallel()
	outRate := 24000
	threshold := 0.6
	speechDurationMs := 120
	createResponse := true
	removeFillerWords := true
	interruptResponse := true
	autoTruncate := true
	speed := 1.1
	style := "cheerful"
	pitch := "+0Hz"
	volume := "+0%"
	parallelToolCalls := true
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "voice-advanced"},
		Model:           &Model{Id: "gpt-realtime"},
		Instructions:    new("You are {{persona}}, a concise voice assistant."),
		StructuredInputs: map[string]any{
			"persona": map[string]any{"description": "Assistant persona", "defaultValue": "Ada"},
		},
		Audio: &VoiceAudio{
			Input: &VoiceAudioInput{
				Format:           &VoiceAudioFormat{Type: "audio/pcmu"},
				NoiseReduction:   &VoiceNoiseReduction{Type: "near_field"},
				EchoCancellation: map[string]any{"type": "server_echo_cancellation", "channels": 1},
				TurnDetection: &VoiceTurnDetection{
					Type:              "azure_semantic_vad",
					Threshold:         &threshold,
					SpeechDurationMs:  &speechDurationMs,
					CreateResponse:    &createResponse,
					RemoveFillerWords: &removeFillerWords,
					InterruptResponse: &interruptResponse,
					Languages:         []string{"en-US"},
					AutoTruncate:      &autoTruncate,
				},
				Transcription: &VoiceTranscription{
					Model: "azure-speech", Language: new("en-US"), Prompt: new("Contoso terms"),
				},
			},
			Output: &VoiceAudioOutput{
				Format: &VoiceAudioFormat{Type: "audio/pcm", Rate: &outRate},
				Voice: &VoiceConfig{
					Type: "azure_standard", Name: "en-US-AvaNeural", Style: &style,
					Pitch: &pitch, Locale: new("en-US"), Volume: &volume,
				},
				Speed: &speed,
			},
		},
		OutputModalities:  []string{"audio", "text"},
		Tools:             []map[string]any{{"type": "system", "name": "end_conversation"}},
		Avatar:            map[string]any{"type": "video_avatar", "character": "lisa", "output_protocol": "webrtc"},
		Greeting:          map[string]any{"type": "template", "text": "Hello {{persona}}"},
		ToolChoice:        "auto",
		ParallelToolCalls: &parallelToolCalls,
		MaxOutputTokens:   "inf",
		Include:           []string{"item.input_audio_transcription.phrases"},
	}

	req, err := CreateVoiceAgentAPIRequest(agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	def, ok := wire["definition"].(map[string]any)
	if !ok {
		t.Fatalf("definition = %#v, want object", wire["definition"])
	}
	audio, ok := def["audio"].(map[string]any)
	if !ok {
		t.Fatalf("definition.audio = %#v, want object", def["audio"])
	}
	input, ok := audio["input"].(map[string]any)
	if !ok {
		t.Fatalf("definition.audio.input = %#v, want object", audio["input"])
	}
	output, ok := audio["output"].(map[string]any)
	if !ok {
		t.Fatalf("definition.audio.output = %#v, want object", audio["output"])
	}
	structuredInputs, ok := def["structured_inputs"].(map[string]any)
	if !ok {
		t.Fatalf("definition.structured_inputs = %#v, want object", def["structured_inputs"])
	}
	structured, ok := structuredInputs["persona"].(map[string]any)
	if !ok {
		t.Fatalf("definition.structured_inputs.persona = %#v, want object", structuredInputs["persona"])
	}

	if structured["default_value"] != "Ada" || structured["defaultValue"] != nil {
		t.Fatalf("structured input default was not mapped to wire shape: %#v", structured)
	}
	if output["voice"] != "en-US-AvaNeural" || output["voice_type"] != "azure-standard" || output["style"] != style {
		t.Fatalf("output voice flat shape not mapped: %#v", output)
	}
	echoCancellation, ok := input["echo_cancellation"].(map[string]any)
	if !ok {
		t.Fatalf("echo cancellation = %#v, want object", input["echo_cancellation"])
	}
	if echoCancellation["type"] != "server_echo_cancellation" {
		t.Fatalf("echo cancellation not mapped: %#v", input["echo_cancellation"])
	}
	if def["tool_choice"] != "auto" || def["max_output_tokens"] != "inf" {
		t.Fatalf("response options not mapped: %#v", def)
	}
	if output["speed"] != speed || output["rate"] != nil {
		t.Fatalf("output speed/rate not mapped to GA shape: %#v", output)
	}
	tools, ok := def["tools"].([]any)
	if !ok {
		t.Fatalf("tools = %#v, want array", def["tools"])
	}
	avatar, ok := def["avatar"].(map[string]any)
	if !ok {
		t.Fatalf("avatar = %#v, want object", def["avatar"])
	}
	if len(tools) != 1 || avatar["character"] != "lisa" {
		t.Fatalf("tools/avatar not mapped: %#v", def)
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

func TestCreateHostedVoiceAgentAPIRequest_LegacyAuthoringRejected(t *testing.T) {
	t.Parallel()
	store := false
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "voice-wrapper"},
		ModelType:       VoiceModelTypeHostedAgent,
		TargetAgent:     &VoiceTargetAgent{Service: "voice-target", Version: "deployed"},
		Store:           &store,
	}
	_, err := CreateHostedVoiceAgentAPIRequest(agent, agent_api.VoiceTargetAgentReference{
		Name:    "deployed-target",
		Version: "7",
	})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected legacy hosted voice authoring error, got: %v", err)
	}
}

func TestCreateHostedVoiceAgentAPIRequest_ConversationEngineWireShape(t *testing.T) {
	t.Parallel()
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindVoice, Name: "voice"},
		ConversationEngine: &VoiceConversationEngine{
			Type: "hosted_agent",
			Name: "voice-target",
		},
		Greeting: map[string]any{"type": "template", "text": "Hello"},
	}
	req, err := CreateHostedVoiceAgentAPIRequest(agent, agent_api.VoiceTargetAgentReference{
		Name:    "deployed-target",
		Version: "7",
	})
	require.NoError(t, err)
	data, err := json.Marshal(req)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(data, &wire))
	definitionValue, ok := wire["definition"]
	require.True(t, ok, "wire payload is missing definition: %s", data)
	definition, ok := definitionValue.(map[string]any)
	require.True(t, ok, "definition has type %T, want object: %s", definitionValue, data)
	require.NotContains(t, definition, "model_type")
	require.NotContains(t, definition, "target_agent")
	require.Equal(t, map[string]any{
		"type":    "hosted_agent",
		"name":    "deployed-target",
		"version": "7",
	}, definition["conversation_engine"])
}

func TestCreateHostedVoiceAgentAPIRequestRequiresResolvedTarget(t *testing.T) {
	t.Parallel()
	agent := VoiceAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPromptVoice, Name: "voice-wrapper"},
		ConversationEngine: &VoiceConversationEngine{
			Type: "hosted_agent",
			Name: "voice-target",
		},
		Greeting: map[string]any{"type": "template", "text": "Hello"},
	}
	if _, err := CreateHostedVoiceAgentAPIRequest(agent, agent_api.VoiceTargetAgentReference{}); err == nil {
		t.Fatal("expected missing resolved target error")
	}
}
