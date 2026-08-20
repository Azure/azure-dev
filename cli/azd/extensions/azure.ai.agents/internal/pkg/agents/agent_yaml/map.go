// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_api"

	"go.yaml.in/yaml/v3"
)

// RuntimeCmdPrefix returns the command prefix for a given runtime string.
// For example, "python_3_12" -> "python", "dotnet_9" -> "dotnet".
func RuntimeCmdPrefix(runtime string) string {
	if strings.HasPrefix(runtime, "dotnet_") {
		return "dotnet"
	}
	return "python"
}

// AgentBuildOption represents an option for building agent definitions
type AgentBuildOption func(*AgentBuildConfig)

// AgentBuildConfig holds configuration for building agent definitions
type AgentBuildConfig struct {
	ImageURL             string
	CPU                  string
	Memory               string
	EnvironmentVariables map[string]string
	// Add other build-time options here as needed
}

// WithImageURL sets the image URL for hosted agents
func WithImageURL(url string) AgentBuildOption {
	return func(config *AgentBuildConfig) {
		config.ImageURL = url
	}
}

// WithCPU sets the CPU allocation for hosted agents
func WithCPU(cpu string) AgentBuildOption {
	return func(config *AgentBuildConfig) {
		config.CPU = cpu
	}
}

// WithMemory sets the memory allocation for hosted agents
func WithMemory(memory string) AgentBuildOption {
	return func(config *AgentBuildConfig) {
		config.Memory = memory
	}
}

// WithEnvironmentVariable sets an environment variable for hosted agents
func WithEnvironmentVariable(key, value string) AgentBuildOption {
	return func(config *AgentBuildConfig) {
		if config.EnvironmentVariables == nil {
			config.EnvironmentVariables = make(map[string]string)
		}
		config.EnvironmentVariables[key] = value
	}
}

// WithEnvironmentVariables sets multiple environment variables for hosted agents
func WithEnvironmentVariables(envVars map[string]string) AgentBuildOption {
	return func(config *AgentBuildConfig) {
		if config.EnvironmentVariables == nil {
			config.EnvironmentVariables = make(map[string]string)
		}
		maps.Copy(config.EnvironmentVariables, envVars)
	}
}

func constructBuildConfig(options ...AgentBuildOption) *AgentBuildConfig {
	config := &AgentBuildConfig{}
	for _, option := range options {
		option(config)
	}
	return config
}

// mapRaiConfig flattens the manifest-level policies list into the data-plane
// rai_config field. It returns the RAI config derived from the first policy of
// type "rai_policy" that has a policy name, or nil when none is configured.
func mapRaiConfig(policies []Policy) *agent_api.RaiConfig {
	for _, policy := range policies {
		if policy.Type == PolicyTypeRai && policy.RaiPolicyName != "" {
			return &agent_api.RaiConfig{
				RaiPolicyName:         policy.RaiPolicyName,
				InvocationsModeration: mapInvocationsModeration(policy.InvocationsModeration),
			}
		}
	}
	return nil
}

// mapInvocationsModeration translates the YAML invocations-moderation block into its
// data-plane representation. It returns nil when the block is absent so agents that do not
// configure it serialize exactly as before.
func mapInvocationsModeration(moderation *InvocationsModeration) *agent_api.InvocationsModeration {
	if moderation == nil {
		return nil
	}

	mapped := &agent_api.InvocationsModeration{
		InputContentType:  agent_api.RaiInvocationContentType(moderation.InputContentType),
		OutputContentType: agent_api.RaiInvocationContentType(moderation.OutputContentType),
		ResponseMode:      agent_api.RaiInvocationMode(moderation.ResponseMode),
		InputPaths:        slices.Clone(moderation.InputPaths),
		OutputPaths:       slices.Clone(moderation.OutputPaths),
	}

	for _, selector := range moderation.StreamSelectors {
		mapped.StreamSelectors = append(mapped.StreamSelectors, agent_api.SseTextSelector{
			EventType: selector.EventType,
			TextField: selector.TextField,
		})
	}

	return mapped
}

// MapEndpointAndCard maps YAML-layer endpoint and card fields to API model types
// without requiring or validating the full agent definition. This is used by the
// endpoint update command where only endpoint/card patching is needed.
func MapEndpointAndCard(
	agentEndpoint *AgentEndpoint,
	agentCard *AgentCard,
) (*agent_api.AgentEndpoint, *agent_api.AgentCard, error) {
	// Reuse createAgentAPIRequest with a minimal definition to get
	// endpoint/card mapping only.
	req, err := createAgentAPIRequest(
		AgentDefinition{Name: "placeholder"},
		nil,
		agentEndpoint,
		agentCard,
	)
	if err != nil {
		return nil, nil, err
	}
	return req.AgentEndpoint, req.AgentCard, nil
}

