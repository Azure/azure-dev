// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"
)

// AgentKind represents the type of agent
type AgentKind string

const (
	AgentKindHosted   AgentKind = "hosted"
	AgentKindWorkflow AgentKind = "workflow"
	// AgentKindPromptVoice is the authoring (agent.yaml) kind for a declarative
	// voice (speech-to-speech) agent. It is intentionally distinct from the
	// data-plane service kind "voice": the map layer translates prompt-voice ->
	// voice when building the create request. Reserving "prompt-voice" keeps a
	// clean boundary against a future hosted (code) voice agent.
	AgentKindPromptVoice AgentKind = "prompt-voice"
	// AgentKindVoice is the preferred authoring kind for managed Voice agents.
	// AgentKindPromptVoice remains accepted for backwards compatibility.
	AgentKindVoice AgentKind = "voice"
)

// VoiceModelType selects the model-inference mode for a voice agent.
type VoiceModelType string

const (
	VoiceModelTypeManaged      VoiceModelType = "managed"
	VoiceModelTypeSelfDeployed VoiceModelType = "self_deployed"
	VoiceModelTypeHostedAgent  VoiceModelType = "hosted_agent"
)

// VoiceTargetAgent identifies the hosted agent service that supplies the
// conversation logic for a hosted voice wrapper. Service is an azure.yaml
// service name; azd resolves it to the deployed Foundry agent name and version.
type VoiceTargetAgent struct {
	Service string `json:"service" yaml:"service"`
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
}

// IsValidAgentKind checks if the provided AgentKind is valid
func IsValidAgentKind(kind AgentKind) bool {
	return slices.Contains(ValidAgentKinds(), kind)
}

// ValidAgentKinds returns a slice of all valid AgentKind values
func ValidAgentKinds() []AgentKind {
	return []AgentKind{
		AgentKindHosted,
		AgentKindWorkflow,
		AgentKindPromptVoice,
		AgentKindVoice,
	}
}

// IsVoiceAgentKind reports whether kind is a managed Voice agent authoring kind.
func IsVoiceAgentKind(kind AgentKind) bool {
	return kind == AgentKindPromptVoice || kind == AgentKindVoice
}

type ResourceKind string

const (
	ResourceKindModel      ResourceKind = "model"
	ResourceKindTool       ResourceKind = "tool"
	ResourceKindToolbox    ResourceKind = "toolbox"
	ResourceKindConnection ResourceKind = "connection"
)

type ToolKind string

const (
	ToolKindFunction        ToolKind = "function"
	ToolKindCustom          ToolKind = "custom"
	ToolKindWebSearch       ToolKind = "web_search"
	ToolKindBingGrounding   ToolKind = "bing_grounding"
	ToolKindFileSearch      ToolKind = "file_search"
	ToolKindMcp             ToolKind = "mcp"
	ToolKindOpenApi         ToolKind = "openapi"
	ToolKindCodeInterpreter ToolKind = "code_interpreter"
	ToolKindAzureAiSearch   ToolKind = "azure_ai_search"
	ToolKindA2APreview      ToolKind = "a2a_preview"
)

// legacyToolKindAliases maps deprecated camelCase tool kind names to their
// current snake_case equivalents so that older agent.yaml files continue to parse.
var legacyToolKindAliases = map[ToolKind]ToolKind{
	"webSearch":       ToolKindWebSearch,
	"bingGrounding":   ToolKindBingGrounding,
	"fileSearch":      ToolKindFileSearch,
	"codeInterpreter": ToolKindCodeInterpreter,
	"azureAiSearch":   ToolKindAzureAiSearch,
	"a2aPreview":      ToolKindA2APreview,
	"openApi":         ToolKindOpenApi,
}

// NormalizeToolKind maps legacy camelCase tool kind values to the current
// snake_case form. If the kind is already canonical it is returned unchanged.
func NormalizeToolKind(kind ToolKind) ToolKind {
	if canonical, ok := legacyToolKindAliases[kind]; ok {
		return canonical
	}
	return kind
}

// AuthType represents the authentication type for a connection.
type AuthType string

const (
	AuthTypeAAD                  AuthType = "AAD"
	AuthTypeApiKey               AuthType = "ApiKey"
	AuthTypeCustomKeys           AuthType = "CustomKeys"
	AuthTypeNone                 AuthType = "None"
	AuthTypeOAuth2               AuthType = "OAuth2"
	AuthTypePAT                  AuthType = "PAT"
	AuthTypeUserEntraToken       AuthType = "UserEntraToken"
	AuthTypeAgenticIdentity      AuthType = "AgenticIdentity"
	AuthTypeAgenticIdentityToken AuthType = "AgenticIdentityToken"
	AuthTypeManagedIdentity      AuthType = "ProjectManagedIdentity"
	AuthTypeServicePrincipal     AuthType = "ServicePrincipal"
	AuthTypeUsernamePassword     AuthType = "UsernamePassword"
	AuthTypeAccessKey            AuthType = "AccessKey"
	AuthTypeAccountKey           AuthType = "AccountKey"
	AuthTypeSAS                  AuthType = "SAS"
)

// NormalizeConnectionAuthType maps auth types accepted in agent.yaml to
// the management-plane value required for project connection provisioning.
// Legacy AgenticIdentity values are normalized to AgenticIdentityToken
// for API compatibility.
func NormalizeConnectionAuthType(authType AuthType) AuthType {
	if authType == AuthTypeAgenticIdentity {
		return AuthTypeAgenticIdentityToken
	}

	return authType
}

// CategoryKind represents the category of a connection resource.
type CategoryKind string

const (
	CategoryAzureOpenAI           CategoryKind = "AzureOpenAI"
	CategoryCognitiveSearch       CategoryKind = "CognitiveSearch"
	CategoryCognitiveService      CategoryKind = "CognitiveService"
	CategoryCustomKeys            CategoryKind = "CustomKeys"
	CategoryServerlessEndpoint    CategoryKind = "Serverless"
	CategoryContainerRegistry     CategoryKind = "ContainerRegistry"
	CategoryApiKey                CategoryKind = "ApiKey"
	CategoryAzureBlob             CategoryKind = "AzureBlob"
	CategoryGit                   CategoryKind = "Git"
	CategoryRedis                 CategoryKind = "Redis"
	CategoryS3                    CategoryKind = "S3"
	CategorySnowflake             CategoryKind = "Snowflake"
	CategoryAzureSqlDb            CategoryKind = "AzureSqlDb"
	CategoryAzureSynapseAnalytics CategoryKind = "AzureSynapseAnalytics"
	CategoryAzureMySqlDb          CategoryKind = "AzureMySqlDb"
	CategoryAzurePostgresDb       CategoryKind = "AzurePostgresDb"
	CategoryADLSGen2              CategoryKind = "ADLSGen2"
	CategoryAzureDataExplorer     CategoryKind = "AzureDataExplorer"
	CategoryBingLLMSearch         CategoryKind = "BingLLMSearch"
	CategoryMicrosoftOneLake      CategoryKind = "MicrosoftOneLake"
	CategoryElasticSearch         CategoryKind = "Elasticsearch"
	CategoryPinecone              CategoryKind = "Pinecone"
	CategoryQdrant                CategoryKind = "Qdrant"
)

