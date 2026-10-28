// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"
)

var validAgentNamePattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
var e164Pattern = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
var acsTpeRawIDPattern = regexp.MustCompile(
	`^28:orgid:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-` +
		`[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var telephonyTargetNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var canonicalUUIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidateAgentDefinition validates a direct agent definition.
func ValidateAgentDefinition(templateBytes []byte) error {
	var errors []string

	var agentDef AgentDefinition
	if err := yaml.Unmarshal(templateBytes, &agentDef); err != nil {
		errors = append(errors, "failed to parse agent definition to determine agent kind")
	} else {
		// Validate the kind is supported
		if !IsValidAgentKind(agentDef.Kind) {
			validKinds := ValidAgentKinds()
			validKindStrings := make([]string, len(validKinds))
			for i, validKind := range validKinds {
				validKindStrings[i] = string(validKind)
			}
			errors = append(errors, fmt.Sprintf("kind must be one of: %v, got '%s'", validKindStrings, agentDef.Kind))
		} else {
			if err := ValidateAgentName(agentDef.Name); err != nil {
				errors = append(errors, fmt.Sprintf("name not in valid format: %v", err))
			}

			var fields map[string]yaml.Node
			if fieldErr := yaml.Unmarshal(templateBytes, &fields); fieldErr == nil {
				if modelType, ok := fields["model_type"]; ok &&
					modelType.Kind == yaml.ScalarNode && modelType.Value == string(VoiceModelTypeHostedAgent) &&
					!IsVoiceAgentKind(agentDef.Kind) {
					errors = append(errors,
						"modelType hosted_agent is not supported; use conversationEngine")
				}
				if _, ok := fields["target_agent"]; ok && !IsVoiceAgentKind(agentDef.Kind) {
					errors = append(errors,
						"targetAgent is not supported; use conversationEngine")
				}
				if _, ok := fields["conversation_engine"]; ok && !IsVoiceAgentKind(agentDef.Kind) {
					errors = append(errors,
						"conversationEngine is only valid for voice agents")
				}
				if _, hasToolbox := fields["toolbox"]; hasToolbox && !IsVoiceAgentKind(agentDef.Kind) {
					if _, hasToolboxes := fields["toolboxes"]; hasToolboxes {
						errors = append(errors,
							"cannot specify both 'toolbox' and 'toolboxes'; remove 'toolbox' and use 'toolboxes'")
					} else {
						errors = append(errors,
							"field 'toolbox' is no longer supported; use 'toolboxes'")
					}
				}
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
										"('raiPolicyName' in the direct/root-$ref definition)",
									i, policy.Type))
							} else if err := ValidateRaiPolicyName(policy.RaiPolicyName); err != nil {
								errors = append(errors, fmt.Sprintf("policies[%d]: %v", i, err))
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
					// 	errors = append(errors, "models is required and must not be empty")
					// }
				} else {
					errors = append(errors, fmt.Sprintf("failed to unmarshal to ContainerAgent: %v", err))
				}
			case AgentKindWorkflow:
				var agent Workflow
				if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
					if agent.Name == "" {
						errors = append(errors, "name is required")
					}
					// Workflow doesn't have models, so no model validation needed
				} else {
					errors = append(errors, fmt.Sprintf("failed to unmarshal to Workflow: %v", err))
				}
			case AgentKindPrompt:
				var agent PromptAgent
				if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
					if strings.TrimSpace(agent.Model) == "" {
						errors = append(errors, "model is required for prompt agents")
					}
					if strings.TrimSpace(agent.Instructions) == "" {
						errors = append(errors, "instructions is required for prompt agents")
					}
					for i, policy := range agent.Policies {
						switch policy.Type {
						case PolicyTypeRai:
							if policy.RaiPolicyName == "" {
								errors = append(errors, fmt.Sprintf(
									"policies[%d] of type '%s' requires a policy name (raiPolicyName)",
									i, policy.Type))
							} else if err := ValidateRaiPolicyName(policy.RaiPolicyName); err != nil {
								errors = append(errors, fmt.Sprintf("policies[%d]: %v", i, err))
							}
						case "":
							errors = append(errors, fmt.Sprintf(
								"policies[%d] requires a type", i))
						default:
							errors = append(errors, fmt.Sprintf(
								"policies[%d] has an unsupported type '%s' (supported: %s)",
								i, policy.Type, PolicyTypeRai))
						}
					}
				} else {
					errors = append(errors, fmt.Sprintf("failed to unmarshal to PromptAgent: %v", err))
				}
			case AgentKindPromptVoice, AgentKindVoice:
				var agent VoiceAgent
				if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
					var fields map[string]yaml.Node
					if fieldErr := yaml.Unmarshal(templateBytes, &fields); fieldErr == nil {
						for _, field := range []string{
							"subagent_config", "subagentConfig", "interim_response", "interimResponse",
						} {
							if _, ok := fields[field]; ok {
								errors = append(errors,
									field+" is not supported by this azd version; remove it or upgrade azd")
							}
						}
						if _, hasCodeConfig := fields["code_configuration"]; hasCodeConfig {
							errors = append(errors,
								"codeConfiguration is not supported for a prompt-voice agent; "+
									"configure code settings on the hosted target")
						}
						if _, hasSessionConfig := fields["session_configuration"]; hasSessionConfig {
							errors = append(errors,
								"sessionConfiguration is not supported for a prompt-voice agent; "+
									"configure session settings on the hosted target")
						}
						if _, hasEnvironmentVariables := fields["environment_variables"]; hasEnvironmentVariables {
							errors = append(errors,
								"environmentVariables is not supported for a prompt-voice agent; "+
									"configure environment variables on the hosted target")
						}
						if _, hasToolbox := fields["toolbox"]; hasToolbox {
							errors = append(errors,
								"toolbox is not supported for a prompt-voice agent; "+
									"remove it from the agent definition")
						}
						if _, hasToolboxes := fields["toolboxes"]; hasToolboxes {
							errors = append(errors,
								"toolboxes is not supported for a prompt-voice agent; "+
									"remove it from the agent definition")
						}
						if _, hasProtocols := fields["protocols"]; hasProtocols {
							errors = append(errors,
								"protocols is not supported for a prompt-voice agent; "+
									"configure protocols on the hosted target")
						}
						if _, hasPolicies := fields["policies"]; hasPolicies {
							errors = append(errors,
								"policies is not supported for prompt-voice agents; "+
									"move target-owned policies to the hosted target")
						}
					}
					_, hasLegacyTarget := fields["target_agent"]
					if isHostedVoiceWrapper(agent) || hasLegacyTarget {
						if agent.ModelType == VoiceModelTypeHostedAgent || hasLegacyTarget {
							errors = append(errors,
								"modelType hosted_agent and targetAgent are not supported; "+
									"use conversationEngine")
						}
						if agent.ConversationEngine != nil && agent.ModelType != "" &&
							agent.ModelType != VoiceModelTypeHostedAgent {
							errors = append(errors,
								"conversationEngine cannot be combined with modelType")
						}
						if agent.ConversationEngine != nil &&
							strings.EqualFold(strings.TrimSpace(agent.ConversationEngine.Type), "hosted_agent") &&
							strings.TrimSpace(agent.ConversationEngine.Name) == "" {
							errors = append(errors,
								"conversationEngine.name is required when "+
									"conversationEngine.type is 'hosted_agent'")
						}
						if agent.ConversationEngine != nil && agent.ConversationEngine.Version != "" &&
							agent.ConversationEngine.Version != "deployed" {
							errors = append(errors,
								"conversationEngine.version must be 'deployed' when specified")
						}
						if agent.Model != nil {
							errors = append(errors, "model is not allowed for hosted voice wrappers")
						}
						if agent.Instructions != nil || len(agent.Tools) > 0 || agent.ToolChoice != nil {
							errors = append(errors,
								"model, modelType, instructions, tools, and toolChoice belong to the target hosted agent")
						}
					} else {
						if agent.Model == nil || strings.TrimSpace(agent.Model.Id) == "" {
							errors = append(errors, "model.id is required for a prompt-voice agent")
						}
						if agent.ConversationEngine != nil {
							errors = append(errors,
								"conversationEngine is only valid for hosted voice wrappers")
						}
					}
					if agent.ModelType != "" && agent.ModelType != VoiceModelTypeManaged &&
						agent.ModelType != VoiceModelTypeSelfDeployed && agent.ModelType != VoiceModelTypeHostedAgent {
						errors = append(errors, fmt.Sprintf(
							"modelType '%s' is not supported; use '%s' or '%s'. "+
								"For hosted voice, use conversationEngine.",
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

func isHostedVoiceWrapper(agent VoiceAgent) bool {
	return agent.ModelType == VoiceModelTypeHostedAgent || agent.TargetAgent != nil ||
		(agent.ConversationEngine != nil &&
			strings.EqualFold(strings.TrimSpace(agent.ConversationEngine.Type), "hosted_agent"))
}

func validateVoiceAgentAdvancedConfig(agent VoiceAgent) []string {
	var errors []string
	if agent.InputSchema != nil || agent.OutputSchema != nil {
		errors = append(errors,
			"inputSchema and outputSchema are not properties of the GA voice agent definition; remove them")
	}
	if agent.OutputModalities != nil && len(agent.OutputModalities) == 0 {
		errors = append(errors, "outputModalities must not be empty when specified")
	}
	for i, modality := range agent.OutputModalities {
		if strings.TrimSpace(modality) == "" {
			errors = append(errors, fmt.Sprintf("outputModalities[%d] must not be blank", i))
		}
	}
	if len(agent.Handoff) > 0 {
		errors = append(errors,
			"handoff is not supported by the GA voice agent API; remove it and use telephony transferTargets where applicable")
	}
	if err := validateVoiceMaxOutputTokens(agent.MaxOutputTokens); err != nil {
		errors = append(errors, err.Error())
	}
	errors = append(errors, validateVoiceTelephony(agent.Telephony)...)

	if agent.Audio == nil {
		return append(errors, validateVoiceIncludeTranscriptionCompatibility(agent, "")...)
	}
	transcriptionModel := ""
	if agent.Audio.Input != nil {
		errors = append(errors, validateVoiceAudioFormat("audio.input.format", agent.Audio.Input.Format)...)
		if nr := agent.Audio.Input.NoiseReduction; nr != nil && strings.TrimSpace(nr.Type) == "" {
			errors = append(errors, "audio.input.noiseReduction.type must not be blank")
		}
		if td := agent.Audio.Input.TurnDetection; td != nil {
			if strings.TrimSpace(td.Type) == "" {
				errors = append(errors, "audio.input.turnDetection.type must not be blank")
			}
			if td.Threshold != nil && (!isFinite(*td.Threshold) || *td.Threshold <= 0 || *td.Threshold > 1) {
				errors = append(errors, "audio.input.turnDetection.threshold must be greater than 0 and <= 1")
			}
			if td.PrefixPaddingMs != nil && *td.PrefixPaddingMs < 0 {
				errors = append(errors, "audio.input.turnDetection.prefixPaddingMs must be >= 0")
			}
			if td.SilenceDurationMs != nil && *td.SilenceDurationMs < 0 {
				errors = append(errors, "audio.input.turnDetection.silenceDurationMs must be >= 0")
			}
			if td.SpeechDurationMs != nil && *td.SpeechDurationMs < 0 {
				errors = append(errors, "audio.input.turnDetection.speechDurationMs must be >= 0")
			}
		}
		if agent.Audio.Input.Transcription != nil {
			transcriptionModel = agent.Audio.Input.Transcription.Model
		}
	}
	if agent.Audio.Output != nil {
		errors = append(errors, validateVoiceAudioFormat("audio.output.format", agent.Audio.Output.Format)...)
		if voice := agent.Audio.Output.Voice; voice != nil {
			if strings.TrimSpace(voice.Type) == "" {
				errors = append(errors, "audio.output.voice.type must not be blank")
			}
			if strings.TrimSpace(voice.Name) == "" {
				errors = append(errors, "audio.output.voice.name must not be blank")
			}
			if voice.Rate != nil && strings.TrimSpace(*voice.Rate) != "" {
				errors = append(errors,
					"audio.output.voice.rate is not supported by the GA voice agent API; use audio.output.speed")
			}
		}
		if speed := agent.Audio.Output.Speed; speed != nil && (!isFinite(*speed) || *speed < 0.25 || *speed > 1.5) {
			errors = append(errors, "audio.output.speed must be between 0.25 and 1.5")
		}
	}
	return append(errors, validateVoiceIncludeTranscriptionCompatibility(agent, transcriptionModel)...)
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validateVoiceTelephony(telephony *VoiceTelephony) []string {
	if telephony == nil {
		return nil
	}
	var errors []string
	if len(telephony.Bindings) == 0 {
		errors = append(errors, "telephony.bindings must not be empty")
	}
	seen := map[string]struct{}{}
	for i, binding := range telephony.Bindings {
		path := fmt.Sprintf("telephony.bindings[%d]", i)
		provider := strings.TrimSpace(binding.Provider)
		identifier := strings.TrimSpace(binding.Identifier)
		if provider != "" && identifier != "" {
			bindingID := telephonyValidationProvider(provider) + ":" + identifier
			if _, ok := seen[bindingID]; ok {
				errors = append(errors, path+" duplicates telephony binding "+bindingID)
			} else {
				seen[bindingID] = struct{}{}
			}
		}
		if provider == "" {
			errors = append(errors, path+".provider is required")
		}
		if identifier == "" {
			errors = append(errors, path+".identifier is required")
		}
		if strings.TrimSpace(binding.Connection) == "" {
			errors = append(errors, path+".connection is required")
		}
		switch provider {
		case "acs":
			if identifier == "" {
				continue
			}
			if !acsTpeRawIDPattern.MatchString(identifier) {
				errors = append(errors, path+".identifier must be 28:orgid:<guid> for acs")
			}
		case "twilio":
			if identifier != "" && !e164Pattern.MatchString(identifier) {
				errors = append(errors, path+".identifier must be +<E.164> for twilio")
			}
		case "":
		default:
			errors = append(errors, path+".provider must be acs or twilio")
		}
		errors = append(errors, validateVoiceTransferTargets(path, binding.TransferTargets)...)
	}
	return errors
}

// ValidateVoiceTelephony validates authored telephony configuration before it is converted or deployed.
func ValidateVoiceTelephony(telephony *VoiceTelephony) error {
	errors := validateVoiceTelephony(telephony)
	if len(errors) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(errors, "; "))
}

func validateVoiceTransferTargets(bindingPath string, targets []map[string]any) []string {
	if targets == nil {
		return nil
	}
	var errors []string
	names := map[string]struct{}{}
	for i, target := range targets {
		path := fmt.Sprintf("%s.transferTargets[%d]", bindingPath, i)
		for key := range target {
			if key != "name" && key != "description" && key != "destination" {
				errors = append(errors, path+" contains unsupported field "+fmt.Sprintf("%q", key))
			}
		}
		name, nameOK := target["name"].(string)
		description, descriptionOK := target["description"].(string)
		if !nameOK || !telephonyTargetNamePattern.MatchString(name) {
			errors = append(errors, path+".name must be 1-64 ASCII letters, digits, underscores, or hyphens and start with a letter or digit")
		} else if _, duplicate := names[strings.ToLower(name)]; duplicate {
			errors = append(errors, path+".name duplicates transfer target "+name)
		} else {
			names[strings.ToLower(name)] = struct{}{}
		}
		if !descriptionOK || strings.TrimSpace(description) == "" {
			errors = append(errors, path+".description is required")
		} else if len(utf16.Encode([]rune(description))) > 1024 {
			errors = append(errors, path+".description is too long")
		}
		destination, ok := target["destination"].(map[string]any)
		if !ok {
			errors = append(errors, path+".destination must be an object")
			continue
		}
		for key := range destination {
			if key != "kind" && key != "value" {
				errors = append(errors, path+".destination contains unsupported field "+fmt.Sprintf("%q", key))
			}
		}
		kind, kindOK := destination["kind"].(string)
		value, valueOK := destination["value"].(string)
		if !kindOK || !valueOK || value == "" {
			errors = append(errors, path+".destination.kind and value are required strings")
			continue
		}
		if len(utf16.Encode([]rune(value))) > 2048 {
			errors = append(errors, path+".destination.value is too long")
			continue
		}
		switch kind {
		case "pstn":
			if !e164Pattern.MatchString(value) {
				errors = append(errors, path+".destination.value must be +<E.164> for pstn")
			}
		case "teams":
			if !isTeamsTransferTarget(value) {
				errors = append(errors, path+".destination.value must be a canonical object GUID or 28:orgid:<guid> for teams")
			}
		case "sip":
			if !isAuthoredSIPUri(value) {
				errors = append(errors, path+".destination.value must be a sip or sips URI with user@host")
			}
		default:
			errors = append(errors, path+".destination.kind must be pstn, teams, or sip")
		}
	}
	return errors
}

func isTeamsTransferTarget(value string) bool {
	const prefix = "28:orgid:"
	if len(value) >= len(prefix) && strings.EqualFold(value[:len(prefix)], prefix) {
		value = value[len(prefix):]
	}
	if !canonicalUUIDPattern.MatchString(value) {
		return false
	}
	_, err := uuid.Parse(value)
	return err == nil
}

func isAuthoredSIPUri(value string) bool {
	if strings.ContainsAny(value, " \t\r\n") || strings.Contains(value, "#") || hasMalformedPercentEscape(value) {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed == nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "sip" && scheme != "sips" {
		return false
	}
	address := value[strings.Index(value, ":")+1:]
	if suffix := strings.IndexAny(address, ";?"); suffix >= 0 {
		address = address[:suffix]
	}
	at := strings.LastIndex(address, "@")
	if strings.Count(address, "@") != 1 || at <= 0 || at == len(address)-1 {
		return false
	}
	hostURI, err := url.Parse("https://" + address[at+1:])
	return err == nil && hostURI.Hostname() != "" && hostURI.User == nil &&
		(hostURI.Path == "" || hostURI.Path == "/")
}

func hasMalformedPercentEscape(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] != '%' {
			continue
		}
		if i+2 >= len(value) || !isHex(value[i+1]) || !isHex(value[i+2]) {
			return true
		}
		i += 2
	}
	return false
}

func isHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

func telephonyValidationProvider(provider string) string {
	if strings.TrimSpace(provider) == "acs" {
		return "azure-communication-service"
	}
	return strings.TrimSpace(provider)
}

func validateVoiceMaxOutputTokens(value any) error {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case string:
		if v == "inf" {
			return nil
		}
	case int:
		return validateVoiceMaxOutputTokensInt64(int64(v))
	case int8:
		return validateVoiceMaxOutputTokensInt64(int64(v))
	case int16:
		return validateVoiceMaxOutputTokensInt64(int64(v))
	case int32:
		return validateVoiceMaxOutputTokensInt64(int64(v))
	case int64:
		return validateVoiceMaxOutputTokensInt64(v)
	case uint:
		return validateVoiceMaxOutputTokensUint64(uint64(v))
	case uint8:
		return validateVoiceMaxOutputTokensUint64(uint64(v))
	case uint16:
		return validateVoiceMaxOutputTokensUint64(uint64(v))
	case uint32:
		return validateVoiceMaxOutputTokensUint64(uint64(v))
	case uint64:
		return validateVoiceMaxOutputTokensUint64(v)
	case float32:
		f := float64(v)
		if isFinite(f) && math.Trunc(f) == f {
			return validateVoiceMaxOutputTokensInt64(int64(f))
		}
	case float64:
		if isFinite(v) && math.Trunc(v) == v {
			return validateVoiceMaxOutputTokensInt64(int64(v))
		}
	}
	return fmt.Errorf("maxOutputTokens must be an integer from 1 to 2147483647 or the string inf")
}

func validateVoiceMaxOutputTokensInt64(value int64) error {
	if value < 1 || value > math.MaxInt32 {
		return fmt.Errorf("maxOutputTokens must be an integer from 1 to 2147483647 or the string inf")
	}
	return nil
}

func validateVoiceMaxOutputTokensUint64(value uint64) error {
	if value < 1 || value > math.MaxInt32 {
		return fmt.Errorf("maxOutputTokens must be an integer from 1 to 2147483647 or the string inf")
	}
	return nil
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
		"include item.input_audio_transcription.phrases requires " +
			"audio.input.transcription.model to be azure-speech or azure-fast-transcription",
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
	if format.Rate != nil {
		switch formatType {
		case "audio/pcm":
			if *format.Rate != 24000 {
				errors = append(errors, path+".rate must be 24000 for audio/pcm")
			}
		case "audio/pcmu", "audio/pcma":
			errors = append(errors, path+".rate must be omitted for G.711 audio/pcmu and audio/pcma")
		}
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
// The policies are decoded as raw maps rather than into [Policy] because project loading may
// validate either the raw camelCase property map or an internally YAML-marshaled Go value with
// snake_case tags. The latter is an implementation detail, not a public authoring alias.
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

// hasInvocationsModeration reports whether a raw policy map declares a moderation block.
// camelCase is the public spelling; snake_case may occur after internal YAML marshaling.
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
// It deliberately does not compile the selector expressions; malformed paths are still
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
		eventType := selector.EventType
		if strings.TrimSpace(eventType) == "" {
			errors = append(errors, fmt.Sprintf(
				"%s.streamSelectors[%d].eventType is required and must be non-empty", prefix, i))
		} else if eventType != strings.TrimSpace(eventType) {
			errors = append(errors, fmt.Sprintf(
				"%s.streamSelectors[%d].eventType must not have leading or trailing whitespace",
				prefix, i))
		}

		// textField names a field on the event payload; it is not a selector expression.
		// The mapper preserves this value exactly, so surrounding whitespace or a '$'-prefix
		// can make the literal lookup find no field and silently disable moderation.
		if selector.TextField == nil {
			continue
		}

		textField := *selector.TextField
		if textField == "" {
			errors = append(errors, fmt.Sprintf(
				"%s.streamSelectors[%d].textField must be non-empty when specified",
				prefix, i))
		} else if textField != strings.TrimSpace(textField) {
			errors = append(errors, fmt.Sprintf(
				"%s.streamSelectors[%d].textField must not have leading or trailing whitespace",
				prefix, i))
		} else if strings.HasPrefix(textField, "$") {
			errors = append(errors, fmt.Sprintf(
				"%s.streamSelectors[%d].textField must be a field name such as 'delta', "+
					"not a selector expression like '%s'",
				prefix, i, textField))
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