// CreateAgentAPIRequestFromDefinition creates a CreateAgentRequest from AgentDefinition with strong typing
func CreateAgentAPIRequestFromDefinition(agentTemplate any, options ...AgentBuildOption) (*agent_api.CreateAgentRequest, error) {
	buildConfig := constructBuildConfig(options...)

	templateBytes, _ := yaml.Marshal(agentTemplate)

	var agentDef AgentDefinition
	if err := yaml.Unmarshal(templateBytes, &agentDef); err != nil {
		return nil, fmt.Errorf("failed to parse template to determine agent kind while creating api request")
	}

	// Route to appropriate handler based on agent kind
	switch agentDef.Kind {
	case AgentKindHosted:
		hostedDef := agentTemplate.(ContainerAgent)
		return CreateHostedAgentAPIRequest(hostedDef, buildConfig)
	case AgentKindPromptVoice:
		voiceDef := agentTemplate.(VoiceAgent)
		return CreateVoiceAgentAPIRequest(voiceDef)
	default:
		return nil, fmt.Errorf("unsupported agent kind: %s. Supported kinds are: hosted, prompt-voice", agentDef.Kind)
	}
}

// convertYamlToolsToApiTools converts agent_yaml tools to agent_api tools
func convertYamlToolsToApiTools(yamlTools []any) []any {
	var apiTools []any

	for _, yamlTool := range yamlTools {
		apiTool, err := convertYamlToolToApiTool(yamlTool)
		if err != nil {
			// Log error and skip this tool instead of failing completely
			continue
		}
		apiTools = append(apiTools, apiTool)
	}

	return apiTools
}

// convertYamlToolToApiTool converts a single agent_yaml tool to its corresponding agent_api tool type
func convertYamlToolToApiTool(yamlTool any) (any, error) {
	if yamlTool == nil {
		return nil, fmt.Errorf("tool cannot be nil")
	}

	switch tool := yamlTool.(type) {
	case FunctionTool:
		return agent_api.FunctionTool{
			Tool: agent_api.Tool{
				Type: agent_api.ToolTypeFunction,
			},
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  convertPropertySchemaToInterface(tool.Parameters),
			Strict:      tool.Strict,
		}, nil

	case WebSearchTool:
		apiTool := agent_api.WebSearchPreviewTool{
			Tool: agent_api.Tool{
				Type: agent_api.ToolTypeWebSearchPreview,
			},
		}
		// Extract options back to specific fields
		if tool.Options != nil {
			if userLocation, exists := (tool.Options)["userLocation"]; exists {
				if loc, ok := userLocation.(*agent_api.Location); ok {
					apiTool.UserLocation = loc
				}
			}
			if searchContextSize, exists := (tool.Options)["searchContextSize"]; exists {
				if size, ok := searchContextSize.(string); ok {
					apiTool.SearchContextSize = &size
				}
			}
		}
		return apiTool, nil

	case BingGroundingTool:
		apiTool := agent_api.BingGroundingAgentTool{
			Tool: agent_api.Tool{
				Type: agent_api.ToolTypeBingGrounding,
			},
		}
		// Extract bingGrounding from options
		if tool.Options != nil {
			if bingGrounding, exists := (tool.Options)["bingGrounding"]; exists {
				if bg, ok := bingGrounding.(agent_api.BingGroundingSearchToolParameters); ok {
					apiTool.BingGrounding = bg
				}
			}
		}
		return apiTool, nil

	case FileSearchTool:
		maxResults, err := convertIntToInt32(tool.MaximumResultCount)
		if err != nil {
			return nil, fmt.Errorf("file_search maximumResultCount: %w", err)
		}
		apiTool := agent_api.FileSearchTool{
			Tool: agent_api.Tool{
				Type: agent_api.ToolTypeFileSearch,
			},
			VectorStoreIds: tool.VectorStoreIds,
			MaxNumResults:  maxResults,
		}

		// Set ranking options
		if tool.Ranker != nil || tool.ScoreThreshold != nil {
			apiTool.RankingOptions = &agent_api.RankingOptions{
				Ranker:         tool.Ranker,
				ScoreThreshold: convertFloat64ToFloat32(tool.ScoreThreshold),
			}
		}

		// Extract filters from options
		if tool.Options != nil {
			if filters, exists := tool.Options["filters"]; exists {
				apiTool.Filters = filters
			}
		}
		return apiTool, nil

	case McpTool:
		apiTool := agent_api.MCPTool{
			Tool: agent_api.Tool{
				Type: agent_api.ToolTypeMCP,
			},
			ServerLabel: tool.ServerName,
			ServerURL:   tool.URL,
		}
		if projectConnectionID := projectConnectionIDFromMcpConnection(tool.Connection); projectConnectionID != "" {
			apiTool.ProjectConnectionID = &projectConnectionID
		}

		// Extract options back to specific fields
		if tool.Options != nil {
			if serverURL, exists := tool.Options["serverUrl"]; exists {
				if url, ok := serverURL.(string); ok {
					apiTool.ServerURL = url
				}
			}
			if headers, exists := tool.Options["headers"]; exists {
				if h, ok := headers.(map[string]string); ok {
					apiTool.Headers = h
				}
			}
			if allowedTools, exists := tool.Options["allowedTools"]; exists {
				apiTool.AllowedTools = allowedTools
			}
			if requireApproval, exists := tool.Options["requireApproval"]; exists {
				apiTool.RequireApproval = requireApproval
			}
			if projectConnectionId, exists := tool.Options["projectConnectionId"]; exists {
				if id, ok := projectConnectionId.(string); ok {
					apiTool.ProjectConnectionID = &id
				}
			}
		}
		return apiTool, nil

	case OpenApiTool:
		apiTool := agent_api.OpenApiAgentTool{
			Tool: agent_api.Tool{
				Type: agent_api.ToolTypeOpenAPI,
			},
		}

		// Extract openapi from options
		if tool.Options != nil {
			if openapi, exists := tool.Options["openapi"]; exists {
				if api, ok := openapi.(agent_api.OpenApiFunctionDefinition); ok {
					apiTool.OpenAPI = api
				}
			}
		}
		return apiTool, nil

	case CodeInterpreterTool:
		apiTool := agent_api.CodeInterpreterTool{
			Tool: agent_api.Tool{
				Type: agent_api.ToolTypeCodeInterpreter,
			},
		}

		// Extract container from options
		if tool.Options != nil {
			if container, exists := tool.Options["container"]; exists {
				apiTool.Container = container
			}
		}
		return apiTool, nil

	default:
		return nil, fmt.Errorf("unsupported YAML tool type: %T", yamlTool)
	}
}

