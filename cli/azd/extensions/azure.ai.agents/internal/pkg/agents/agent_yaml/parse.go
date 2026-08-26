// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"azureaiagent/internal/exterrors"
)

var validAgentNamePattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// LoadAndValidateAgentManifest parses YAML content and validates it as an AgentManifest
// Returns the parsed manifest and any validation errors
func LoadAndValidateAgentManifest(manifestYamlContent []byte) (*AgentManifest, error) {
	var manifest AgentManifest
	if err := yaml.Unmarshal(manifestYamlContent, &manifest); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			"YAML content does not conform to AgentManifest format",
			"Ensure the agent manifest is valid YAML and conforms to the AgentManifest schema. "+
				"An easy first check is whether the YAML contains a 'template' field. "+
				"See https://github.com/microsoft/AgentSchema for the expected format.",
		)
	}

	agentDef, err := ExtractAgentDefinition(manifestYamlContent)
	if err != nil {
		return nil, err
	}
	manifest.Template = agentDef

	resourceDefs, err := ExtractResourceDefinitions(manifestYamlContent)
	if err != nil {
		return nil, err
	}
	manifest.Resources = resourceDefs

	templateBytes, _ := yaml.Marshal(manifest.Template)
	if err := ValidateAgentDefinition(templateBytes); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// Returns a specific agent definition based on the "kind" field in the template
func ExtractAgentDefinition(manifestYamlContent []byte) (any, error) {
	var genericManifest map[string]any
	if err := yaml.Unmarshal(manifestYamlContent, &genericManifest); err != nil {
		return nil, fmt.Errorf("YAML content is not valid: %w", err)
	}

	// Handle manifest format with "template" field
	var templateBytes []byte

	if templateValue, exists := genericManifest["template"]; exists && templateValue != nil {
		// Manifest format with "template" field
		template, ok := templateValue.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("template field must be a map, got %T", templateValue)
		}
		if len(template) == 0 {
			return nil, fmt.Errorf(
				"YAML content does not conform to AgentManifest format: template field is empty. " +
					"See https://microsoft.github.io/AgentSchema/reference/agentmanifest for the expected format " +
					"and https://github.com/microsoft-foundry/foundry-samples for examples",
			)
		}
		var err error
		templateBytes, err = yaml.Marshal(template)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal template: %w", err)
		}
	} else {
		// "template" field not found - return error
		return nil, fmt.Errorf(
			"YAML content does not conform to AgentManifest format: must contain 'template' field. " +
				"See https://microsoft.github.io/AgentSchema/reference/agentmanifest for the expected format " +
				"and https://github.com/microsoft-foundry/foundry-samples for examples",
		)
	}

	var agentDef AgentDefinition
	if err := yaml.Unmarshal(templateBytes, &agentDef); err != nil {
		return nil, fmt.Errorf("failed to unmarshal to AgentDefinition: %w", err)
	}

	// Check template properties and assign from manifest if nil
	if agentDef.Name == "" {
		if name, ok := genericManifest["name"].(string); ok {
			agentDef.Name = name
		}
	}
	if agentDef.Description == nil || *agentDef.Description == "" {
		if description, ok := genericManifest["description"].(string); ok {
			agentDef.Description = &description
		}
	}
	if agentDef.Metadata == nil {
		if metadata, ok := genericManifest["metadata"].(map[string]any); ok {
			agentDef.Metadata = &metadata
		}
	}

	switch agentDef.Kind {
	case AgentKindHosted:
		var agent ContainerAgent
		if err := yaml.Unmarshal(templateBytes, &agent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal to ContainerAgent: %w", err)
		}

		agent.AgentDefinition = agentDef
		return agent, nil
	case AgentKindPromptVoice:
		var agent VoiceAgent
		if err := yaml.Unmarshal(templateBytes, &agent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal to VoiceAgent: %w", err)
		}

		agent.AgentDefinition = agentDef
		return agent, nil
	}

	return nil, fmt.Errorf("unrecognized agent kind: %s", agentDef.Kind)
}