type ConnectionKind string

const (
	ConnectionKindReference ConnectionKind = "reference"
	ConnectionKindRemote    ConnectionKind = "remote"
	ConnectionKindApiKey    ConnectionKind = "apiKey"
	ConnectionKindAnonymous ConnectionKind = "anonymous"
)

// AgentDefinition The following is a specification for defining AI agents with structured metadata, inputs, outputs, tools,
// and templates.
// It provides a way to create reusable and composable AI agents that can be executed with specific configurations.
// The specification includes metadata about the agent, model configuration, input parameters, expected outputs,
// available tools, and template configurations for prompt rendering.
type AgentDefinition struct {
	Kind         AgentKind       `json:"kind" yaml:"kind"`
	Name         string          `json:"name" yaml:"name"`
	DisplayName  *string         `json:"displayName,omitempty" yaml:"displayName,omitempty"`
	Description  *string         `json:"description,omitempty" yaml:"description,omitempty"`
	Metadata     *map[string]any `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	InputSchema  *PropertySchema `json:"inputSchema,omitempty" yaml:"inputSchema,omitempty"`
	OutputSchema *PropertySchema `json:"outputSchema,omitempty" yaml:"outputSchema,omitempty"`
}

// Workflow A workflow agent that can orchestrate multiple steps and actions.
// This agent type is designed to handle complex workflows that may involve
// multiple tools, models, and decision points.
// The workflow agent can be configured with a series of steps that define
// the flow of execution, including conditional logic and parallel processing.
// This allows for the creation of sophisticated AI-driven processes that can
// adapt to various scenarios and requirements.
// Note: The detailed structure of the workflow steps and actions is not defined here
// and would need to be implemented based on specific use cases and requirements.
type Workflow struct {
	AgentDefinition `json:",inline" yaml:",inline"`
	Trigger         *map[string]any `json:"trigger,omitempty" yaml:"trigger,omitempty"`
}

// VoiceAgent is a declarative (managed) voice speech-to-speech agent authored in
// agent.yaml with kind "prompt-voice". Unlike a ContainerAgent it has no image,
// Dockerfile, or code — Foundry's Voice Live service hosts the model and audio
// pipeline. The map layer translates this into a data-plane VoiceAgentDefinition
// whose service kind is "voice".
//
// Simple authoring can rely on defaults for omitted audio/runtime settings.
// Advanced projects can override the audio pipeline, structured inputs, tools,
// greeting, avatar, handoff, and response options supported by the service.
// ModelType defaults to "managed" when omitted; BYOM uses "self_deployed".
type VoiceAgent struct {
	AgentDefinition `json:",inline" yaml:",inline"`
	// ModelType selects managed vs self_deployed (BYOM). Optional; defaults to managed.
	ModelType VoiceModelType `json:"modelType,omitempty" yaml:"model_type,omitempty"`
	// Model names the speech-to-speech model (e.g. "gpt-realtime"). Reuses the
	// shared Model struct; only Id is required for voice.
	Model *Model `json:"model,omitempty" yaml:"model,omitempty"`
	// TargetAgent references the hosted agent service used when model_type is hosted_agent.
	TargetAgent *VoiceTargetAgent `json:"targetAgent,omitempty" yaml:"target_agent,omitempty"`
	// Instructions is the system prompt for the voice assistant.
	Instructions *string `json:"instructions,omitempty" yaml:"instructions,omitempty"`
	// Voice is the output voice name (e.g. "en-US-Ava:DragonHDLatestNeural" for
	// an Azure Neural voice, or "alloy" for an OpenAI realtime voice).
	Voice *string `json:"voice,omitempty" yaml:"voice,omitempty"`
	// StructuredInputs declares template inputs used by voice instructions and greeting.
	StructuredInputs map[string]any `json:"structuredInputs,omitempty" yaml:"structured_inputs,omitempty"`
	// Audio customizes the input and output voice pipeline. Missing fields keep azd defaults.
	Audio *VoiceAudio `json:"audio,omitempty" yaml:"audio,omitempty"`
	// OutputModalities declares response modalities such as audio, text, animation, or avatar.
	OutputModalities []string `json:"outputModalities,omitempty" yaml:"output_modalities,omitempty"`
	// Store toggles server-side logging (transcript + per-turn audio). Optional;
	// the service defaults to false when omitted.
	Store *bool `json:"store,omitempty" yaml:"store,omitempty"`
	// Tools are passed through to the prompt voice service. Supported direct tool
	// types include function, mcp, system, and toolbox.
	Tools []map[string]any `json:"tools,omitempty" yaml:"tools,omitempty"`
	// Avatar customizes voice avatar output for services that support it.
	Avatar map[string]any `json:"avatar,omitempty" yaml:"avatar,omitempty"`
	// Greeting configures initial greeting behavior for services that support it.
	Greeting map[string]any `json:"greeting,omitempty" yaml:"greeting,omitempty"`
	// Handoff configures voice handoff behavior for services that support it.
	Handoff map[string]any `json:"handoff,omitempty" yaml:"handoff,omitempty"`
	// ToolChoice configures service tool choice behavior, such as auto/none/required.
	ToolChoice any `json:"toolChoice,omitempty" yaml:"tool_choice,omitempty"`
	// ParallelToolCalls toggles parallel tool calls.
	ParallelToolCalls *bool `json:"parallelToolCalls,omitempty" yaml:"parallel_tool_calls,omitempty"`
	// MaxOutputTokens limits response output tokens. Use an integer or service-supported string such as "inf".
	MaxOutputTokens any `json:"maxOutputTokens,omitempty" yaml:"max_output_tokens,omitempty"`
	// Include requests additional service response fields.
	Include []string `json:"include,omitempty" yaml:"include,omitempty"`
}

// VoiceAudio bundles optional prompt voice input/output audio overrides.
type VoiceAudio struct {
	Input  *VoiceAudioInput  `json:"input,omitempty" yaml:"input,omitempty"`
	Output *VoiceAudioOutput `json:"output,omitempty" yaml:"output,omitempty"`
}

// VoiceAudioInput customizes caller-to-agent audio.
type VoiceAudioInput struct {
	Format           *VoiceAudioFormat    `json:"format,omitempty" yaml:"format,omitempty"`
	NoiseReduction   *VoiceNoiseReduction `json:"noiseReduction,omitempty" yaml:"noise_reduction,omitempty"`
	EchoCancellation map[string]any       `json:"echoCancellation,omitempty" yaml:"echo_cancellation,omitempty"`
	TurnDetection    *VoiceTurnDetection  `json:"turnDetection,omitempty" yaml:"turn_detection,omitempty"`
	Transcription    *VoiceTranscription  `json:"transcription,omitempty" yaml:"transcription,omitempty"`
}

// VoiceAudioOutput customizes agent-to-caller audio.
type VoiceAudioOutput struct {
	Format *VoiceAudioFormat `json:"format,omitempty" yaml:"format,omitempty"`
	Voice  *VoiceConfig      `json:"voice,omitempty" yaml:"voice,omitempty"`
	Speed  *float64          `json:"speed,omitempty" yaml:"speed,omitempty"`
}

// VoiceAudioFormat describes an audio stream format.
type VoiceAudioFormat struct {
	Type string `json:"type" yaml:"type"`
	Rate *int   `json:"rate,omitempty" yaml:"rate,omitempty"`
}

// VoiceNoiseReduction configures input audio noise reduction.
type VoiceNoiseReduction struct {
	Type string `json:"type" yaml:"type"`
}

// VoiceTurnDetection configures server-side turn detection.
type VoiceTurnDetection struct {
	Type              string   `json:"type" yaml:"type"`
	Threshold         *float64 `json:"threshold,omitempty" yaml:"threshold,omitempty"`
	PrefixPaddingMs   *int     `json:"prefixPaddingMs,omitempty" yaml:"prefix_padding_ms,omitempty"`
	SilenceDurationMs *int     `json:"silenceDurationMs,omitempty" yaml:"silence_duration_ms,omitempty"`
	CreateResponse    *bool    `json:"createResponse,omitempty" yaml:"create_response,omitempty"`
	Eagerness         *string  `json:"eagerness,omitempty" yaml:"eagerness,omitempty"`
	SpeechDurationMs  *int     `json:"speechDurationMs,omitempty" yaml:"speech_duration_ms,omitempty"`
	RemoveFillerWords *bool    `json:"removeFillerWords,omitempty" yaml:"remove_filler_words,omitempty"`
	InterruptResponse *bool    `json:"interruptResponse,omitempty" yaml:"interrupt_response,omitempty"`
	Languages         []string `json:"languages,omitempty" yaml:"languages,omitempty"`
	AutoTruncate      *bool    `json:"autoTruncate,omitempty" yaml:"auto_truncate,omitempty"`
}

// VoiceTranscription configures input transcription.
type VoiceTranscription struct {
	Model    string  `json:"model,omitempty" yaml:"model,omitempty"`
	Language *string `json:"language,omitempty" yaml:"language,omitempty"`
	Prompt   *string `json:"prompt,omitempty" yaml:"prompt,omitempty"`
}

// VoiceConfig selects the output voice.
type VoiceConfig struct {
	Type   string  `json:"type" yaml:"type"`
	Name   string  `json:"name" yaml:"name"`
	Style  *string `json:"style,omitempty" yaml:"style,omitempty"`
	Pitch  *string `json:"pitch,omitempty" yaml:"pitch,omitempty"`
	Rate   *string `json:"rate,omitempty" yaml:"rate,omitempty"`
	Locale *string `json:"locale,omitempty" yaml:"locale,omitempty"`
	Volume *string `json:"volume,omitempty" yaml:"volume,omitempty"`
}

// ContainerResources represents the resource allocation for a containerized agent.
type ContainerResources struct {
	Cpu    string `json:"cpu" yaml:"cpu"`
	Memory string `json:"memory" yaml:"memory"`
}

// CodeConfiguration represents the code deploy configuration for a hosted agent.
// When present in a ContainerAgent, it signals code deploy mode (ZIP upload)
// instead of container/image-based deploy.
type CodeConfiguration struct {
	Runtime              string  `json:"runtime" yaml:"runtime"`
	EntryPoint           string  `json:"entryPoint" yaml:"entry_point"`
	DependencyResolution *string `json:"dependencyResolution,omitempty" yaml:"dependency_resolution,omitempty"`
}

// DefaultDependencyResolution is the dependency-resolution mode used for code
// deploy when a CodeConfiguration omits one. Foundry's create-agent API rejects
// an empty dependency_resolution, so callers default to this value (the same
// default as `azd ai agent init --dep-resolution`).
const DefaultDependencyResolution = "remote_build"

// Session idle-timeout bounds (in seconds) for a hosted agent, matching the
// upstream HostedAgentDefinition.session_configuration.idle_timeout_seconds
// contract. When omitted, the service applies its own default.
const (
	// MinSessionIdleTimeoutSeconds is the smallest accepted idle timeout.
	MinSessionIdleTimeoutSeconds = 120
	// MaxSessionIdleTimeoutSeconds is the largest accepted idle timeout.
	MaxSessionIdleTimeoutSeconds = 3600
)

// SessionConfiguration configures the runtime session behavior of a hosted agent.
type SessionConfiguration struct {
	// IdleTimeoutSeconds is the idle duration, in seconds, before a session's
	// sandbox is suspended. Valid range is 120–3600 (inclusive). When nil,
	// session_configuration is omitted from the request and the service default
	// (900 seconds) applies.
	IdleTimeoutSeconds *int `json:"idleTimeoutSeconds,omitempty" yaml:"idle_timeout_seconds,omitempty"`
}

// PolicyType identifies the kind of governance policy attached to a hosted agent.
type PolicyType string

const (
	// PolicyTypeRai is a Responsible AI (content safety) policy.
	PolicyTypeRai PolicyType = "rai_policy"
)

// Invocation content types describe how a request or response body is encoded, which
// determines how the content-safety proxy extracts the text it moderates. Both default to
// InvocationContentTypeJSON when omitted.
//
// Keys in these structures follow the extension's dual-casing convention: camelCase in
// azure.yaml, snake_case in the deprecated on-disk agent.yaml. The values below are wire
// values and stay snake_case in both.
const (
	InvocationContentTypeJSON = "json"
	InvocationContentTypeText = "text"
)

// Invocation response modes declare which response shapes the agent container can produce.
const (
	InvocationResponseModeNonStreaming = "non_streaming"
	InvocationResponseModeStreaming    = "streaming"
	InvocationResponseModeBoth         = "both"
)

// InvocationsProtocol is the protocol name an agent must expose for invocations moderation
// to have any effect. The WebSocket variant ("invocations_ws") does not go through the
// content-safety HTTP proxy and is therefore not covered.
const InvocationsProtocol = "invocations"

// SseTextSelector locates the text to moderate inside a single server-sent event frame.
type SseTextSelector struct {
	// EventType is the SSE event name this selector applies to. Required.
	EventType string `json:"eventType" yaml:"event_type"`
	// TextField is the JSONPath expression, relative to the frame payload, holding the text.
	TextField string `json:"textField,omitempty" yaml:"text_field,omitempty"`
}

// InvocationsModeration configures how the content-safety proxy extracts the text it submits
// to the RAI policy for agents that expose the invocations protocol. A RAI policy without it
// has nothing to moderate on the invocations path.
//
// ResponseMode declares the response shapes the container can produce; it is not an
// "input and output" switch. At runtime the proxy picks exactly one output gate from the
// actual response Content-Type.
type InvocationsModeration struct {
	// InputContentType is "json" or "text". Defaults to "json" when omitted.
	InputContentType string `json:"inputContentType,omitempty" yaml:"input_content_type,omitempty"`
	// OutputContentType is "json" or "text". Defaults to "json" when omitted.
	OutputContentType string `json:"outputContentType,omitempty" yaml:"output_content_type,omitempty"`
	// ResponseMode is "non_streaming", "streaming" or "both". Required.
	ResponseMode string `json:"responseMode,omitempty" yaml:"response_mode,omitempty"`
	// InputPaths are JSONPath expressions selecting request text. Required when the input
	// content type resolves to "json".
	InputPaths []string `json:"inputPaths,omitempty" yaml:"input_paths,omitempty"`
	// OutputPaths are JSONPath expressions selecting buffered response text. Required when
	// ResponseMode includes non-streaming and the output content type resolves to "json".
	OutputPaths []string `json:"outputPaths,omitempty" yaml:"output_paths,omitempty"`
	// StreamSelectors locate text within SSE frames. Required when ResponseMode includes
	// streaming and the output content type resolves to "json".
	StreamSelectors []SseTextSelector `json:"streamSelectors,omitempty" yaml:"stream_selectors,omitempty"`
}

// Policy represents a single safety or governance policy attached to a hosted agent.
// Type discriminates the policy kind; the remaining fields are interpreted based on Type.
//
// For Type "rai_policy", RaiPolicyName is the full ARM resource ID of the RAI policy, for example
// "/subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.CognitiveServices/accounts/<account>/raiPolicies/<policyName>".
// InvocationsModeration is optional and only valid for agents exposing the invocations protocol.
type Policy struct {
	Type                  PolicyType             `json:"type" yaml:"type"`
	RaiPolicyName         string                 `json:"raiPolicyName,omitempty" yaml:"rai_policy_name,omitempty"`
	InvocationsModeration *InvocationsModeration `json:"invocationsModeration,omitempty" yaml:"invocations_moderation,omitempty"`
}

// ContainerAgent This represents a container based agent hosted by the provider/publisher.
// The intent is to represent a container application that the user wants to run
// in a hosted environment that the provider manages.
//
// When Image is set, deploy can use the pre-built container image instead of
// building from a Dockerfile:
//   - Interactive mode: the user is prompted whether to use the configured
//     image or build from a Dockerfile. The default is to build.
//   - Non-interactive mode (`--no-prompt`): the default selection (build from
//     Dockerfile) is used automatically.
type ContainerAgent struct {
	AgentDefinition      `json:",inline" yaml:",inline"`
	Language             string                  `json:"language,omitempty" yaml:"language,omitempty"`
	Toolbox              *ToolboxReference       `json:"toolbox,omitempty" yaml:"toolbox,omitempty"`
	Image                string                  `json:"image,omitempty" yaml:"image,omitempty"`
	RegistryConnectionID string                  `json:"registryConnectionId,omitempty" yaml:"registryConnectionId,omitempty"`
	Protocols            []ProtocolVersionRecord `json:"protocols" yaml:"protocols"`
	Resources            *ContainerResources     `json:"resources,omitempty" yaml:"resources,omitempty"`
	EnvironmentVariables *[]EnvironmentVariable  `json:"environmentVariables,omitempty" yaml:"environment_variables,omitempty"`
	AgentEndpoint        *AgentEndpoint          `json:"agentEndpoint,omitempty" yaml:"agent_endpoint,omitempty"`
	AgentCard            *AgentCard              `json:"agentCard,omitempty" yaml:"agent_card,omitempty"`
	CodeConfiguration    *CodeConfiguration      `json:"codeConfiguration,omitempty" yaml:"code_configuration,omitempty"`
	Policies             []Policy                `json:"policies,omitempty" yaml:"policies,omitempty"`
	SessionConfiguration *SessionConfiguration   `json:"sessionConfiguration,omitempty" yaml:"session_configuration,omitempty"`
}

// ToolboxReference identifies the Foundry toolbox consumed by an agent.
// Version is optional; omitting it follows the toolbox's default version.
type ToolboxReference struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
}

// AgentManifest The following represents a manifest that can be used to create agents dynamically.
// It includes parameters that can be used to configure the agent's behavior.
// These parameters include values that can be used as publisher parameters that can
// be used to describe additional variables that have been tested and are known to work.
// Variables described here are used to configure the agent dynamically at init time.
// Once parameters are provided, these can be referenced in the manifest using the following notation:
// `{{myParameter}}`
// This allows for dynamic configuration of the agent based on the provided parameters.
// (This notation is used elsewhere, but only the `param` scope is supported here)
type AgentManifest struct {
	Name        string          `json:"name" yaml:"name"`
	DisplayName string          `json:"displayName" yaml:"displayName"`
	Description *string         `json:"description,omitempty" yaml:"description,omitempty"`
	Metadata    *map[string]any `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Template    any             `json:"template" yaml:"template"`
	Parameters  PropertySchema  `json:"parameters" yaml:"parameters"`
	Resources   []any           `json:"resources" yaml:"resources"` // Will be a type of Resource
}

