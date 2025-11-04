// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_api"

	"go.yaml.in/yaml/v3"
)

/*
MAPPING QUESTIONS TO RESOLVE:

1. TYPE ARCHITECTURE:
   - Should we work with specific types (PromptAgent, ContainerAgent) instead of base AgentDefinition?
   - ContainerAgent has Protocol and Options fields that might be relevant for hosted agents

2. MODEL MAPPING (for prompt agents):
   - How to extract Temperature, TopP from Model.Options?
   - Model.Options.Kind seems to be required - what values are expected?
   - Are agent_yaml.Tool and agent_api.Tool compatible or need conversion?

3. ADVANCED PROMPT FEATURES:
   - How to map InputSchema to StructuredInputs?
   - How to map OutputSchema to Text response format?
   - Where does Reasoning configuration come from?

4. HOSTED AGENT CONFIGURATION:
   - Should CPU/Memory/EnvironmentVariables come from YAML (ContainerAgent.Options) or only build config?
   - Should Protocol versions be configurable or always default to "responses/v1"?
   - How to handle ContainerAgent.Protocol field if present?

5. BUILD VS YAML PRECEDENCE:
   - When both YAML and build config specify the same thing, which takes precedence?
   - Should build config override YAML values or complement them?
*/

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
		for k, v := range envVars {
			config.EnvironmentVariables[k] = v
		}
	}
}

func constructBuildConfig(options ...AgentBuildOption) *AgentBuildConfig {
	config := &AgentBuildConfig{}
	for _, option := range options {
		option(config)
	}
	return config
}

// CreateAgentAPIRequestFromManifest creates a CreateAgentRequest from AgentManifest with strong typing
func CreateAgentAPIRequestFromManifest(agentManifest AgentManifest, options ...AgentBuildOption) (*agent_api.CreateAgentRequest, error) {
	buildConfig := constructBuildConfig(options...)

	templateBytes, _ := yaml.Marshal(agentManifest.Template)

	var agentDef AgentDefinition
	if err := yaml.Unmarshal(templateBytes, &agentDef); err != nil {
		return nil, fmt.Errorf("failed to parse template to determine agent kind while creating api request")
	}

	// Route to appropriate handler based on agent kind
	switch agentDef.Kind {
	case AgentKindPrompt:
		promptDef := agentManifest.Template.(PromptAgent)
		return CreatePromptAgentAPIRequest(promptDef, buildConfig)
	case AgentKindHosted:
		hostedDef := agentManifest.Template.(ContainerAgent)
		return CreateHostedAgentAPIRequest(hostedDef, buildConfig)
	default:
		return nil, fmt.Errorf("unsupported agent kind: %s. Supported kinds are: prompt, hosted", agentDef.Kind)
	}
}

// CreatePromptAgentAPIRequest creates a CreateAgentRequest for prompt-based agents
func CreatePromptAgentAPIRequest(promptAgent PromptAgent, buildConfig *AgentBuildConfig) (*agent_api.CreateAgentRequest, error) {
	// Extract model information from the prompt agent
	var modelId string
	var instructions *string
	var temperature *float32
	var topP *float32

	// Get model ID
	if promptAgent.Model.Id != "" {
		modelId = promptAgent.Model.Id
	} else {
		return nil, fmt.Errorf("model.id is required for prompt agents")
	}

	// Get instructions
	if promptAgent.Instructions != nil {
		instructions = promptAgent.Instructions
	}

	// Extract temperature and topP from model options if available
	if promptAgent.Model.Options != nil {
		if promptAgent.Model.Options.Temperature != nil {
			tempFloat32 := float32(*promptAgent.Model.Options.Temperature)
			temperature = &tempFloat32
		}
		if promptAgent.Model.Options.TopP != nil {
			tpFloat32 := float32(*promptAgent.Model.Options.TopP)
			topP = &tpFloat32
		}
	}

	promptDef := agent_api.PromptAgentDefinition{
		AgentDefinition: agent_api.AgentDefinition{
			Kind: agent_api.AgentKindPrompt,
		},
		Model:        modelId,
		Instructions: instructions,
		Temperature:  temperature,
		TopP:         topP,

		// TODO: Handle additional fields like Tools, Reasoning, etc.
		// Tools: convertYamlToolsToApiTools(promptAgent.Tools),
		// Text: mapOutputSchemaToTextFormat(promptAgent.OutputSchema),
		// StructuredInputs: mapInputSchemaToStructuredInputs(promptAgent.InputSchema),
	}

	return createAgentAPIRequest(promptAgent.AgentDefinition, promptDef)
}