// Returns a specific resource type based on the "kind" field in the resource
func ExtractResourceDefinitions(manifestYamlContent []byte) ([]any, error) {
	var genericManifest map[string]any
	if err := yaml.Unmarshal(manifestYamlContent, &genericManifest); err != nil {
		return nil, fmt.Errorf("YAML content is not valid: %w", err)
	}

	resourcesValue, exists := genericManifest["resources"]
	if !exists || resourcesValue == nil {
		return []any{}, nil // Return empty slice if no resources key
	}

	resources, ok := resourcesValue.([]any)
	if !ok {
		return nil, fmt.Errorf("resources field is not a valid array")
	}

	var resourceDefs []any
	for _, resource := range resources {
		resourceBytes, _ := yaml.Marshal(resource)

		var resourceDef Resource
		if err := yaml.Unmarshal(resourceBytes, &resourceDef); err != nil {
			return nil, fmt.Errorf("failed to unmarshal to ResourceDefinition: %w", err)
		}

		switch resourceDef.Kind {
		case ResourceKindModel:
			var modelDef ModelResource
			if err := yaml.Unmarshal(resourceBytes, &modelDef); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to ModelResource: %w", err)
			}
			resourceDefs = append(resourceDefs, modelDef)
		case ResourceKindTool:
			var toolDef ToolResource
			if err := yaml.Unmarshal(resourceBytes, &toolDef); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to ToolResource: %w", err)
			}
			resourceDefs = append(resourceDefs, toolDef)
		case ResourceKindToolbox:
			var toolboxDef ToolboxResource
			if err := yaml.Unmarshal(resourceBytes, &toolboxDef); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to ToolboxResource: %w", err)
			}
			resourceDefs = append(resourceDefs, toolboxDef)
		case ResourceKindConnection:
			var connDef ConnectionResource
			if err := yaml.Unmarshal(resourceBytes, &connDef); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to ConnectionResource: %w", err)
			}
			resourceDefs = append(resourceDefs, connDef)
		default:
			return nil, fmt.Errorf("unrecognized resource kind: %s", resourceDef.Kind)
		}
	}

	return resourceDefs, nil
}

