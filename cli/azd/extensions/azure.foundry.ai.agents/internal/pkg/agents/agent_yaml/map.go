// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_api"
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

// BuildAgentDefinitionFromManifest constructs an AgentDefinition from the given AgentManifest
// with optional build-time configuration. It returns both the agent definition and build config.
func BuildAgentDefinitionFromManifest(agentManifest AgentManifest, options ...AgentBuildOption) (AgentDefinition, *AgentBuildConfig, error) {
	// Apply options
	config := &AgentBuildConfig{}
	for _, option := range options {
		option(config)
	}
	
	// Return the agent definition and build config separately
	// The build config will be used later when creating the API request
	return agentManifest.Agent, config, nil
}

// CreateAgentAPIRequestFromManifest creates a CreateAgentRequest from AgentManifest with strong typing
func CreateAgentAPIRequestFromManifest(agentManifest AgentManifest, options ...AgentBuildOption) (*agent_api.CreateAgentRequest, error) {
	agentDef, buildConfig, err := BuildAgentDefinitionFromManifest(agentManifest, options...)
	if err != nil {
		return nil, err
	}

	// Route to appropriate handler based on agent kind
	switch agentDef.Kind {
	case AgentKindPrompt:
		return CreatePromptAgentAPIRequest(agentDef, buildConfig)
	case AgentKindHosted:
		return CreateHostedAgentAPIRequest(agentDef, buildConfig)
	default:
		return nil, fmt.Errorf("unsupported agent kind: %s. Supported kinds are: prompt, hosted", agentDef.Kind)
	}
}

// CreatePromptAgentAPIRequest creates a CreateAgentRequest for prompt-based agents
func CreatePromptAgentAPIRequest(agentDefinition AgentDefinition, buildConfig *AgentBuildConfig) (*agent_api.CreateAgentRequest, error) {
	// TODO QUESTION: Should I expect a PromptAgent type instead of AgentDefinition?
	// The AgentDefinition has all the fields but PromptAgent might have additional prompt-specific fields
	
	promptDef := agent_api.PromptAgentDefinition{
		AgentDefinition: agent_api.AgentDefinition{
        	Kind: agent_api.AgentKindPrompt, // This sets Kind to "prompt"
    	},
		Model: agentDefinition.Model.Id, // TODO QUESTION: Is Model.Id the right field to use?
		Instructions: &agentDefinition.Instructions,
		
		// TODO QUESTION: How should I map Model.Options to these fields?
		// The agent_yaml.Model has ModelOptions with a Kind field, but how do I get:
		// - Temperature (float32) - from Model.Options or somewhere else?
		// - TopP (float32) - from Model.Options or somewhere else?
		// 
		// Example: if agentDefinition.Model.Options has structured data:
		// Temperature: extractFloat32FromOptions(agentDefinition.Model.Options, "temperature"),
		// TopP: extractFloat32FromOptions(agentDefinition.Model.Options, "top_p"),
		
		// TODO QUESTION: How should I map Tools from agent_yaml to agent_api?
		// agent_yaml.Tool vs agent_api.Tool - are they compatible or do I need conversion?
		// Tools: convertYamlToolsToApiTools(agentDefinition.Tools),
		
		// TODO QUESTION: What about these advanced fields?
		// - Reasoning (*agent_api.Reasoning) - where does this come from in YAML?
		// - Text (*agent_api.ResponseTextFormatConfiguration) - related to output format?
		// - StructuredInputs (map[string]agent_api.StructuredInputDefinition) - from InputSchema?
		// 
		// Possible mappings:
		// Text: mapOutputSchemaToTextFormat(agentDefinition.OutputSchema),
		// StructuredInputs: mapInputSchemaToStructuredInputs(agentDefinition.InputSchema),
	}

	return createAgentAPIRequest(agentDefinition, promptDef)
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

// mapInputSchemaToStructuredInputs converts InputSchema to StructuredInputs
func mapInputSchemaToStructuredInputs(inputSchema InputSchema) map[string]agent_api.StructuredInputDefinition {
	// TODO QUESTION: How does InputSchema map to StructuredInputDefinition?
	// InputSchema might have parameters that become structured inputs
	return nil // Placeholder
}

// mapOutputSchemaToTextFormat converts OutputSchema to text response format
func mapOutputSchemaToTextFormat(outputSchema OutputSchema) *agent_api.ResponseTextFormatConfiguration {
	// TODO QUESTION: How does OutputSchema influence text formatting?
	// OutputSchema might specify response structure that affects text config
	return nil // Placeholder
}

// CreateHostedAgentAPIRequest creates a CreateAgentRequest for hosted agents
func CreateHostedAgentAPIRequest(agentDefinition AgentDefinition, buildConfig *AgentBuildConfig) (*agent_api.CreateAgentRequest, error) {
	// TODO QUESTION: Should I expect a ContainerAgent type instead of AgentDefinition?
	// ContainerAgent has additional fields like Protocol and Options that might be relevant
	
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
		return nil, fmt.Errorf("image URL is required for hosted agents - use WithImageURL build option")
	}

	// TODO QUESTION: Should protocol versions come from YAML definition or be configurable via build options?
	// ContainerAgent.Protocol might specify this, or should it be in build config?
	
	// Set default protocol versions
	protocolVersions := []agent_api.ProtocolVersionRecord{
		{Protocol: agent_api.AgentProtocolResponses, Version: "v1"},
	}

	hostedDef := agent_api.HostedAgentDefinition{
		AgentDefinition: agent_api.AgentDefinition{
        	Kind: agent_api.AgentKindHosted, // This sets Kind to "hosted"
    	},
		ContainerProtocolVersions: protocolVersions,
		CPU:                       cpu,
		Memory:                    memory,
		EnvironmentVariables:      envVars,
	}
	
	// Set the image from build configuration
	imageHostedDef := agent_api.ImageBasedHostedAgentDefinition{
		HostedAgentDefinition: hostedDef,
		Image:                 imageURL,
	}

	return createAgentAPIRequest(agentDefinition, imageHostedDef)
}

// createAgentAPIRequest is a helper function to create the final request with common fields
func createAgentAPIRequest(agentDefinition AgentDefinition, agentDef interface{}) (*agent_api.CreateAgentRequest, error) {
	// Prepare metadata
	metadata := make(map[string]string)
	if agentDefinition.Metadata != nil {
		// Handle authors specially - convert slice to comma-separated string
		if authors, exists := agentDefinition.Metadata["authors"]; exists {
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
		for key, value := range agentDefinition.Metadata {
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

	if agentDefinition.Description != "" {
		request.Description = &agentDefinition.Description
	}

	if len(metadata) > 0 {
		request.Metadata = metadata
	}

	return request, nil
}

// Legacy function for backward compatibility - delegates to the new structured approach
func CreateAgentAPIRequestFromAgentDefinition(agentDefinition AgentDefinition, buildConfig *AgentBuildConfig) (*agent_api.CreateAgentRequest, error) {
	// Route to appropriate handler based on agent kind
	switch agentDefinition.Kind {
	case AgentKindPrompt:
		return CreatePromptAgentAPIRequest(agentDefinition, buildConfig)
	case AgentKindHosted:
		return CreateHostedAgentAPIRequest(agentDefinition, buildConfig)
	default:
		return nil, fmt.Errorf("unsupported agent kind: %s. Supported kinds are: prompt, hosted", agentDefinition.Kind)
	}
}