// Helper functions for type conversion (TODO: Implement based on answers to questions above)

// extractFloat32FromOptions extracts a float32 value from ModelOptions
func extractFloat32FromOptions(options ModelOptions, key string) *float32 {
	// TODO QUESTION: How is ModelOptions structured? Is it a map or typed struct?
	// If it's map[string]interface{}: check options[key] and convert to float32
	// If it's typed struct: access specific fields
	return nil // Placeholder
}

// convertYamlToolsToApiTools converts agent_yaml tools to agent_api tools
func convertYamlToolsToApiTools(yamlTools []Tool) []agent_api.Tool {
	// TODO QUESTION: Are the Tool types compatible or do they need field mapping?
	// Compare agent_yaml.Tool vs agent_api.Tool structures
	return nil // Placeholder
}

// mapInputSchemaToStructuredInputs converts PropertySchema to StructuredInputs
func mapInputSchemaToStructuredInputs(inputSchema *PropertySchema) map[string]agent_api.StructuredInputDefinition {
	// TODO QUESTION: How does PropertySchema map to StructuredInputDefinition?
	// PropertySchema might have parameters that become structured inputs
	return nil // Placeholder
}

// mapOutputSchemaToTextFormat converts PropertySchema to text response format
func mapOutputSchemaToTextFormat(outputSchema *PropertySchema) *agent_api.ResponseTextFormatConfiguration {
	// TODO QUESTION: How does PropertySchema influence text formatting?
	// PropertySchema might specify response structure that affects text config
	return nil // Placeholder
}

// CreateHostedAgentAPIRequest creates a CreateAgentRequest for hosted agents
func CreateHostedAgentAPIRequest(hostedAgent ContainerAgent, buildConfig *AgentBuildConfig) (*agent_api.CreateAgentRequest, error) {
	// Check if we have an image URL set via the build config
	imageURL := ""
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

	if imageURL == "" {
		return nil, fmt.Errorf("image URL is required for hosted agents - use WithImageURL build option or specify in container.image")
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
			{Protocol: agent_api.AgentProtocolResponses, Version: "v1"},
		}
	}

	hostedDef := agent_api.HostedAgentDefinition{
		AgentDefinition: agent_api.AgentDefinition{
			Kind: agent_api.AgentKindHosted,
		},
		ContainerProtocolVersions: protocolVersions,
		CPU:                       cpu,
		Memory:                    memory,
		EnvironmentVariables:      envVars,
	}

	// Set the image from build configuration or container definition
	imageHostedDef := agent_api.ImageBasedHostedAgentDefinition{
		HostedAgentDefinition: hostedDef,
		Image:                 imageURL,
	}

	return createAgentAPIRequest(hostedAgent.AgentDefinition, imageHostedDef)
}

// createAgentAPIRequest is a helper function to create the final request with common fields
func createAgentAPIRequest(agentDefinition AgentDefinition, agentDef interface{}) (*agent_api.CreateAgentRequest, error) {
	// Prepare metadata
	metadata := make(map[string]string)
	if agentDefinition.Metadata != nil {
		// Handle authors specially - convert slice to comma-separated string
		if authors, exists := (*agentDefinition.Metadata)["authors"]; exists {
			if authorsSlice, ok := authors.([]interface{}); ok {
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

	return request, nil
}