func ExtractToolsDefinitions(template map[string]any) ([]any, error) {
	var tools []any

	toolsValue, exists := template["tools"]
	if exists && toolsValue != nil {
		toolsArray, ok := toolsValue.([]any)
		if !ok {
			return nil, fmt.Errorf("tools field is not a valid array")
		}

		for _, tool := range toolsArray {
			toolBytes, _ := yaml.Marshal(tool)

			var toolDef Tool
			if err := yaml.Unmarshal(toolBytes, &toolDef); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to Tool: %w", err)
			}

			toolDef.Kind = NormalizeToolKind(toolDef.Kind)

			switch toolDef.Kind {
			case ToolKindFunction:
				var functionTool FunctionTool
				if err := yaml.Unmarshal(toolBytes, &functionTool); err != nil {
					return nil, fmt.Errorf("failed to unmarshal to FunctionTool: %w", err)
				}
				tools = append(tools, functionTool)
			case ToolKindCustom:
				var customTool CustomTool
				if err := yaml.Unmarshal(toolBytes, &customTool); err != nil {
					return nil, fmt.Errorf("failed to unmarshal to CustomTool: %w", err)
				}

				if customTool.Connection != nil {
					connectionBytes, _ := yaml.Marshal(customTool.Connection)
					connectionDef, err := ExtractConnectionDefinition(connectionBytes)
					if err != nil {
						return nil, fmt.Errorf("failed to extract connection definition: %w", err)
					}
					customTool.Connection = connectionDef
				}

				tools = append(tools, customTool)
			case ToolKindWebSearch:
				var webSearchTool WebSearchTool
				if err := yaml.Unmarshal(toolBytes, &webSearchTool); err != nil {
					return nil, fmt.Errorf("failed to unmarshal to WebSearchTool: %w", err)
				}

				tools = append(tools, webSearchTool)
			case ToolKindBingGrounding:
				var webSearchTool BingGroundingTool
				if err := yaml.Unmarshal(toolBytes, &webSearchTool); err != nil {
					return nil, fmt.Errorf("failed to unmarshal to BingGroundingTool: %w", err)
				}

				if webSearchTool.Connection != nil {
					connectionBytes, _ := yaml.Marshal(webSearchTool.Connection)
					connectionDef, err := ExtractConnectionDefinition(connectionBytes)
					if err != nil {
						return nil, fmt.Errorf("failed to extract connection definition: %w", err)
					}
					webSearchTool.Connection = connectionDef
				}

				tools = append(tools, webSearchTool)
			case ToolKindFileSearch:
				var fileSearchTool FileSearchTool
				if err := yaml.Unmarshal(toolBytes, &fileSearchTool); err != nil {
					return nil, fmt.Errorf("failed to unmarshal to FileSearchTool: %w", err)
				}

				if fileSearchTool.Connection != nil {
					connectionBytes, _ := yaml.Marshal(fileSearchTool.Connection)
					connectionDef, err := ExtractConnectionDefinition(connectionBytes)
					if err != nil {
						return nil, fmt.Errorf("failed to extract connection definition: %w", err)
					}
					fileSearchTool.Connection = connectionDef
				}

				tools = append(tools, fileSearchTool)
			case ToolKindMcp:
				var mcpTool McpTool
				if err := yaml.Unmarshal(toolBytes, &mcpTool); err != nil {
					return nil, fmt.Errorf("failed to unmarshal to McpTool: %w", err)
				}

				if mcpTool.Connection != nil {
					connectionBytes, _ := yaml.Marshal(mcpTool.Connection)
					connectionDef, err := ExtractConnectionDefinition(connectionBytes)
					if err != nil {
						return nil, fmt.Errorf("failed to extract connection definition: %w", err)
					}
					mcpTool.Connection = connectionDef
				}

				tools = append(tools, mcpTool)
			case ToolKindOpenApi:
				var openApiTool OpenApiTool
				if err := yaml.Unmarshal(toolBytes, &openApiTool); err != nil {
					return nil, fmt.Errorf("failed to unmarshal to OpenApiTool: %w", err)
				}

				if openApiTool.Connection != nil {
					connectionBytes, _ := yaml.Marshal(openApiTool.Connection)
					connectionDef, err := ExtractConnectionDefinition(connectionBytes)
					if err != nil {
						return nil, fmt.Errorf("failed to extract connection definition: %w", err)
					}
					openApiTool.Connection = connectionDef
				}

				tools = append(tools, openApiTool)
			case ToolKindCodeInterpreter:
				var codeInterpreterTool CodeInterpreterTool
				if err := yaml.Unmarshal(toolBytes, &codeInterpreterTool); err != nil {
					return nil, fmt.Errorf("failed to unmarshal to CodeInterpreterTool: %w", err)
				}
				tools = append(tools, codeInterpreterTool)
			case ToolKindAzureAiSearch:
				var azureAiSearchTool AzureAISearchTool
				if err := yaml.Unmarshal(toolBytes, &azureAiSearchTool); err != nil {
					return nil, fmt.Errorf("failed to unmarshal to AzureAISearchTool: %w", err)
				}
				tools = append(tools, azureAiSearchTool)
			case ToolKindA2APreview:
				var a2aTool A2APreviewTool
				if err := yaml.Unmarshal(toolBytes, &a2aTool); err != nil {
					return nil, fmt.Errorf("failed to unmarshal to A2APreviewTool: %w", err)
				}
				tools = append(tools, a2aTool)
			default:
				return nil, fmt.Errorf("unrecognized tool kind: %s", toolDef.Kind)
			}
		}
	}

	return tools, nil
}

func ExtractConnectionDefinition(connectionBytes []byte) (any, error) {
	var connectionDef Connection
	if err := yaml.Unmarshal(connectionBytes, &connectionDef); err != nil {
		return nil, fmt.Errorf("failed to unmarshal to ConnectionDefinition: %w", err)
	}

	switch connectionDef.Kind {
	case ConnectionKindReference:
		var refConn ReferenceConnection
		if err := yaml.Unmarshal(connectionBytes, &refConn); err != nil {
			return nil, fmt.Errorf("failed to unmarshal to ReferenceConnection: %w", err)
		}
		return refConn, nil
	case ConnectionKindRemote:
		var remoteConn RemoteConnection
		if err := yaml.Unmarshal(connectionBytes, &remoteConn); err != nil {
			return nil, fmt.Errorf("failed to unmarshal to RemoteConnection: %w", err)
		}
		return remoteConn, nil
	case ConnectionKindApiKey:
		var apiKeyConn ApiKeyConnection
		if err := yaml.Unmarshal(connectionBytes, &apiKeyConn); err != nil {
			return nil, fmt.Errorf("failed to unmarshal to ApiKeyConnection: %w", err)
		}
		return apiKeyConn, nil
	case ConnectionKindAnonymous:
		var anonymousConn AnonymousConnection
		if err := yaml.Unmarshal(connectionBytes, &anonymousConn); err != nil {
			return nil, fmt.Errorf("failed to unmarshal to AnonymousConnection: %w", err)
		}
		return anonymousConn, nil
	default:
		return nil, fmt.Errorf("unrecognized connection kind: %s", connectionDef.Kind)
	}
}