func projectConnectionIDFromMcpConnection(connection any) string {
	switch conn := connection.(type) {
	case ReferenceConnection:
		return conn.Name
	case RemoteConnection:
		return conn.Name
	case map[string]any:
		if name, ok := conn["name"].(string); ok {
			return name
		}
	}

	return ""
}

// Helper function to convert PropertySchema to interface{} for agent_api
func convertPropertySchemaToInterface(schema PropertySchema) any {
	// This is a placeholder implementation - would need to convert PropertySchema
	// back to the original format expected by agent_api
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

// Helper function to convert *int to *int32
func convertIntToInt32(i *int) (*int32, error) {
	if i == nil {
		return nil, nil
	}
	if *i > math.MaxInt32 || *i < math.MinInt32 {
		return nil, fmt.Errorf("value %d overflows int32 range", *i)
	}
	i32 := int32(*i)
	return &i32, nil
}

// Helper function to convert *float64 to *float32
func convertFloat64ToFloat32(f64 *float64) *float32 {
	if f64 == nil {
		return nil
	}
	f32 := float32(*f64)
	return &f32
}

// mapSessionConfiguration converts the author-facing session configuration into
// the API shape, validating the idle-timeout bounds. It returns nil when the
// author omitted session configuration so session_configuration is left out of
// the request and the service applies its default.
func mapSessionConfiguration(sc *SessionConfiguration) (*agent_api.SessionConfigurationAPI, error) {
	if sc == nil || sc.IdleTimeoutSeconds == nil {
		return nil, nil
	}

	idle := *sc.IdleTimeoutSeconds
	if idle < MinSessionIdleTimeoutSeconds || idle > MaxSessionIdleTimeoutSeconds {
		return nil, fmt.Errorf(
			"session idle timeout must be between %d and %d seconds, got %d "+
				"('sessionConfiguration.idleTimeoutSeconds' in azure.yaml, "+
				"'session_configuration.idle_timeout_seconds' in agent.yaml)",
			MinSessionIdleTimeoutSeconds, MaxSessionIdleTimeoutSeconds, idle)
	}

	return &agent_api.SessionConfigurationAPI{IdleTimeoutSeconds: idle}, nil
}

// CreateHostedAgentAPIRequest creates a CreateAgentRequest for hosted agents
func CreateHostedAgentAPIRequest(hostedAgent ContainerAgent, buildConfig *AgentBuildConfig) (*agent_api.CreateAgentRequest, error) {
	imageURL := hostedAgent.Image
	cpu := "1"      // Default CPU
	memory := "2Gi" // Default memory
	envVars := make(map[string]string)

	if buildConfig != nil {
		if buildConfig.ImageURL != "" {
			imageURL = buildConfig.ImageURL
		}
		if buildConfig.CPU != "" {
			cpu = buildConfig.CPU
		}
		if buildConfig.Memory != "" {
			memory = buildConfig.Memory
		}
		if buildConfig.EnvironmentVariables != nil {
			envVars = buildConfig.EnvironmentVariables
		}
	}

	// Map protocol versions from the hosted agent definition
	protocolVersions := make([]agent_api.ProtocolVersionRecord, 0)
	if len(hostedAgent.Protocols) > 0 {
		for _, protocol := range hostedAgent.Protocols {
			protocolVersions = append(protocolVersions, agent_api.ProtocolVersionRecord{
				Protocol: agent_api.AgentProtocol(protocol.Protocol),
				Version:  protocol.Version,
			})
		}
	} else {
		// Set default protocol versions if none specified
		protocolVersions = []agent_api.ProtocolVersionRecord{
			{Protocol: agent_api.AgentProtocolResponses, Version: "2.0.0"},
		}
	}

	// Map optional session configuration (validated); nil when omitted so the
	// service applies its default.
	sessionConfig, err := mapSessionConfiguration(hostedAgent.SessionConfiguration)
	if err != nil {
		return nil, err
	}

	// Code deploy path
	if hostedAgent.CodeConfiguration != nil {
		cmdPrefix := RuntimeCmdPrefix(hostedAgent.CodeConfiguration.Runtime)
		entryPoint := []string{cmdPrefix, hostedAgent.CodeConfiguration.EntryPoint}
		// Foundry requires dependency_resolution for code deploy; default to
		// remote_build (matching `azd ai agent init --dep-resolution`) when the
		// author omits it, so the create-agent request isn't rejected with a 400.
		depRes := DefaultDependencyResolution
		if hostedAgent.CodeConfiguration.DependencyResolution != nil &&
			*hostedAgent.CodeConfiguration.DependencyResolution != "" {
			depRes = *hostedAgent.CodeConfiguration.DependencyResolution
		}

		codeDef := agent_api.HostedAgentDefinition{
			AgentDefinition: agent_api.AgentDefinition{
				Kind:      agent_api.AgentKindHosted,
				RaiConfig: mapRaiConfig(hostedAgent.Policies),
			},
			ProtocolVersions:     protocolVersions,
			CPU:                  cpu,
			Memory:               memory,
			EnvironmentVariables: envVars,
			CodeConfiguration: &agent_api.CodeConfigurationAPI{
				Runtime:              hostedAgent.CodeConfiguration.Runtime,
				EntryPoint:           entryPoint,
				DependencyResolution: depRes,
			},
			SessionConfiguration: sessionConfig,
		}

		return createAgentAPIRequest(hostedAgent.AgentDefinition, codeDef,
			hostedAgent.AgentEndpoint, hostedAgent.AgentCard)
	}

	// Container/image deploy path
	if imageURL == "" {
		return nil, fmt.Errorf("image URL is required for hosted agents - use WithImageURL build option or specify in container.image")
	}

	imageDef := agent_api.HostedAgentDefinition{
		AgentDefinition: agent_api.AgentDefinition{
			Kind:      agent_api.AgentKindHosted,
			RaiConfig: mapRaiConfig(hostedAgent.Policies),
		},
		ProtocolVersions:     protocolVersions,
		CPU:                  cpu,
		Memory:               memory,
		EnvironmentVariables: envVars,
		ContainerConfiguration: &agent_api.ContainerConfigurationAPI{
			Image: imageURL,
		},
		SessionConfiguration: sessionConfig,
	}

	return createAgentAPIRequest(hostedAgent.AgentDefinition, imageDef,
		hostedAgent.AgentEndpoint, hostedAgent.AgentCard)
}

// Default audio-pipeline values for a voice agent. Authors don't specify the
// audio block in v1; these mirror the Voice Live sample (PCM16 @ 24 kHz, server
// VAD turn detection, input transcription enabled).
const (
	defaultVoiceAudioType         = "audio/pcm"
	defaultVoiceAudioRate         = 24000
	defaultVoiceTurnDetectionType = "server_vad"
	defaultVoiceInstructions      = "You are a helpful voice assistant. Respond naturally and concisely."
	// defaultVoiceInputTranscriptionModel enables user-speech transcription events.
	// azure-speech is accepted by both realtime and cascaded voice pipelines.
	defaultVoiceInputTranscriptionModel = "azure-speech"
	// defaultVoiceName is a DragonHD (HD) Azure Neural voice used when the author
	// omits a voice.
	defaultVoiceName = "en-US-Ava:DragonHDLatestNeural"
)

// knownOpenAIVoices is the set of OpenAI realtime voice names accepted by the
// data-plane voice service. OpenAI voices are single lowercase tokens; Azure
// Neural voices are locale-prefixed (see azureNeuralVoicePattern). Keep this in
// sync with the service's supported voice list.
var knownOpenAIVoices = map[string]struct{}{
	"alloy":   {},
	"ash":     {},
	"ballad":  {},
	"coral":   {},
	"echo":    {},
	"sage":    {},
	"shimmer": {},
	"verse":   {},
}

// azureNeuralVoicePattern matches the locale prefix that every Azure Neural
// voice name carries, e.g. "en-US-Ava:DragonHDLatestNeural" or
// "ja-JP-NanamiNeural". The service contract guarantees this <lang>-<REGION>-
// shape for Azure voices, which is what distinguishes them from the flat
// lowercase OpenAI voice tokens.
var azureNeuralVoicePattern = regexp.MustCompile(`^[a-z]{2,3}-[A-Z]{2,3}-`)

// isOpenAIVoice reports whether a voice name denotes an OpenAI realtime voice
// (e.g. "alloy") vs an Azure Neural voice (e.g. "en-US-Ava:DragonHDLatestNeural").
// It first matches the explicit known-OpenAI set, then falls back to structure:
// anything lacking the Azure Neural locale prefix is treated as OpenAI. This is
// deliberately stricter than a plain "contains '-'" check so that a partly
// specified or future name is classified by its actual shape.
func isOpenAIVoice(name string) bool {
	if _, ok := knownOpenAIVoices[strings.ToLower(strings.TrimSpace(name))]; ok {
		return true
	}
	return !azureNeuralVoicePattern.MatchString(name)
}

// buildVoiceConfig chooses the OpenAI vs Azure voice type by name shape.
//
// OpenAI realtime voice IDs are lowercase on the wire, and the classifier
// already matches them case-insensitively, so normalize to lowercase to keep
// e.g. "--voice Shimmer" from being emitted as "Shimmer". Azure Neural voice
// names are case-sensitive (e.g. "en-US-Ava:DragonHDLatestNeural"), so only the
// surrounding whitespace is trimmed for those.
func buildVoiceConfig(name string) *agent_api.VoiceConfig {
	trimmed := strings.TrimSpace(name)
	if isOpenAIVoice(trimmed) {
		return &agent_api.VoiceConfig{Type: "openai", Name: strings.ToLower(trimmed)}
	}
	return &agent_api.VoiceConfig{Type: "azure_standard", Name: trimmed}
}

func flatVoiceType(voice *agent_api.VoiceConfig) string {
	if voice == nil {
		return ""
	}
	if voice.Type == "azure_standard" {
		return "azure-standard"
	}
	return voice.Type
}

func normalizeFlatVoiceType(voiceType string) string {
	switch strings.TrimSpace(voiceType) {
	case "azure_standard":
		return "azure-standard"
	default:
		return strings.TrimSpace(voiceType)
	}
}

func flatVoiceLocale(voice *agent_api.VoiceConfig) string {
	if voice == nil || voice.Name == "" || isOpenAIVoice(voice.Name) {
		return ""
	}
	if voice.Locale != nil && strings.TrimSpace(*voice.Locale) != "" {
		return strings.TrimSpace(*voice.Locale)
	}
	parts := strings.SplitN(voice.Name, "-", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "-" + parts[1]
}

func defaultVoiceAudioFormat() *agent_api.VoiceAudioFormat {
	rate := defaultVoiceAudioRate
	return &agent_api.VoiceAudioFormat{Type: defaultVoiceAudioType, Rate: &rate}
}

func mapVoiceAudioFormat(format *VoiceAudioFormat, fallback *agent_api.VoiceAudioFormat) *agent_api.VoiceAudioFormat {
	out := &agent_api.VoiceAudioFormat{}
	if fallback != nil {
		*out = *fallback
	}
	if format != nil {
		if strings.TrimSpace(format.Type) != "" {
			out.Type = strings.TrimSpace(format.Type)
		}
		if format.Rate != nil {
			out.Rate = format.Rate
		}
	}
	return out
}

func mapVoiceTurnDetection(turnDetection *VoiceTurnDetection) *agent_api.VoiceTurnDetection {
	out := &agent_api.VoiceTurnDetection{Type: defaultVoiceTurnDetectionType}
	if turnDetection == nil {
		return out
	}
	if strings.TrimSpace(turnDetection.Type) != "" {
		out.Type = strings.TrimSpace(turnDetection.Type)
	}
	out.Threshold = turnDetection.Threshold
	out.PrefixPaddingMs = turnDetection.PrefixPaddingMs
	out.SilenceDurationMs = turnDetection.SilenceDurationMs
	out.CreateResponse = turnDetection.CreateResponse
	out.Eagerness = turnDetection.Eagerness
	out.SpeechDurationMs = turnDetection.SpeechDurationMs
	out.RemoveFillerWords = turnDetection.RemoveFillerWords
	out.InterruptResponse = turnDetection.InterruptResponse
	out.Languages = turnDetection.Languages
	out.AutoTruncate = turnDetection.AutoTruncate
	return out
}

func mapVoiceTranscription(transcription *VoiceTranscription) *agent_api.VoiceTranscription {
	out := &agent_api.VoiceTranscription{Model: defaultVoiceInputTranscriptionModel}
	if transcription == nil {
		return out
	}
	if strings.TrimSpace(transcription.Model) != "" {
		out.Model = strings.TrimSpace(transcription.Model)
	}
	out.Language = transcription.Language
	out.Prompt = transcription.Prompt
	return out
}

func mapVoiceConfig(voice *VoiceConfig, fallbackName string) *agent_api.VoiceConfig {
	if voice == nil {
		return buildVoiceConfig(fallbackName)
	}
	name := strings.TrimSpace(voice.Name)
	if name == "" {
		name = fallbackName
	}
	voiceType := strings.TrimSpace(voice.Type)
	if voiceType == "" {
		out := buildVoiceConfig(name)
		out.Style = voice.Style
		out.Pitch = voice.Pitch
		out.Rate = voice.Rate
		out.Locale = voice.Locale
		out.Volume = voice.Volume
		return out
	}
	return &agent_api.VoiceConfig{
		Type:   voiceType,
		Name:   name,
		Style:  voice.Style,
		Pitch:  voice.Pitch,
		Rate:   voice.Rate,
		Locale: voice.Locale,
		Volume: voice.Volume,
	}
}

func mapVoiceStructuredInputs(inputs map[string]any) map[string]any {
	if len(inputs) == 0 {
		return nil
	}
	out := make(map[string]any, len(inputs))
	for name, input := range inputs {
		inputMap, ok := input.(map[string]any)
		if !ok {
			out[name] = input
			continue
		}

		mapped := maps.Clone(inputMap)
		if value, ok := mapped["defaultValue"]; ok {
			if _, hasSnakeCase := mapped["default_value"]; !hasSnakeCase {
				mapped["default_value"] = value
			}
			delete(mapped, "defaultValue")
		}
		out[name] = mapped
	}
	return out
}

// CreateVoiceAgentAPIRequest builds a CreateAgentRequest for a declarative
// voice agent. It translates the authoring kind "prompt-voice" into the
// data-plane service kind "voice" and defaults the audio pipeline.
func CreateVoiceAgentAPIRequest(voiceAgent VoiceAgent) (*agent_api.CreateAgentRequest, error) {
	return createVoiceAgentAPIRequest(voiceAgent, false)
}

// CreateVoiceAgentAPIRequestFlat builds a CreateAgentRequest using the newer
// TiP/unified API flat output voice shape.
func CreateVoiceAgentAPIRequestFlat(voiceAgent VoiceAgent) (*agent_api.CreateAgentRequest, error) {
	return createVoiceAgentAPIRequest(voiceAgent, true)
}

func createVoiceAgentAPIRequest(voiceAgent VoiceAgent, flatOutput bool) (*agent_api.CreateAgentRequest, error) {
	modelID := ""
	if voiceAgent.Model != nil {
		modelID = strings.TrimSpace(voiceAgent.Model.Id)
	}
	if modelID == "" {
		return nil, fmt.Errorf("model.id is required for a prompt-voice agent")
	}

	modelType := agent_api.VoiceModelTypeManaged
	if voiceAgent.ModelType != "" {
		modelType = agent_api.VoiceModelType(voiceAgent.ModelType)
	}
	if modelType != agent_api.VoiceModelTypeManaged && modelType != agent_api.VoiceModelTypeSelfDeployed {
		return nil, fmt.Errorf(
			"model_type '%s' is not supported; use '%s' or '%s'",
			voiceAgent.ModelType, VoiceModelTypeManaged, VoiceModelTypeSelfDeployed)
	}

	instructions := defaultVoiceInstructions
	if voiceAgent.Instructions != nil && *voiceAgent.Instructions != "" {
		instructions = *voiceAgent.Instructions
	}

	voiceName := defaultVoiceName
	if voiceAgent.Voice != nil && *voiceAgent.Voice != "" {
		voiceName = *voiceAgent.Voice
	}

	inputFormat := defaultVoiceAudioFormat()
	outputFormat := defaultVoiceAudioFormat()
	turnDetection := mapVoiceTurnDetection(nil)
	transcription := mapVoiceTranscription(nil)
	var noiseReduction *agent_api.VoiceNoiseReduction
	var echoCancellation map[string]any
	outputVoice := buildVoiceConfig(voiceName)
	var outputSpeed *float64
	if voiceAgent.Audio != nil {
		if voiceAgent.Audio.Input != nil {
			inputFormat = mapVoiceAudioFormat(voiceAgent.Audio.Input.Format, inputFormat)
			if voiceAgent.Audio.Input.NoiseReduction != nil {
				noiseReduction = &agent_api.VoiceNoiseReduction{Type: strings.TrimSpace(voiceAgent.Audio.Input.NoiseReduction.Type)}
			}
			echoCancellation = voiceAgent.Audio.Input.EchoCancellation
			turnDetection = mapVoiceTurnDetection(voiceAgent.Audio.Input.TurnDetection)
			transcription = mapVoiceTranscription(voiceAgent.Audio.Input.Transcription)
		}
		if voiceAgent.Audio.Output != nil {
			outputFormat = mapVoiceAudioFormat(voiceAgent.Audio.Output.Format, outputFormat)
			outputVoice = mapVoiceConfig(voiceAgent.Audio.Output.Voice, voiceName)
			outputSpeed = voiceAgent.Audio.Output.Speed
		}
	}

	outputModalities := []string{"audio"}
	if len(voiceAgent.OutputModalities) > 0 {
		outputModalities = voiceAgent.OutputModalities
	}

	input := &agent_api.VoiceInputConfig{
		Format:           inputFormat,
		NoiseReduction:   noiseReduction,
		EchoCancellation: echoCancellation,
		TurnDetection:    turnDetection,
		Transcription:    transcription,
	}
	if flatOutput {
		voiceDef := agent_api.VoiceAgentDefinitionFlat{
			AgentDefinition: agent_api.AgentDefinition{
				// Translate authoring kind prompt-voice -> service kind voice.
				Kind: agent_api.AgentKindVoice,
			},
			ModelType:        modelType,
			Model:            modelID,
			Instructions:     instructions,
			StructuredInputs: mapVoiceStructuredInputs(voiceAgent.StructuredInputs),
			Audio: &agent_api.VoiceAudioConfigFlat{
				Input: input,
				Output: &agent_api.VoiceOutputConfigFlat{
					Format:      outputFormat,
					Voice:       outputVoice.Name,
					VoiceType:   normalizeFlatVoiceType(flatVoiceType(outputVoice)),
					VoiceLocale: flatVoiceLocale(outputVoice),
					Style:       outputVoice.Style,
					Pitch:       outputVoice.Pitch,
					Rate:        outputVoice.Rate,
					Volume:      outputVoice.Volume,
					Speed:       outputSpeed,
				},
			},
			OutputModalities:  outputModalities,
			Store:             voiceAgent.Store,
			Tools:             voiceAgent.Tools,
			Avatar:            voiceAgent.Avatar,
			Greeting:          voiceAgent.Greeting,
			Handoff:           voiceAgent.Handoff,
			ToolChoice:        voiceAgent.ToolChoice,
			ParallelToolCalls: voiceAgent.ParallelToolCalls,
			MaxOutputTokens:   voiceAgent.MaxOutputTokens,
			Include:           voiceAgent.Include,
		}

		return createAgentAPIRequest(voiceAgent.AgentDefinition, voiceDef, nil, nil)
	}

	voiceDef := agent_api.VoiceAgentDefinition{
		AgentDefinition: agent_api.AgentDefinition{
			// Translate authoring kind prompt-voice -> service kind voice.
			Kind: agent_api.AgentKindVoice,
		},
		ModelType:        modelType,
		Model:            modelID,
		Instructions:     instructions,
		StructuredInputs: mapVoiceStructuredInputs(voiceAgent.StructuredInputs),
		Audio: &agent_api.VoiceAudioConfig{
			Input: input,
			Output: &agent_api.VoiceOutputConfig{
				Format: outputFormat,
				Voice:  outputVoice,
				Speed:  outputSpeed,
			},
		},
		OutputModalities:  outputModalities,
		Store:             voiceAgent.Store,
		Tools:             voiceAgent.Tools,
		Avatar:            voiceAgent.Avatar,
		Greeting:          voiceAgent.Greeting,
		Handoff:           voiceAgent.Handoff,
		ToolChoice:        voiceAgent.ToolChoice,
		ParallelToolCalls: voiceAgent.ParallelToolCalls,
		MaxOutputTokens:   voiceAgent.MaxOutputTokens,
		Include:           voiceAgent.Include,
	}

	return createAgentAPIRequest(voiceAgent.AgentDefinition, voiceDef, nil, nil)
}

// createAgentAPIRequest is a helper function to create the final request with common fields.
// The optional agentEndpoint and agentCard parameters are mapped to the corresponding
// request-level fields when non-nil.
func createAgentAPIRequest(
	agentDefinition AgentDefinition,
	agentDef any,
	agentEndpoint *AgentEndpoint,
	agentCard *AgentCard,
) (*agent_api.CreateAgentRequest, error) {
	// Prepare metadata
	metadata := make(map[string]string)
	if agentDefinition.Metadata != nil {
		// Handle authors specially - convert slice to comma-separated string
		if authors, exists := (*agentDefinition.Metadata)["authors"]; exists {
			if authorsSlice, ok := authors.([]any); ok {
				var authorsStr []string
				for _, author := range authorsSlice {
					if authorStr, ok := author.(string); ok {
						authorsStr = append(authorsStr, authorStr)
					}
				}
				metadata["authors"] = strings.Join(authorsStr, ",")
			}
		}
		// Copy other metadata as strings
		for key, value := range *agentDefinition.Metadata {
			if key != "authors" {
				if strValue, ok := value.(string); ok {
					metadata[key] = strValue
				}
			}
		}
	}

	// Determine agent name (use name from agent definition)
	agentName := agentDefinition.Name
	if agentName == "" {
		agentName = "unspecified-agent-name"
	}

	// Create the agent request
	request := &agent_api.CreateAgentRequest{
		Name: agentName,
		CreateAgentVersionRequest: agent_api.CreateAgentVersionRequest{
			Definition: agentDef,
		},
	}

	if agentDefinition.Description != nil && *agentDefinition.Description != "" {
		request.Description = agentDefinition.Description
	}

	if len(metadata) > 0 {
		request.Metadata = metadata
	}

	// Map optional agent endpoint and card fields.
	if agentEndpoint != nil {
		apiEndpoint := &agent_api.AgentEndpoint{}

		// Map protocols
		if len(agentEndpoint.Protocols) > 0 {
			protocols := make(
				[]agent_api.AgentEndpointProtocol, 0, len(agentEndpoint.Protocols),
			)
			for _, p := range agentEndpoint.Protocols {
				trimmed := strings.TrimSpace(p)
				if trimmed == "" {
					return nil, fmt.Errorf(
						"agentEndpoint contains an empty protocol value",
					)
				}
				protocols = append(protocols, agent_api.AgentEndpointProtocol(trimmed))
			}
			apiEndpoint.Protocols = protocols
		}

		// Map version selector
		if agentEndpoint.VersionSelector != nil {
			rules := make(
				[]agent_api.VersionSelectionRule, 0,
				len(agentEndpoint.VersionSelector.VersionSelectionRules),
			)
			for _, r := range agentEndpoint.VersionSelector.VersionSelectionRules {
				rules = append(rules, agent_api.VersionSelectionRule{
					Type:              agent_api.VersionSelectorType(r.Type),
					AgentVersion:      r.AgentVersion,
					TrafficPercentage: r.TrafficPercentage,
				})
			}
			apiEndpoint.VersionSelector = &agent_api.VersionSelector{
				VersionSelectionRules: rules,
			}
		}

		// Map authorization schemes
		if len(agentEndpoint.AuthorizationSchemes) > 0 {
			schemes := make(
				[]agent_api.AgentEndpointAuthorizationScheme, 0,
				len(agentEndpoint.AuthorizationSchemes),
			)
			for _, s := range agentEndpoint.AuthorizationSchemes {
				scheme := agent_api.AgentEndpointAuthorizationScheme{
					Type: agent_api.AgentEndpointAuthorizationSchemeType(s.Type),
				}
				if s.IsolationKeySource != nil {
					scheme.IsolationKeySource = &agent_api.IsolationKeySource{
						Kind: agent_api.IsolationKeySourceKind(
							s.IsolationKeySource.Kind,
						),
					}
				}
				schemes = append(schemes, scheme)
			}
			apiEndpoint.AuthorizationSchemes = schemes
		}

		request.AgentEndpoint = apiEndpoint
	}

	if agentCard != nil {
		if strings.TrimSpace(agentCard.Description) == "" {
			return nil, fmt.Errorf(
				"agentCard.description is required",
			)
		}
		if len(agentCard.Skills) == 0 {
			return nil, fmt.Errorf(
				"agentCard.skills must contain at least one skill",
			)
		}
		skills := make([]agent_api.AgentCardSkill, len(agentCard.Skills))
		for i, s := range agentCard.Skills {
			if strings.TrimSpace(s.ID) == "" {
				return nil, fmt.Errorf(
					"agentCard.skills[%d].id is required", i,
				)
			}
			if strings.TrimSpace(s.Name) == "" {
				return nil, fmt.Errorf(
					"agentCard.skills[%d].name is required", i,
				)
			}
			if strings.TrimSpace(s.Description) == "" {
				return nil, fmt.Errorf(
					"agentCard.skills[%d].description is required", i,
				)
			}
			skills[i] = agent_api.AgentCardSkill{
				ID:          s.ID,
				Name:        s.Name,
				Description: s.Description,
				Tags:        s.Tags,
				Examples:    s.Examples,
			}
		}
		request.AgentCard = &agent_api.AgentCard{
			Description: agentCard.Description,
			Version:     agentCard.Version,
			Skills:      skills,
		}
	}

	return request, nil
}