// Binding Represents a binding between an input property and a tool parameter.
type Binding struct {
	Name  string `json:"name" yaml:"name"`
	Input string `json:"input" yaml:"input"`
}

// Connection Connection configuration for AI agents.
// `provider`, `kind`, and `endpoint` are required properties here,
// but this section can accept additional via options.
type Connection struct {
	Kind               ConnectionKind `json:"kind" yaml:"kind"`
	AuthenticationMode string         `json:"authenticationMode" yaml:"authenticationMode"`
	UsageDescription   *string        `json:"usageDescription,omitempty" yaml:"usageDescription,omitempty"`
}

// ReferenceConnection Connection configuration for AI services using named connections.
type ReferenceConnection struct {
	Connection `json:",inline" yaml:",inline"`
	Name       string  `json:"name" yaml:"name"`
	Target     *string `json:"target,omitempty" yaml:"target,omitempty"`
}

// RemoteConnection Connection configuration for AI services using named connections.
type RemoteConnection struct {
	Connection `json:",inline" yaml:",inline"`
	Name       string `json:"name" yaml:"name"`
	Endpoint   string `json:"endpoint" yaml:"endpoint"`
}

// ApiKeyConnection Connection configuration for AI services using API keys.
type ApiKeyConnection struct {
	Connection `json:",inline" yaml:",inline"`
	Endpoint   string `json:"endpoint" yaml:"endpoint"`
	//nolint:gosec // schema field name for manifest serialization, not embedded credential
	ApiKey string `json:"apiKey" yaml:"apiKey"`
}