// ValidateAgentManifest performs basic validation of an AgentManifest
// Returns an error if the manifest is invalid, nil if valid
func ValidateAgentDefinition(templateBytes []byte) error {
	var errors []string

	var agentDef AgentDefinition
	if err := yaml.Unmarshal(templateBytes, &agentDef); err != nil {
		errors = append(errors, "failed to parse template to determine agent kind")
	} else {
		// Validate the kind is supported
		if !IsValidAgentKind(agentDef.Kind) {
			validKinds := ValidAgentKinds()
			validKindStrings := make([]string, len(validKinds))
			for i, validKind := range validKinds {
				validKindStrings[i] = string(validKind)
			}
			errors = append(errors, fmt.Sprintf("template.kind must be one of: %v, got '%s'", validKindStrings, agentDef.Kind))
		} else {
			if err := ValidateAgentName(agentDef.Name); err != nil {
				errors = append(errors, fmt.Sprintf("template.name not in valid format: %v", err))
			}

			// Only hosted agents carry policies to the service, so a moderation block on any
			// other kind would be dropped silently instead of enforced.
			if agentDef.Kind != AgentKindHosted {
				errors = append(errors,
					validateInvocationsModerationKind(templateBytes, agentDef.Kind)...)
			}

			switch AgentKind(agentDef.Kind) {
			case AgentKindHosted:
				var agent ContainerAgent
				if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
					raiPolicyCount := 0
					for i, policy := range agent.Policies {
						switch policy.Type {
						case PolicyTypeRai:
							raiPolicyCount++
							if policy.RaiPolicyName == "" {
								errors = append(errors, fmt.Sprintf(
									"policies[%d] of type '%s' requires a policy name "+
										"('raiPolicyName' in azure.yaml, 'rai_policy_name' in agent.yaml)",
									i, policy.Type))
							}
							errors = append(errors,
								validateInvocationsModeration(i, policy.InvocationsModeration, agent.Protocols)...)
						case "":
							errors = append(errors, fmt.Sprintf(
								"policies[%d] requires a type", i))
						default:
							errors = append(errors, fmt.Sprintf(
								"policies[%d] has an unsupported type '%s' (supported: %s)",
								i, policy.Type, PolicyTypeRai))
						}
					}
					// rai_config carries a single policy on the wire, so only the first
					// rai_policy would ever reach the service. Reject the ambiguity rather
					// than silently dropping the rest.
					if raiPolicyCount > 1 {
						errors = append(errors, fmt.Sprintf(
							"policies declares %d policies of type '%s', but only one is supported",
							raiPolicyCount, PolicyTypeRai))
					}
					// TODO: Do we need this?
					// if len(agent.Models) == 0 {
					// 	errors = append(errors, "template.models is required and must not be empty")
					// }
				} else {
					errors = append(errors, fmt.Sprintf("failed to unmarshal to ContainerAgent: %v", err))
				}
			case AgentKindWorkflow:
				var agent Workflow
				if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
					if agent.Name == "" {
						errors = append(errors, "template.name is required")
					}
					// Workflow doesn't have models, so no model validation needed
				} else {
					errors = append(errors, fmt.Sprintf("failed to unmarshal to Workflow: %v", err))
				}
			case AgentKindPromptVoice:
				var agent VoiceAgent
				if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
					if agent.Model == nil || strings.TrimSpace(agent.Model.Id) == "" {
						errors = append(errors, "template.model.id is required for a prompt-voice agent")
					}
					if agent.ModelType != "" &&
						agent.ModelType != VoiceModelTypeManaged &&
						agent.ModelType != VoiceModelTypeSelfDeployed {
						errors = append(errors, fmt.Sprintf(
							"template.model_type '%s' is not supported; use '%s' or '%s'",
							agent.ModelType, VoiceModelTypeManaged, VoiceModelTypeSelfDeployed))
					}
					errors = append(errors, validateVoiceAgentAdvancedConfig(agent)...)
				} else {
					errors = append(errors, fmt.Sprintf("failed to unmarshal to VoiceAgent: %v", err))
				}
			}
		}
	}

	if len(errors) > 0 {
		var errorMsg strings.Builder
		errorMsg.WriteString("validation failed:")
		for _, err := range errors {
			errorMsg.WriteString(fmt.Sprintf("\n  - %s", err))
		}
		return fmt.Errorf("%s", errorMsg.String())
	}

	return nil
}

