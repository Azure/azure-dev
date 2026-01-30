// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"context"
	"fmt"
	"regexp"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
)

// LoadAndValidateAgentManifest parses YAML content and validates it as an AgentManifest
// Returns the parsed manifest and any validation errors
func LoadAndValidateAgentManifest(manifestYamlContent []byte) (*AgentManifest, error) {
	var manifest AgentManifest
	if err := yaml.Unmarshal(manifestYamlContent, &manifest); err != nil {
		return nil, fmt.Errorf("YAML content does not conform to AgentManifest format: %w", err)
	}

	agentDef, err := ExtractAgentDefinition(manifestYamlContent)
	if err != nil {
		return nil, fmt.Errorf("YAML content does not conform to AgentManifest format: %w", err)
	}
	manifest.Template = agentDef

	resourceDefs, err := ExtractResourceDefinitions(manifestYamlContent)
	if err != nil {
		return nil, fmt.Errorf("YAML content does not conform to AgentManifest format: %w", err)
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
	var genericManifest map[string]interface{}
	if err := yaml.Unmarshal(manifestYamlContent, &genericManifest); err != nil {
		return nil, fmt.Errorf("YAML content is not valid: %w", err)
	}

	template := genericManifest["template"].(map[string]interface{})
	templateBytes, _ := yaml.Marshal(template)

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
		if metadata, ok := genericManifest["metadata"].(map[string]interface{}); ok {
			agentDef.Metadata = &metadata
		}
	}

	switch agentDef.Kind {
	case AgentKindPrompt:
		return nil, fmt.Errorf("prompt agents not currently supported")

		// var agent PromptAgent
		// if err := yaml.Unmarshal(templateBytes, &agent); err != nil {
		// 	return nil, fmt.Errorf("failed to unmarshal to PromptAgent: %w", err)
		// }

		// agent.AgentDefinition = agentDef

		// tools, err := ExtractToolsDefinitions(template)
		// if err != nil {
		// 	return nil, fmt.Errorf("failed to extract tools definitions: %w", err)
		// }

		// agent.Tools = &tools
		// return agent, nil
	case AgentKindHosted:
		var agent ContainerAgent
		if err := yaml.Unmarshal(templateBytes, &agent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal to ContainerAgent: %w", err)
		}

		agent.AgentDefinition = agentDef
		return agent, nil
	}

	return nil, fmt.Errorf("unrecognized agent kind: %s", agentDef.Kind)
}

// Returns a specific resource type based on the "kind" field in the resource
func ExtractResourceDefinitions(manifestYamlContent []byte) ([]any, error) {
	var genericManifest map[string]interface{}
	if err := yaml.Unmarshal(manifestYamlContent, &genericManifest); err != nil {
		return nil, fmt.Errorf("YAML content is not valid: %w", err)
	}

	resourcesValue, exists := genericManifest["resources"]
	if !exists || resourcesValue == nil {
		return []any{}, nil // Return empty slice if no resources key
	}

	resources, ok := resourcesValue.([]interface{})
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
		default:
			return nil, fmt.Errorf("unrecognized resource kind: %s", resourceDef.Kind)
		}
	}

	return resourceDefs, nil
}

func ExtractToolsDefinitions(template map[string]interface{}) ([]any, error) {
	var tools []any

	toolsValue, exists := template["tools"]
	if exists && toolsValue != nil {
		toolsArray, ok := toolsValue.([]interface{})
		if !ok {
			return nil, fmt.Errorf("tools field is not a valid array")
		}

		for _, tool := range toolsArray {
			toolBytes, _ := yaml.Marshal(tool)

			var toolDef Tool
			if err := yaml.Unmarshal(toolBytes, &toolDef); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to Tool: %w", err)
			}

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

			switch AgentKind(agentDef.Kind) {
			case AgentKindPrompt:
				var agent PromptAgent
				if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
					if agent.Model.Id == "" {
						errors = append(errors, "template.model.id is required")
					}
				} else {
					errors = append(errors, fmt.Sprintf("failed to unmarshal to PromptAgent: %v", err))
				}
			case AgentKindHosted:
				var agent ContainerAgent
				if err := yaml.Unmarshal(templateBytes, &agent); err == nil {
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
			}
		}
	}

	if len(errors) > 0 {
		errorMsg := "validation failed:"
		for _, err := range errors {
			errorMsg += fmt.Sprintf("\n  - %s", err)
		}
		return fmt.Errorf("%s", errorMsg)
	}

	return nil
}