// AnonymousConnection represents a anonymousconnection.
type AnonymousConnection struct {
	Connection `json:",inline" yaml:",inline"`
	Endpoint   string `json:"endpoint" yaml:"endpoint"`
}

// EnvironmentVariable Definition for an environment variable used in containerized agents.
type EnvironmentVariable struct {
	Name  string `json:"name" yaml:"name"`
	Value string `json:"value" yaml:"value"`
}

// Format Template format definition
type Format struct {
	Kind    string          `json:"kind" yaml:"kind"`
	Strict  *bool           `json:"strict,omitempty" yaml:"strict,omitempty"`
	Options *map[string]any `json:"options,omitempty" yaml:"options,omitempty"`
}

// McpServerApprovalMode The approval mode for MCP server tools.
type McpServerApprovalMode struct {
	Kind string `json:"kind" yaml:"kind"`
}

// McpServerToolAlwaysRequireApprovalMode represents a mcpservertoolalwaysrequireapprovalmode.
type McpServerToolAlwaysRequireApprovalMode struct {
	McpServerApprovalMode `json:",inline" yaml:",inline"`
	Kind                  string `json:"kind" yaml:"kind"`
}

// McpServerToolNeverRequireApprovalMode represents a mcpservertoolneverrequireapprovalmode.
type McpServerToolNeverRequireApprovalMode struct {
	McpServerApprovalMode `json:",inline" yaml:",inline"`
	Kind                  string `json:"kind" yaml:"kind"`
}