func validateVoiceAgentAdvancedConfig(agent VoiceAgent) []string {
	var errors []string
	if agent.OutputModalities != nil && len(agent.OutputModalities) == 0 {
		errors = append(errors, "template.output_modalities must not be empty when specified")
	}
	for i, modality := range agent.OutputModalities {
		if strings.TrimSpace(modality) == "" {
			errors = append(errors, fmt.Sprintf("template.output_modalities[%d] must not be blank", i))
		}
	}
	if agent.ParallelToolCalls != nil {
		errors = append(errors,
			"template.parallel_tool_calls is not currently supported by the prompt voice runtime; "+
				"remove it from the agent definition")
	}
	if err := validateVoiceMaxOutputTokens(agent.MaxOutputTokens); err != nil {
		errors = append(errors, err.Error())
	}

	if agent.Audio == nil {
		return append(errors, validateVoiceIncludeTranscriptionCompatibility(agent, "")...)
	}
	transcriptionModel := ""
	if agent.Audio.Input != nil {
		errors = append(errors, validateVoiceAudioFormat("template.audio.input.format", agent.Audio.Input.Format)...)
		if nr := agent.Audio.Input.NoiseReduction; nr != nil && strings.TrimSpace(nr.Type) == "" {
			errors = append(errors, "template.audio.input.noise_reduction.type must not be blank")
		}
		if td := agent.Audio.Input.TurnDetection; td != nil {
			if strings.TrimSpace(td.Type) == "" {
				errors = append(errors, "template.audio.input.turn_detection.type must not be blank")
			}
			if td.Threshold != nil && (*td.Threshold <= 0 || *td.Threshold > 1) {
				errors = append(errors, "template.audio.input.turn_detection.threshold must be greater than 0 and <= 1")
			}
			if td.PrefixPaddingMs != nil && *td.PrefixPaddingMs < 0 {
				errors = append(errors, "template.audio.input.turn_detection.prefix_padding_ms must be >= 0")
			}
			if td.SilenceDurationMs != nil && *td.SilenceDurationMs < 0 {
				errors = append(errors, "template.audio.input.turn_detection.silence_duration_ms must be >= 0")
			}
			if td.SpeechDurationMs != nil && *td.SpeechDurationMs < 0 {
				errors = append(errors, "template.audio.input.turn_detection.speech_duration_ms must be >= 0")
			}
		}
		if agent.Audio.Input.Transcription != nil {
			transcriptionModel = agent.Audio.Input.Transcription.Model
		}
	}
	if agent.Audio.Output != nil {
		errors = append(errors, validateVoiceAudioFormat("template.audio.output.format", agent.Audio.Output.Format)...)
		if voice := agent.Audio.Output.Voice; voice != nil {
			if strings.TrimSpace(voice.Type) == "" {
				errors = append(errors, "template.audio.output.voice.type must not be blank")
			}
			if strings.TrimSpace(voice.Name) == "" {
				errors = append(errors, "template.audio.output.voice.name must not be blank")
			}
		}
		if speed := agent.Audio.Output.Speed; speed != nil && (*speed < 0.25 || *speed > 1.5) {
			errors = append(errors, "template.audio.output.speed must be between 0.25 and 1.5")
		}
	}
	return append(errors, validateVoiceIncludeTranscriptionCompatibility(agent, transcriptionModel)...)
}