// Validate that the agent name matches the expected deployable format
func ValidateAgentName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}

	// Regex pattern: ^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$
	// - Must start with alphanumeric character
	// - Can contain alphanumeric characters and hyphens
	// - Must end with alphanumeric character (if more than 1 character)
	// - Maximum length of 63 characters
	validNamePattern := regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

	if !validNamePattern.MatchString(name) {
		return fmt.Errorf("name must start and end with an alphanumeric character, can contain hyphens in the middle, and be 1-63 characters long")
	}

	return nil
}

func ProcessPromptAgentToolsConnections(ctx context.Context, manifest *AgentManifest, azdClient *azdext.AzdClient) (*AgentManifest, error) {
	agentDef, ok := manifest.Template.(PromptAgent)
	if !ok {
		return nil, fmt.Errorf("agent template is not a PromptAgent")
	}

	if agentDef.Tools != nil {
		var tools []any
		for _, tool := range *agentDef.Tools {
			toolBytes, _ := yaml.Marshal(tool)

			var toolDef Tool
			if err := yaml.Unmarshal(toolBytes, &toolDef); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to Tool: %w", err)
			}

			switch toolDef.Kind {
			case ToolKindCustom:
				tool := tool.(CustomTool)

				if tool.Connection == nil {
					var connectionName string

					// Check if ProjectConnectionID is provided in options
					if tool.Options != nil {
						if projectConnID, exists := tool.Options["projectConnectionId"]; exists {
							if connIDStr, ok := projectConnID.(string); ok {
								connectionName = connIDStr
							}
						}
					}

					// If no ProjectConnectionID found in options, prompt the user
					if connectionName == "" {
						promptResult, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
							Options: &azdext.PromptOptions{
								Message:        fmt.Sprintf("Connection for tool %s not provided. Please enter connection name", tool.Name),
								IgnoreHintKeys: true,
								Required:       true,
								DefaultValue:   string(tool.Tool.Kind),
							},
						})
						if err != nil {
							return nil, fmt.Errorf("failed to prompt for text value: %w", err)
						}
						connectionName = promptResult.Value
					}

					// Create a ReferenceConnection using the connectionName
					refConnection := ReferenceConnection{
						Connection: Connection{
							Kind: ConnectionKindReference,
						},
						Name: connectionName,
					}
					tool.Connection = refConnection
				}

				tools = append(tools, tool)
			case ToolKindBingGrounding:
				tool := tool.(BingGroundingTool)

				if tool.Connection == nil {
					var connectionName string

					// Check if ProjectConnectionID is provided in options
					if tool.Options != nil {
						if projectConnID, exists := tool.Options["projectConnectionId"]; exists {
							if connIDStr, ok := projectConnID.(string); ok {
								connectionName = connIDStr
							}
						}
					}

					// If no ProjectConnectionID found in options, prompt the user
					if connectionName == "" {
						promptResult, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
							Options: &azdext.PromptOptions{
								Message:        fmt.Sprintf("Connection for tool %s not provided. Please enter connection name", tool.Name),
								IgnoreHintKeys: true,
								Required:       true,
								DefaultValue:   string(tool.Tool.Kind),
							},
						})
						if err != nil {
							return nil, fmt.Errorf("failed to prompt for text value: %w", err)
						}
						connectionName = promptResult.Value
					}

					// Create a ReferenceConnection using the connectionName
					refConnection := ReferenceConnection{
						Connection: Connection{
							Kind: ConnectionKindReference,
						},
						Name: connectionName,
					}
					tool.Connection = refConnection
				}

				tools = append(tools, tool)
			case ToolKindFileSearch:
				tool := tool.(FileSearchTool)

				if tool.Connection == nil {
					var connectionName string

					// Check if ProjectConnectionID is provided in options
					if tool.Options != nil {
						if projectConnID, exists := tool.Options["projectConnectionId"]; exists {
							if connIDStr, ok := projectConnID.(string); ok {
								connectionName = connIDStr
							}
						}
					}

					// If no ProjectConnectionID found in options, prompt the user
					if connectionName == "" {
						promptResult, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
							Options: &azdext.PromptOptions{
								Message:        fmt.Sprintf("Connection for tool %s not provided. Please enter connection name", tool.Name),
								IgnoreHintKeys: true,
								Required:       true,
								DefaultValue:   string(tool.Tool.Kind),
							},
						})
						if err != nil {
							return nil, fmt.Errorf("failed to prompt for text value: %w", err)
						}
						connectionName = promptResult.Value
					}

					// Create a ReferenceConnection using the connectionName
					refConnection := ReferenceConnection{
						Connection: Connection{
							Kind: ConnectionKindReference,
						},
						Name: connectionName,
					}
					tool.Connection = refConnection
				}

				tools = append(tools, tool)
			case ToolKindMcp:
				tool := tool.(McpTool)

				if tool.Connection == nil {
					var connectionName string

					// Check if ProjectConnectionID is provided in options
					if tool.Options != nil {
						if projectConnID, exists := tool.Options["projectConnectionId"]; exists {
							if connIDStr, ok := projectConnID.(string); ok {
								connectionName = connIDStr
							}
						}
					}

					// If no ProjectConnectionID found in options, prompt the user
					if connectionName == "" {
						promptResult, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
							Options: &azdext.PromptOptions{
								Message:        fmt.Sprintf("Connection for tool %s not provided. Please enter connection name", tool.Name),
								IgnoreHintKeys: true,
								Required:       true,
								DefaultValue:   string(tool.Tool.Kind),
							},
						})
						if err != nil {
							return nil, fmt.Errorf("failed to prompt for text value: %w", err)
						}
						connectionName = promptResult.Value
					}

					// Create a ReferenceConnection using the connectionName
					refConnection := ReferenceConnection{
						Connection: Connection{
							Kind: ConnectionKindReference,
						},
						Name: connectionName,
					}
					tool.Connection = refConnection
				}

				tools = append(tools, tool)
			case ToolKindOpenApi:
				tool := tool.(OpenApiTool)

				if tool.Connection == nil {
					var connectionName string

					// Check if ProjectConnectionID is provided in options
					if tool.Options != nil {
						if projectConnID, exists := tool.Options["projectConnectionId"]; exists {
							if connIDStr, ok := projectConnID.(string); ok {
								connectionName = connIDStr
							}
						}
					}

					// If no ProjectConnectionID found in options, prompt the user
					if connectionName == "" {
						promptResult, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
							Options: &azdext.PromptOptions{
								Message:        fmt.Sprintf("Connection for tool %s not provided. Please enter connection name", tool.Name),
								IgnoreHintKeys: true,
								Required:       true,
								DefaultValue:   string(tool.Tool.Kind),
							},
						})
						if err != nil {
							return nil, fmt.Errorf("failed to prompt for text value: %w", err)
						}
						connectionName = promptResult.Value
					}

					// Create a ReferenceConnection using the connectionName
					refConnection := ReferenceConnection{
						Connection: Connection{
							Kind: ConnectionKindReference,
						},
						Name: connectionName,
					}
					tool.Connection = refConnection
				}

				tools = append(tools, tool)
			default:
				tools = append(tools, tool)
			}
		}

		agentDef.Tools = &tools
		manifest.Template = agentDef
	}

	return manifest, nil
}