// McpServerToolSpecifyApprovalMode represents a mcpservertoolspecifyapprovalmode.
type McpServerToolSpecifyApprovalMode struct {
	McpServerApprovalMode      `json:",inline" yaml:",inline"`
	Kind                       string   `json:"kind" yaml:"kind"`
	AlwaysRequireApprovalTools []string `json:"alwaysRequireApprovalTools" yaml:"alwaysRequireApprovalTools"`
	NeverRequireApprovalTools  []string `json:"neverRequireApprovalTools" yaml:"neverRequireApprovalTools"`
}

// Model Model for defining the structure and behavior of AI agents.
// This model includes properties for specifying the model's provider, connection details, and various options.
// It allows for flexible configuration of AI models to suit different use cases and requirements.
type Model struct {
	Id         string        `json:"id" yaml:"id"`
	Provider   *string       `json:"provider,omitempty" yaml:"provider,omitempty"`
	ApiType    *string       `json:"apiType,omitempty" yaml:"apiType,omitempty"`
	Connection *any          `json:"connection,omitempty" yaml:"connection,omitempty"` // Must be a type of Connection
	Options    *ModelOptions `json:"options,omitempty" yaml:"options,omitempty"`
}

// ModelOptions Options for configuring the behavior of the AI model.
// `kind` is a required property here, but this section can accept additional via options.
type ModelOptions struct {
	FrequencyPenalty       *float64        `json:"frequencyPenalty,omitempty" yaml:"frequencyPenalty,omitempty"`
	MaxOutputTokens        *int            `json:"maxOutputTokens,omitempty" yaml:"maxOutputTokens,omitempty"`
	PresencePenalty        *float64        `json:"presencePenalty,omitempty" yaml:"presencePenalty,omitempty"`
	Seed                   *int            `json:"seed,omitempty" yaml:"seed,omitempty"`
	Temperature            *float64        `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	TopK                   *int            `json:"topK,omitempty" yaml:"topK,omitempty"`
	TopP                   *float64        `json:"topP,omitempty" yaml:"topP,omitempty"`
	StopSequences          *[]string       `json:"stopSequences,omitempty" yaml:"stopSequences,omitempty"`
	AllowMultipleToolCalls *bool           `json:"allowMultipleToolCalls,omitempty" yaml:"allowMultipleToolCalls,omitempty"`
	AdditionalProperties   *map[string]any `json:"additionalProperties,omitempty" yaml:"additionalProperties,omitempty"`
}

// Parser Template parser definition
type Parser struct {
	Kind    string          `json:"kind" yaml:"kind"`
	Options *map[string]any `json:"options,omitempty" yaml:"options,omitempty"`
}

// Property Represents a single property.
// This model defines the structure of properties that can be used in prompts,
// including their type, description, whether they are required, and other attributes.
// It allows for the definition of dynamic inputs that can be filled with data
// and processed to generate prompts for AI models.
type Property struct {
	Name        string  `json:"name" yaml:"name"`
	Kind        string  `json:"kind" yaml:"kind"`
	Description *string `json:"description,omitempty" yaml:"description,omitempty"`
	Required    *bool   `json:"required,omitempty" yaml:"required,omitempty"`
	Default     *any    `json:"default,omitempty" yaml:"default,omitempty"`
	Example     *any    `json:"example,omitempty" yaml:"example,omitempty"`
	EnumValues  *[]any  `json:"enumValues,omitempty" yaml:"enumValues,omitempty"`
	Secret      *bool   `json:"secret,omitempty" yaml:"secret,omitempty"`
}

// ArrayProperty Represents an array property.
// This extends the base Property model to represent an array of items.
type ArrayProperty struct {
	Property `json:",inline" yaml:",inline"`
	Kind     string   `json:"kind" yaml:"kind"`
	Items    Property `json:"items" yaml:"items"`
}

// ObjectProperty Represents an object property.
// This extends the base Property model to represent a structured object.
type ObjectProperty struct {
	Property   `json:",inline" yaml:",inline"`
	Kind       string     `json:"kind" yaml:"kind"`
	Properties []Property `json:"properties" yaml:"properties"`
}

// PropertySchema Definition for the property schema of a model.
// This includes the properties and example records.
//
// The schema supports two YAML layouts for Properties:
//
// Array format (explicit):
//
//	properties:
//	  - name: foo
//	    kind: string
//
// Record/map format (canonical agent manifest shorthand):
//
//	parameters:
//	  foo:
//	    schema: { type: string }
//	    description: a foo param
//	    required: true
//
// UnmarshalYAML detects which layout is present and normalises to []Property.
type PropertySchema struct {
	Examples   *[]map[string]any `json:"examples,omitempty" yaml:"-"`
	Strict     *bool             `json:"strict,omitempty" yaml:"-"`
	Properties []Property        `json:"properties" yaml:"-"`
}

// UnmarshalYAML supports both the array format (properties: []) and the
// record/map format where parameter names are direct YAML keys.
func (ps *PropertySchema) UnmarshalYAML(value *yaml.Node) error {
	// The node should be a mapping.
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("PropertySchema: expected mapping node, got %d", value.Kind)
	}

	// First pass: look for known struct keys (examples, strict, properties).
	// Anything else is treated as a record-format parameter name.
	var (
		propertiesNode *yaml.Node
		extraKeys      []string
		extraValues    []*yaml.Node
	)

	for i := 0; i < len(value.Content)-1; i += 2 {
		key := value.Content[i].Value
		val := value.Content[i+1]

		switch key {
		case "examples":
			var examples []map[string]any
			if err := val.Decode(&examples); err != nil {
				return fmt.Errorf("PropertySchema.examples: %w", err)
			}
			ps.Examples = &examples
		case "strict":
			var strict bool
			if err := val.Decode(&strict); err != nil {
				return fmt.Errorf("PropertySchema.strict: %w", err)
			}
			ps.Strict = &strict
		case "properties":
			propertiesNode = val
		default:
			extraKeys = append(extraKeys, key)
			extraValues = append(extraValues, val)
		}
	}

	// If an explicit "properties" key was found, decode it (array or map).
	if propertiesNode != nil {
		props, err := decodePropertiesNode(propertiesNode)
		if err != nil {
			return fmt.Errorf("PropertySchema.properties: %w", err)
		}
		ps.Properties = props
		return nil
	}

	// No explicit "properties" key — treat extra keys as record-format params.
	if len(extraKeys) > 0 {
		for i, name := range extraKeys {
			prop, err := decodeRecordProperty(name, extraValues[i])
			if err != nil {
				return fmt.Errorf("PropertySchema parameter %q: %w", name, err)
			}
			ps.Properties = append(ps.Properties, prop)
		}
	}

	return nil
}

// decodePropertiesNode handles "properties:" as either an array or a map.
func decodePropertiesNode(node *yaml.Node) ([]Property, error) {
	switch node.Kind {
	case yaml.SequenceNode:
		var props []Property
		if err := node.Decode(&props); err != nil {
			return nil, err
		}
		return props, nil
	case yaml.MappingNode:
		var props []Property
		for i := 0; i < len(node.Content)-1; i += 2 {
			name := node.Content[i].Value
			prop, err := decodeRecordProperty(name, node.Content[i+1])
			if err != nil {
				return nil, fmt.Errorf("property %q: %w", name, err)
			}
			props = append(props, prop)
		}
		return props, nil
	default:
		return nil, fmt.Errorf("expected sequence or mapping, got %d", node.Kind)
	}
}

// recordEntry is the intermediate structure for parsing a single record-format
// parameter entry like:
//
//	param_name:
//	  schema: { type: string, enum: [...], default: ... }
//	  description: some text
//	  required: true
type recordEntry struct {
	Schema      *recordSchema `yaml:"schema"`
	Description string        `yaml:"description"`
	Required    bool          `yaml:"required"`
	Default     *any          `yaml:"default"`
	Example     *any          `yaml:"example"`
	EnumValues  *[]any        `yaml:"enumValues"`
}

type recordSchema struct {
	Type    string `yaml:"type"`
	Enum    []any  `yaml:"enum"`
	Default *any   `yaml:"default"`
	Secret  bool   `yaml:"secret"`
}

// decodeRecordProperty converts a record-format parameter entry into a Property.
func decodeRecordProperty(name string, node *yaml.Node) (Property, error) {
	var entry recordEntry
	if err := node.Decode(&entry); err != nil {
		return Property{}, err
	}

	prop := Property{Name: name}
	if entry.Description != "" {
		prop.Description = &entry.Description
	}
	if entry.Required {
		prop.Required = &entry.Required
	}
	if entry.Default != nil {
		prop.Default = entry.Default
	}
	if entry.Example != nil {
		prop.Example = entry.Example
	}
	if entry.EnumValues != nil {
		prop.EnumValues = entry.EnumValues
	}

	// Extract kind/default/enum/secret from nested schema if present
	if entry.Schema != nil {
		prop.Kind = entry.Schema.Type
		if entry.Schema.Default != nil && prop.Default == nil {
			prop.Default = entry.Schema.Default
		}
		if len(entry.Schema.Enum) > 0 && prop.EnumValues == nil {
			prop.EnumValues = &entry.Schema.Enum
		}
		if entry.Schema.Secret {
			prop.Secret = new(true)
		}
	}

	return prop, nil
}

// MarshalYAML writes PropertySchema back as the record/map format so that
// {{param}} placeholders elsewhere in the document survive a marshal→unmarshal
// round-trip through InjectParameterValuesIntoManifest.
func (ps PropertySchema) MarshalYAML() (any, error) {
	out := make(map[string]any)

	if ps.Examples != nil {
		out["examples"] = *ps.Examples
	}
	if ps.Strict != nil {
		out["strict"] = *ps.Strict
	}

	// Emit each property as a record-format entry.
	props := make(map[string]any, len(ps.Properties))
	for _, p := range ps.Properties {
		entry := map[string]any{}
		schema := map[string]any{}

		if p.Kind != "" {
			schema["type"] = p.Kind
		}
		if p.Default != nil {
			schema["default"] = *p.Default
		}
		if p.EnumValues != nil {
			schema["enum"] = *p.EnumValues
		}
		if p.Secret != nil && *p.Secret {
			schema["secret"] = true
		}
		if len(schema) > 0 {
			entry["schema"] = schema
		}

		if p.Description != nil {
			entry["description"] = *p.Description
		}
		if p.Required != nil {
			entry["required"] = *p.Required
		}
		if p.Example != nil {
			entry["example"] = *p.Example
		}
		props[p.Name] = entry
	}

	if len(props) > 0 {
		// Merge property keys at the top level (record format).
		// Reject parameter names that collide with reserved schema keys.
		reservedKeys := map[string]bool{
			"examples":   true,
			"strict":     true,
			"properties": true,
		}
		for k, v := range props {
			if reservedKeys[k] {
				return nil, fmt.Errorf(
					"parameter name %q conflicts with reserved PropertySchema key", k,
				)
			}
			out[k] = v
		}
	}

	return out, nil
}

// ProtocolVersionRecord represents a protocolversionrecord.
type ProtocolVersionRecord struct {
	Protocol string `json:"protocol" yaml:"protocol"`
	Version  string `json:"version" yaml:"version"`
}

// VersionSelectionRule describes how traffic is routed to an agent version in agent.yaml.
type VersionSelectionRule struct {
	Type              string `json:"type" yaml:"type"`
	AgentVersion      string `json:"agentVersion" yaml:"agent_version"`
	TrafficPercentage *int32 `json:"trafficPercentage,omitempty" yaml:"traffic_percentage,omitempty"`
}

// VersionSelector determines how traffic is routed to different versions of an agent.
type VersionSelector struct {
	VersionSelectionRules []VersionSelectionRule `json:"versionSelectionRules" yaml:"version_selection_rules"`
}

// IsolationKeySource describes the source from which a per-user isolation key is derived.
type IsolationKeySource struct {
	Kind string `json:"kind" yaml:"kind"`
}

// AuthorizationScheme describes an authorization scheme for the agent endpoint in agent.yaml.
type AuthorizationScheme struct {
	Type               string              `json:"type" yaml:"type"`
	IsolationKeySource *IsolationKeySource `json:"isolationKeySource,omitempty" yaml:"isolation_key_source,omitempty"`
}

// AgentEndpoint describes the endpoint configuration for an agent in agent.yaml.
type AgentEndpoint struct {
	VersionSelector      *VersionSelector      `json:"versionSelector,omitempty" yaml:"version_selector,omitempty"`
	Protocols            []string              `json:"protocols,omitempty" yaml:"protocols,omitempty"`
	AuthorizationSchemes []AuthorizationScheme `json:"authorizationSchemes,omitempty" yaml:"authorization_schemes,omitempty"`
}

// AgentCardSkill describes a single capability that an agent can perform.
type AgentCardSkill struct {
	ID          string   `json:"id" yaml:"id"`
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description" yaml:"description"`
	Tags        []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty" yaml:"examples,omitempty"`
}