func validateVoiceMaxOutputTokens(value any) error {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("template.max_output_tokens must be a non-empty string or an integer")
		}
		return nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return nil
	case float32:
		if math.Trunc(float64(v)) == float64(v) {
			return nil
		}
	case float64:
		if math.Trunc(v) == v {
			return nil
		}
	}
	return fmt.Errorf("template.max_output_tokens must be a string or an integer")
}

func validateVoiceIncludeTranscriptionCompatibility(agent VoiceAgent, transcriptionModel string) []string {
	if !slices.Contains(agent.Include, "item.input_audio_transcription.phrases") {
		return nil
	}
	model := strings.TrimSpace(transcriptionModel)
	if model == "" {
		model = defaultVoiceInputTranscriptionModel
	}
	if model == "azure-speech" || model == "azure-fast-transcription" {
		return nil
	}
	return []string{
		"template.include item.input_audio_transcription.phrases requires " +
			"template.audio.input.transcription.model to be azure-speech or azure-fast-transcription",
	}
}

func validateVoiceAudioFormat(path string, format *VoiceAudioFormat) []string {
	if format == nil {
		return nil
	}
	var errors []string
	formatType := strings.TrimSpace(format.Type)
	if formatType == "" {
		errors = append(errors, path+".type must not be blank")
	} else if formatType != "audio/pcm" && formatType != "audio/pcmu" && formatType != "audio/pcma" {
		errors = append(errors, path+".type must be 'audio/pcm', 'audio/pcmu', or 'audio/pcma'")
	}
	if format.Rate != nil && *format.Rate <= 0 {
		errors = append(errors, path+".rate must be greater than 0")
	}
	return errors
}

// Validate that the agent name matches the expected deployable format
func ValidateAgentName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}

	if !validAgentNamePattern.MatchString(name) {
		return fmt.Errorf(
			"name must start and end with an alphanumeric character, " +
				"can contain hyphens in the middle, and be 1-63 characters long",
		)
	}

	return nil
}

// validateInvocationsModerationKind reports invocationsModeration blocks declared on a
// non-hosted agent. Only ContainerAgent carries policies through to the service, so such a
// block would be dropped silently rather than enforced. It reads a minimal envelope because
// the kind-specific structs for the other kinds have no policies field at all.
//
// The policies are decoded as raw maps rather than into [Policy] because the two authoring
// surfaces spell the key differently: a standalone agent.yaml uses the snake_case YAML tags,
// while an inline azure.yaml service is validated from the raw property map and therefore
// still carries the camelCase keys the user authored. Decoding into [Policy] would only ever
// match the snake_case spelling and let every inline definition bypass this check.
func validateInvocationsModerationKind(templateBytes []byte, kind AgentKind) []string {
	var envelope struct {
		Policies []map[string]any `json:"policies,omitempty" yaml:"policies,omitempty"`
	}
	if err := yaml.Unmarshal(templateBytes, &envelope); err != nil {
		// A malformed document is reported by the kind-specific parse instead.
		return nil
	}

	var errors []string
	for i, policy := range envelope.Policies {
		if !hasInvocationsModeration(policy) {
			continue
		}
		errors = append(errors, fmt.Sprintf(
			"policies[%d] invocationsModeration is only supported for '%s' agents, got kind '%s'",
			i, AgentKindHosted, kind))
	}
	return errors
}

// hasInvocationsModeration reports whether a raw policy map declares a moderation block under
// either the camelCase (azure.yaml) or snake_case (agent.yaml) spelling.
func hasInvocationsModeration(policy map[string]any) bool {
	for _, key := range []string{"invocationsModeration", "invocations_moderation"} {
		if value, ok := policy[key]; ok && value != nil {
			return true
		}
	}
	return false
}

