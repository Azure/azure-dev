// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agents

import (
	"fmt"
	"os"
	"strings"

	"github.com/braydonk/yaml"
)

// AgentKind represents the type of agent
type AgentKind string

const (
	AgentKindPrompt       AgentKind = "prompt"
	AgentKindHosted       AgentKind = "hosted"
	AgentKindContainerApp AgentKind = "container_app"
	AgentKindWorkflow     AgentKind = "workflow"
)

// RaiConfig represents the configuration for Responsible AI (RAI) content filtering and safety features
type RaiConfig struct {
	RaiPolicyName string `json:"rai_policy_name" yaml:"rai_policy_name"`
}

// ProtocolVersionRecord represents a protocol and its version
type ProtocolVersionRecord struct {
	Protocol string `json:"protocol" yaml:"protocol"`
	Version  string `json:"version"  yaml:"version"`
}

// AgentYAMLConfig represents the structure of the agent YAML configuration file
type AgentYAMLConfig struct {
	ID                        string                  `yaml:"id"`
	Version                   string                  `yaml:"version"`
	Name                      string                  `yaml:"name"`
	Kind                      string                  `yaml:"kind"`
	Description               string                  `yaml:"description"`
	Model                     string                  `yaml:"model"`
	Instructions              string                  `yaml:"instructions"`
	ContainerProtocolVersions []ProtocolVersionRecord `yaml:"container_protocol_versions"`
	CPU                       string                  `yaml:"cpu"`
	Memory                    string                  `yaml:"memory"`
	EnvironmentVariables      map[string]string       `yaml:"environment_variables"`
	Metadata                  map[string]interface{}  `yaml:"metadata"`
}

// AgentDefinition represents the base agent definition
type AgentDefinition struct {
	Kind      AgentKind  `json:"kind"                 yaml:"kind"`
	RaiConfig *RaiConfig `json:"rai_config,omitempty" yaml:"rai_config,omitempty"`
}

// PromptAgentDefinition represents the prompt agent definition
type PromptAgentDefinition struct {
	Kind         AgentKind  `json:"kind"                   yaml:"kind"`
	ModelName    string     `json:"model"                  yaml:"model"`
	Instructions *string    `json:"instructions,omitempty" yaml:"instructions,omitempty"`
	Temperature  *float64   `json:"temperature,omitempty"  yaml:"temperature,omitempty"`
	TopP         *float64   `json:"top_p,omitempty"        yaml:"top_p,omitempty"`
	RaiConfig    *RaiConfig `json:"rai_config,omitempty"   yaml:"rai_config,omitempty"`
}

// HostedAgentDefinition represents the hosted agent definition
type HostedAgentDefinition struct {
	Kind                      AgentKind               `json:"kind"                            yaml:"kind"`
	Image                     string                  `json:"image"                           yaml:"image"`
	ContainerProtocolVersions []ProtocolVersionRecord `json:"container_protocol_versions"     yaml:"container_protocol_versions"`
	CPU                       string                  `json:"cpu"                             yaml:"cpu"`
	Memory                    string                  `json:"memory"                          yaml:"memory"`
	// nolint:lll
	EnvironmentVariables map[string]string `json:"environment_variables,omitempty" yaml:"environment_variables,omitempty"`
	RaiConfig            *RaiConfig        `json:"rai_config,omitempty"            yaml:"rai_config,omitempty"`
}

// CreateAgentVersionRequest represents the request model for creating an agent version
type CreateAgentVersionRequest struct {
	Description *string                `json:"description,omitempty" yaml:"description,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"    yaml:"metadata,omitempty"`
	Definition  interface{}            `json:"definition"            yaml:"definition"`
}

// CreateAgentRequest represents the request model for creating an agent
type CreateAgentRequest struct {
	CreateAgentVersionRequest
	Name string `json:"name" yaml:"name"`
}

// ParseAgentYAML parses the agent YAML file and returns the configuration
func ParseAgentYAML(yamlFilePath string) (*AgentYAMLConfig, error) {
	// Read the YAML file
	data, err := os.ReadFile(yamlFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML file: %w", err)
	}

	// Parse YAML
	var agentConfig AgentYAMLConfig
	if err := yaml.Unmarshal(data, &agentConfig); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Validate required fields
	if agentConfig.ID == "" {
		return nil, fmt.Errorf("agent ID is required in YAML file")
	}

	return &agentConfig, nil
}

// CreateAgentRequest creates a CreateAgentRequest from agent YAML file and image URL
func CreateAgentRequestFromYAML(yamlFilePath string, imageURL string) (*CreateAgentRequest, error) {
	// Read the YAML file
	data, err := os.ReadFile(yamlFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML file: %w", err)
	}

	// Parse YAML
	var agentConfig AgentYAMLConfig
	if err := yaml.Unmarshal(data, &agentConfig); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Prepare metadata
	metadata := make(map[string]interface{})
	if agentConfig.Metadata != nil {
		// Handle authors specially - convert slice to comma-separated string
		if authors, exists := agentConfig.Metadata["authors"]; exists {
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
	}

	// Determine agent kind (default to prompt if not specified)
	agentKind := strings.ToLower(agentConfig.Kind)
	if agentKind == "" {
		agentKind = "prompt"
	}

	// Create agent definition based on kind
	var definition interface{}
	switch agentKind {
	case "prompt":
		promptDef := PromptAgentDefinition{
			Kind:      AgentKindPrompt,
			ModelName: agentConfig.Model,
		}
		if agentConfig.Instructions != "" {
			promptDef.Instructions = &agentConfig.Instructions
		}
		definition = promptDef

	case "hosted":
		// Use the provided image URL directly - it's mandatory for hosted agents
		imageName := imageURL
		if imageName == "" {
			return nil, fmt.Errorf("--image-url is required for hosted agents")
		}

		// Set default protocol versions if not specified
		protocolVersions := agentConfig.ContainerProtocolVersions
		if len(protocolVersions) == 0 {
			protocolVersions = []ProtocolVersionRecord{
				{Protocol: "responses", Version: "v1"},
			}
		}

		// Set default CPU and memory if not specified
		cpu := agentConfig.CPU
		if cpu == "" {
			cpu = "1"
		}
		memory := agentConfig.Memory
		if memory == "" {
			memory = "2Gi"
		}

		hostedDef := HostedAgentDefinition{
			Kind:                      AgentKindHosted,
			Image:                     imageName,
			ContainerProtocolVersions: protocolVersions,
			CPU:                       cpu,
			Memory:                    memory,
			EnvironmentVariables:      agentConfig.EnvironmentVariables,
		}
		definition = hostedDef

	default:
		return nil, fmt.Errorf("unsupported agent kind: %s. Supported kinds are: prompt, hosted", agentKind)
	}

	// Determine agent name (prefer name, fallback to id)
	agentName := agentConfig.Name
	if agentName == "" {
		agentName = agentConfig.ID
	}
	if agentName == "" {
		agentName = "default-agent"
	}

	// Create the agent request
	request := &CreateAgentRequest{
		Name: agentName,
		CreateAgentVersionRequest: CreateAgentVersionRequest{
			Definition: definition,
		},
	}

	if agentConfig.Description != "" {
		request.Description = &agentConfig.Description
	}

	if len(metadata) > 0 {
		request.Metadata = metadata
	}

	return request, nil
}