// AgentCard is the A2A agent card that advertises an agent's capabilities.
type AgentCard struct {
	Description string           `json:"description" yaml:"description"`
	Version     *string          `json:"version,omitempty" yaml:"version,omitempty"`
	Skills      []AgentCardSkill `json:"skills" yaml:"skills"`
}

// Resource Represents a resource required by the agent.
// Resources can include databases, APIs, or other external systems
// that the agent needs to interact with to perform its tasks
type Resource struct {
	Name string       `json:"name" yaml:"name"`
	Kind ResourceKind `json:"kind" yaml:"kind"`
}

// ModelResource Represents a model resource required by the agent
type ModelResource struct {
	Resource `json:",inline" yaml:",inline"`
	Id       string `json:"id" yaml:"id"`
}

// ToolResource Represents a tool resource required by the agent
type ToolResource struct {
	Resource `json:",inline" yaml:",inline"`
	Id       string         `json:"id" yaml:"id"`
	Options  map[string]any `json:"options" yaml:"options"`
}

// ToolboxResource Represents a toolbox resource required by the agent.
// A toolbox is a reusable collection of tools that can be deployed as a Foundry Toolset.
type ToolboxResource struct {
	Resource    `json:",inline" yaml:",inline"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Tools       []any  `json:"tools" yaml:"tools"`
}

// ConnectionResource Represents a connection resource required by the agent.
// Maps to the Bicep ConnectionPropertiesV2 spec for creating project connections.
type ConnectionResource struct {
	Resource       `json:",inline" yaml:",inline"`
	Category       CategoryKind      `json:"category" yaml:"category"`
	Target         string            `json:"target" yaml:"target"`
	AuthType       AuthType          `json:"authType" yaml:"authType"`
	Credentials    map[string]any    `json:"credentials,omitempty" yaml:"credentials,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	ExpiryTime     string            `json:"expiryTime,omitempty" yaml:"expiryTime,omitempty"`
	IsSharedToAll  *bool             `json:"isSharedToAll,omitempty" yaml:"isSharedToAll,omitempty"`
	SharedUserList []string          `json:"sharedUserList,omitempty" yaml:"sharedUserList,omitempty"`
	PeRequirement  string            `json:"peRequirement,omitempty" yaml:"peRequirement,omitempty"`
	PeStatus       string            `json:"peStatus,omitempty" yaml:"peStatus,omitempty"`
	Error          string            `json:"error,omitempty" yaml:"error,omitempty"`

	// UseWorkspaceManagedIdentity indicates whether to use workspace managed identity.
	UseWorkspaceManagedIdentity *bool `json:"useWorkspaceManagedIdentity,omitempty" yaml:"useWorkspaceManagedIdentity,omitempty"` //nolint:lll

	// AuthorizationUrl is the OAuth2 authorization endpoint URL (OAuth2 authType).
	AuthorizationUrl string `json:"authorizationUrl,omitempty" yaml:"authorizationUrl,omitempty"` //nolint:lll

	// TokenUrl is the OAuth2 token endpoint URL (OAuth2 authType).
	TokenUrl string `json:"tokenUrl,omitempty" yaml:"tokenUrl,omitempty"`

	// RefreshUrl is the OAuth2 token refresh endpoint URL (OAuth2 authType).
	RefreshUrl string `json:"refreshUrl,omitempty" yaml:"refreshUrl,omitempty"`

	// Scopes is the list of OAuth2 scopes to request (OAuth2 authType).
	Scopes []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`

	// Audience is the token audience for ManagedIdentity / AgenticIdentity / UserEntraToken auth types.
	Audience string `json:"audience,omitempty" yaml:"audience,omitempty"`

	// ConnectorName is the connector name for OAuth2 auth type, where Microsoft provides a managed OAuth2 app
	ConnectorName string `json:"connectorName,omitempty" yaml:"connectorName,omitempty"`
}

// Template Template model for defining prompt templates.
// This model specifies the rendering engine used for slot filling prompts,
// the parser used to process the rendered template into API-compatible format,
// and additional options for the template engine.
// It allows for the creation of reusable templates that can be filled with dynamic data
// and processed to generate prompts for AI models.
type Template struct {
	Format Format `json:"format" yaml:"format"`
	Parser Parser `json:"parser" yaml:"parser"`
}

// Tool Represents a tool that can be used in prompts.
type Tool struct {
	Name        string     `json:"name" yaml:"name"`
	Kind        ToolKind   `json:"kind" yaml:"kind"`
	Description *string    `json:"description,omitempty" yaml:"description,omitempty"`
	Bindings    *[]Binding `json:"bindings,omitempty" yaml:"bindings,omitempty"`
}

// FunctionTool Represents a local function tool.
// FunctionTool A tool that calls a custom function.
// This tool allows an AI agent to call external functions and APIs.
type FunctionTool struct {
	Tool       `json:",inline" yaml:",inline"`
	Parameters PropertySchema `json:"parameters" yaml:"parameters"`
	Strict     *bool          `json:"strict,omitempty" yaml:"strict,omitempty"`
}

// CustomTool Represents a generic server tool that runs on a server.
// This tool kind is designed for operations that require server-side execution.
// It may include features such as authentication, data storage, and long-running processes.
// This tool kind is ideal for tasks that involve complex computations or access to secure resources.
// Server tools can be used to offload heavy processing from client applications.
type CustomTool struct {
	Tool       `json:",inline" yaml:",inline"`
	Connection any            `json:"connection" yaml:"connection"` // Must be a type of Connection
	Options    map[string]any `json:"options" yaml:"options"`
}

// WebSearchTool The Bing search tool.
type WebSearchTool struct {
	Tool    `json:",inline" yaml:",inline"`
	Options map[string]any `json:"options,omitempty" yaml:"options,omitempty"`
}

// BingGroundingTool The Bing search tool.
type BingGroundingTool struct {
	Tool       `json:",inline" yaml:",inline"`
	Connection any            `json:"connection" yaml:"connection"` // Must be a type of Connection
	Options    map[string]any `json:"options,omitempty" yaml:"options,omitempty"`
}

// FileSearchTool A tool for searching files.
// This tool allows an AI agent to search for files based on a query.
type FileSearchTool struct {
	Tool               `json:",inline" yaml:",inline"`
	Connection         any             `json:"connection" yaml:"connection"` // Must be a type of Connection
	VectorStoreIds     []string        `json:"vectorStoreIds" yaml:"vectorStoreIds"`
	MaximumResultCount *int            `json:"maximumResultCount,omitempty" yaml:"maximumResultCount,omitempty"`
	Ranker             *string         `json:"ranker,omitempty" yaml:"ranker,omitempty"`
	ScoreThreshold     *float64        `json:"scoreThreshold,omitempty" yaml:"scoreThreshold,omitempty"`
	Filters            *map[string]any `json:"filters,omitempty" yaml:"filters,omitempty"`
	Options            map[string]any  `json:"options" yaml:"options"`
}

// McpTool The MCP Server tool.
type McpTool struct {
	Tool              `json:",inline" yaml:",inline"`
	Connection        any                   `json:"connection" yaml:"connection"` // Must be a type of Connection
	ServerName        string                `json:"serverName" yaml:"serverName"`
	ServerDescription *string               `json:"serverDescription,omitempty" yaml:"serverDescription,omitempty"`
	URL               string                `json:"url,omitempty" yaml:"url,omitempty"`
	ApprovalMode      McpServerApprovalMode `json:"approvalMode" yaml:"approvalMode"`
	AllowedTools      *[]string             `json:"allowedTools,omitempty" yaml:"allowedTools,omitempty"`
	Options           map[string]any        `json:"options" yaml:"options"`
}

// OpenApiTool represents a openapitool.
type OpenApiTool struct {
	Tool          `json:",inline" yaml:",inline"`
	Connection    any            `json:"connection" yaml:"connection"` // Must be a type of Connection
	Specification string         `json:"specification" yaml:"specification"`
	Options       map[string]any `json:"options" yaml:"options"`
}

// CodeInterpreterTool A tool for running code.
// This tool allows an AI agent to run and execute code snippets.
type CodeInterpreterTool struct {
	Tool    `json:",inline" yaml:",inline"`
	FileIds []string       `json:"fileIds" yaml:"fileIds"`
	Options map[string]any `json:"options" yaml:"options"`
}

// AzureAISearchIndex represents a single index configuration within an AzureAISearchTool.
type AzureAISearchIndex struct {
	ProjectConnectionId string  `json:"project_connection_id" yaml:"project_connection_id"`
	IndexName           string  `json:"index_name" yaml:"index_name"`
	QueryType           *string `json:"query_type,omitempty" yaml:"query_type,omitempty"`
	TopK                *int    `json:"top_k,omitempty" yaml:"top_k,omitempty"`
	Filter              *string `json:"filter,omitempty" yaml:"filter,omitempty"`
}

// AzureAISearchTool The Azure AI Search tool for grounding agent responses with search index data.
type AzureAISearchTool struct {
	Tool    `json:",inline" yaml:",inline"`
	Indexes []AzureAISearchIndex `json:"indexes" yaml:"indexes"`
}

// A2APreviewTool The A2A (Agent-to-Agent) preview tool for delegating tasks to other agents.
type A2APreviewTool struct {
	Tool                `json:",inline" yaml:",inline"`
	BaseUrl             string  `json:"baseUrl" yaml:"baseUrl"`
	AgentCardPath       *string `json:"agentCardPath,omitempty" yaml:"agentCardPath,omitempty"`
	ProjectConnectionId string  `json:"projectConnectionId" yaml:"projectConnectionId"`
}

// Credential type structs for typed access to connection credentials.
// The ConnectionResource.Credentials field is map[string]any for flexibility,
// but these structs can be used when code needs structured access.

// ApiKeyCredentials holds credentials for ApiKey auth type.
type ApiKeyCredentials struct {
	Key string `json:"key" yaml:"key"`
}

// CustomKeysCredentials holds credentials for CustomKeys auth type.
type CustomKeysCredentials struct {
	Keys map[string]string `json:"keys" yaml:"keys"`
}

// OAuth2Credentials holds credentials for OAuth2 auth type.
type OAuth2Credentials struct {
	AuthUrl        string `json:"authUrl,omitempty" yaml:"authUrl,omitempty"`
	ClientId       string `json:"clientId" yaml:"clientId"`
	ClientSecret   string `json:"clientSecret,omitempty" yaml:"clientSecret,omitempty"`
	DeveloperToken string `json:"developerToken,omitempty" yaml:"developerToken,omitempty"`
	Password       string `json:"password,omitempty" yaml:"password,omitempty"`
	RefreshToken   string `json:"refreshToken,omitempty" yaml:"refreshToken,omitempty"`
	TenantId       string `json:"tenantId,omitempty" yaml:"tenantId,omitempty"`
	Username       string `json:"username,omitempty" yaml:"username,omitempty"`
}

// PATCredentials holds credentials for PAT (Personal Access Token) auth type.
type PATCredentials struct {
	Pat string `json:"pat" yaml:"pat"`
}