// validateInvocationsModeration checks a policy's invocations-moderation block against the
// same structural rules the Agents service applies at create time, so a misconfiguration is
// caught locally instead of surfacing later as an opaque 'invalid_payload' response.
//
// It deliberately does not compile the JSONPath expressions; malformed paths are still
// reported by the service.
func validateInvocationsModeration(
	index int,
	moderation *InvocationsModeration,
	protocols []ProtocolVersionRecord,
) []string {
	if moderation == nil {
		return nil
	}

	prefix := fmt.Sprintf("policies[%d] invocationsModeration", index)
	var errors []string

	// The rest of the block is meaningless on an agent that never serves the invocations
	// path, so report only the root cause rather than cascading field-level errors.
	if !exposesInvocationsProtocol(protocols) {
		return []string{fmt.Sprintf(
			"%s is only supported for agents that expose the '%s' protocol; "+
				"add it to 'protocols' or remove the moderation block",
			prefix, InvocationsProtocol)}
	}

	inputContentType, err := resolveInvocationContentType(moderation.InputContentType)
	if err != nil {
		errors = append(errors, fmt.Sprintf("%s.inputContentType %v", prefix, err))
	}

	outputContentType, err := resolveInvocationContentType(moderation.OutputContentType)
	if err != nil {
		errors = append(errors, fmt.Sprintf("%s.outputContentType %v", prefix, err))
	}

	allowsNonStreaming, allowsStreaming, err := resolveInvocationResponseMode(moderation.ResponseMode)
	if err != nil {
		errors = append(errors, fmt.Sprintf("%s.responseMode %v", prefix, err))
	}

	if inputContentType == InvocationContentTypeJSON && len(moderation.InputPaths) == 0 {
		errors = append(errors, fmt.Sprintf(
			"%s.inputPaths is required when inputContentType is '%s'",
			prefix, InvocationContentTypeJSON))
	}

	if allowsNonStreaming && outputContentType == InvocationContentTypeJSON &&
		len(moderation.OutputPaths) == 0 {
		errors = append(errors, fmt.Sprintf(
			"%s.outputPaths is required when responseMode includes non-streaming "+
				"and outputContentType is '%s'",
			prefix, InvocationContentTypeJSON))
	}

	if allowsStreaming && outputContentType == InvocationContentTypeJSON &&
		len(moderation.StreamSelectors) == 0 {
		errors = append(errors, fmt.Sprintf(
			"%s.streamSelectors is required when responseMode includes streaming "+
				"and outputContentType is '%s'",
			prefix, InvocationContentTypeJSON))
	}

	for i, selector := range moderation.StreamSelectors {
		if strings.TrimSpace(selector.EventType) == "" {
			errors = append(errors, fmt.Sprintf(
				"%s.streamSelectors[%d].eventType is required and must be non-empty", prefix, i))
		}
	}

	return errors
}

// resolveInvocationContentType normalizes an optional content type, defaulting to JSON.
// An unrecognized value yields an empty type alongside the error so callers naturally skip
// the downstream rules that depend on it instead of reporting cascading failures. Suppressing
// those rules is deliberate: the corrected value determines whether paths are required at all,
// so guessing one here would risk demanding paths a 'text' agent never needs.
func resolveInvocationContentType(value string) (string, error) {
	switch value {
	case "":
		return InvocationContentTypeJSON, nil
	case InvocationContentTypeJSON, InvocationContentTypeText:
		return value, nil
	default:
		return "", fmt.Errorf("must be '%s' or '%s', got '%s'",
			InvocationContentTypeJSON, InvocationContentTypeText, value)
	}
}

// resolveInvocationResponseMode reports which output gates a response mode arms. Mode "both"
// arms both, but the proxy still runs exactly one gate per response, chosen from the actual
// response Content-Type.
func resolveInvocationResponseMode(value string) (allowsNonStreaming bool, allowsStreaming bool, err error) {
	switch value {
	case "":
		return false, false, fmt.Errorf("is required (one of '%s', '%s', '%s')",
			InvocationResponseModeNonStreaming,
			InvocationResponseModeStreaming,
			InvocationResponseModeBoth)
	case InvocationResponseModeNonStreaming:
		return true, false, nil
	case InvocationResponseModeStreaming:
		return false, true, nil
	case InvocationResponseModeBoth:
		return true, true, nil
	default:
		return false, false, fmt.Errorf("must be one of '%s', '%s', '%s', got '%s'",
			InvocationResponseModeNonStreaming,
			InvocationResponseModeStreaming,
			InvocationResponseModeBoth,
			value)
	}
}

// exposesInvocationsProtocol reports whether the agent declares the HTTP invocations protocol.
func exposesInvocationsProtocol(protocols []ProtocolVersionRecord) bool {
	return slices.ContainsFunc(protocols, func(record ProtocolVersionRecord) bool {
		return record.Protocol == InvocationsProtocol
	})
}
