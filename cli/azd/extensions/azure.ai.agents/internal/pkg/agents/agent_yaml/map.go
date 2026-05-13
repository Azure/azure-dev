// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"maps"
	"math"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_api"

	"go.yaml.in/yaml/v3"
)

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
	default:
		return nil, fmt.Errorf("unsupported agent kind: %s. Supported kinds are: hosted", agentDef.Kind)
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
			{Protocol: agent_api.AgentProtocolResponses, Version: "1.0.0"},
		}
	}

	// Code deploy path
	if hostedAgent.CodeConfiguration != nil {
		entryPoint := []string{"python", hostedAgent.CodeConfiguration.EntryPoint}
		depRes := ""
		if hostedAgent.CodeConfiguration.DependencyResolution != nil {
			depRes = *hostedAgent.CodeConfiguration.DependencyResolution
		}

		codeDef := agent_api.HostedAgentDefinition{
			AgentDefinition: agent_api.AgentDefinition{
				Kind: agent_api.AgentKindHosted,
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
			Kind: agent_api.AgentKindHosted,
		},
		ProtocolVersions:     protocolVersions,
		CPU:                  cpu,
		Memory:               memory,
		EnvironmentVariables: envVars,
		Image:                imageURL,
	}

	return createAgentAPIRequest(hostedAgent.AgentDefinition, imageDef,
		hostedAgent.AgentEndpoint, hostedAgent.AgentCard)
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
		protocols := make(
			[]agent_api.AgentProtocol, 0, len(agentEndpoint.Protocols),
		)
		for _, p := range agentEndpoint.Protocols {
			trimmed := strings.TrimSpace(p)
			if trimmed == "" {
				return nil, fmt.Errorf(
					"agentEndpoint contains an empty protocol value",
				)
			}
			protocols = append(protocols, agent_api.AgentProtocol(trimmed))
		}
		request.AgentEndpoint = &agent_api.AgentEndpoint{
			Protocols: protocols,
		}
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
