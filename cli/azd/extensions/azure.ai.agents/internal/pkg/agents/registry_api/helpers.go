// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package registry_api

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ParameterValues represents the user-provided values for manifest parameters
type ParameterValues map[string]interface{}

func ProcessRegistryManifest(ctx context.Context, manifest *Manifest, azdClient *azdext.AzdClient) (*agent_yaml.AgentManifest, error) {
	// Convert the agent API definition into a MAML definition
	promptAgent, err := ConvertAgentDefinition(manifest.Template)
	if err != nil {
		return nil, fmt.Errorf("failed to convert agentDefinition: %w", err)
	}

	// Inject Agent API Manifest properties into MAML Agent properties as needed
	updatedAgentDef := MergeManifestIntoAgentDefinition(manifest, &promptAgent.AgentDefinition)
	promptAgent.AgentDefinition = *updatedAgentDef

	// Convert the agent API parameters into MAML parameters
	parameters, err := ConvertParameters(manifest.Parameters)
	if err != nil {
		return nil, fmt.Errorf("failed to convert parameters: %w", err)
	}

	// Create the AgentManifest with the converted PromptAgent
	result := &agent_yaml.AgentManifest{
		Name:        manifest.Name,
		DisplayName: manifest.DisplayName,
		Description: &manifest.Description,
		Template:    *promptAgent,
		Parameters:  *parameters,
	}

	return result, nil
}

func ConvertAgentDefinition(template agent_api.PromptAgentDefinition) (*agent_yaml.PromptAgent, error) {
	// Convert tools from agent_api.Tool to agent_yaml.Tool
	var tools []any
	for _, apiTool := range template.Tools {
		yamlTool, err := ConvertToolToYaml(apiTool)
		if err != nil {
			return nil, fmt.Errorf("failed to convert tool: %w", err)
		}
		tools = append(tools, yamlTool)
	}

	// Create the PromptAgent
	promptAgent := &agent_yaml.PromptAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind:        agent_yaml.AgentKindPrompt, // Set to prompt kind
			Name:        "",                         // Will be set later from manifest or user input
			Description: nil,                        // Will be set later from manifest or user input
			// Metadata:     make(map[string]interface{}), // TODO, Where does this come from?
		},
		Model: agent_yaml.Model{
			Id: template.Model,
		},
		Instructions: template.Instructions,
		Tools:        &tools,
	}

	return promptAgent, nil
}

// ConvertToolToYaml converts an agent_api tool to its corresponding agent_yaml tool type
func ConvertToolToYaml(apiTool any) (any, error) {
	if apiTool == nil {
		return nil, fmt.Errorf("tool cannot be nil")
	}

	switch tool := apiTool.(type) {
	case agent_api.FunctionTool:
		return agent_yaml.FunctionTool{
			Tool: agent_yaml.Tool{
				Name:        tool.Name,
				Kind:        agent_yaml.ToolKindFunction,
				Description: tool.Description,
			},
			Parameters: convertToPropertySchema(tool.Parameters),
			Strict:     tool.Strict,
		}, nil
	case agent_api.WebSearchPreviewTool:
		options := make(map[string]interface{})
		if tool.UserLocation != nil {
			options["userLocation"] = tool.UserLocation
		}
		if tool.SearchContextSize != nil {
			options["searchContextSize"] = *tool.SearchContextSize
		}
		return agent_yaml.WebSearchTool{
			Tool: agent_yaml.Tool{
				Name: "web_search_preview",
				Kind: agent_yaml.ToolKindWebSearch,
			},
			Options: options,
		}, nil
	case agent_api.BingGroundingAgentTool:
		options := make(map[string]interface{})
		options["bingGrounding"] = tool.BingGrounding
		return agent_yaml.BingGroundingTool{
			Tool: agent_yaml.Tool{
				Name: "bing_grounding",
				Kind: agent_yaml.ToolKindBingGrounding,
			},
			Options: options,
		}, nil
	case agent_api.FileSearchTool:
		options := make(map[string]interface{})
		if tool.Filters != nil {
			options["filters"] = tool.Filters
		}
		var ranker *string
		var scoreThreshold *float64
		if tool.RankingOptions != nil {
			ranker = tool.RankingOptions.Ranker
			scoreThreshold = convertFloat32ToFloat64(tool.RankingOptions.ScoreThreshold)
		}
		return agent_yaml.FileSearchTool{
			Tool: agent_yaml.Tool{
				Name: "file_search",
				Kind: agent_yaml.ToolKindFileSearch,
			},
			VectorStoreIds:     tool.VectorStoreIds,
			MaximumResultCount: convertInt32ToInt(tool.MaxNumResults),
			Ranker:             ranker,
			ScoreThreshold:     scoreThreshold,
			Options:            options,
		}, nil
	case agent_api.MCPTool:
		options := make(map[string]interface{})
		if tool.ServerURL != "" {
			options["serverUrl"] = tool.ServerURL
		}
		if tool.Headers != nil {
			options["headers"] = tool.Headers
		}
		if tool.AllowedTools != nil {
			options["allowedTools"] = tool.AllowedTools
		}
		if tool.RequireApproval != nil {
			options["requireApproval"] = tool.RequireApproval
		}
		if tool.ProjectConnectionID != nil {
			options["projectConnectionId"] = *tool.ProjectConnectionID
		}
		return agent_yaml.McpTool{
			Tool: agent_yaml.Tool{
				Name: "mcp",
				Kind: agent_yaml.ToolKindMcp,
			},
			ServerName:        tool.ServerLabel,
			ServerDescription: nil, // Not available in agent_api
			ApprovalMode:      agent_yaml.McpServerApprovalMode{},
			AllowedTools:      nil, // Will be set through options as the types are too generic
			Options:           options,
		}, nil
	case agent_api.OpenApiAgentTool:
		options := make(map[string]interface{})
		options["openapi"] = tool.OpenAPI
		return agent_yaml.OpenApiTool{
			Tool: agent_yaml.Tool{
				Name: "openapi",
				Kind: agent_yaml.ToolKindOpenApi,
			},
			Specification: "", // Placeholder - should be extracted from tool.OpenAPI
			Options:       options,
		}, nil
	case agent_api.CodeInterpreterTool:
		options := make(map[string]interface{})
		if tool.Container != nil {
			options["container"] = tool.Container
		}
		return agent_yaml.CodeInterpreterTool{
			Tool: agent_yaml.Tool{
				Name: "code_interpreter",
				Kind: agent_yaml.ToolKindCodeInterpreter,
			},
			Options: options,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported tool type: %T", apiTool)
	}
}

// Helper function to convert PropertySchema from interface{} (assuming it's already a PropertySchema or can be converted)
func convertToPropertySchema(params interface{}) agent_yaml.PropertySchema {
	// This is a placeholder implementation - you may need to adjust based on the actual structure
	// of the parameters from the agent_api package
	return agent_yaml.PropertySchema{
		Properties: []agent_yaml.Property{},
	}
}

// Helper function to convert *float32 to *float64
func convertFloat32ToFloat64(f32 *float32) *float64 {
	if f32 == nil {
		return nil
	}
	f64 := float64(*f32)
	return &f64
}

// Helper function to convert *int32 to *int
func convertInt32ToInt(i32 *int32) *int {
	if i32 == nil {
		return nil
	}
	i := int(*i32)
	return &i
}

func ConvertParameters(parameters map[string]OpenApiParameter) (*agent_yaml.PropertySchema, error) {
	if len(parameters) == 0 {
		return nil, nil
	}

	var properties []agent_yaml.Property

	for paramName, openApiParam := range parameters {
		// Create a basic Property from the OpenApiParameter
		property := agent_yaml.Property{
			Name:        paramName,
			Description: &openApiParam.Description,
			Required:    &openApiParam.Required,
		}

		// Determine the kind based on schema type
		if openApiParam.Schema != nil {
			property.Kind = openApiParam.Schema.Type
		}

		// Fallback to string kind if no kind specified
		if property.Kind == "" {
			property.Kind = "string"
		}

		// Use example as default if available
		if openApiParam.Example != nil {
			property.Default = &openApiParam.Example
		}

		// Convert enum values if present
		if openApiParam.Schema != nil && len(openApiParam.Schema.Enum) > 0 {
			enumValues := make([]interface{}, len(openApiParam.Schema.Enum))
			copy(enumValues, openApiParam.Schema.Enum)
			property.EnumValues = &enumValues
		}

		properties = append(properties, property)
	}

	return &agent_yaml.PropertySchema{
		Properties: properties,
	}, nil
}

// ProcessManifestParameters prompts the user for parameter values and injects them into the template
func ProcessManifestParameters(ctx context.Context, manifest *agent_yaml.AgentManifest, azdClient *azdext.AzdClient, noPrompt bool) (*agent_yaml.AgentManifest, error) {
	// If no parameters are defined, return the manifest as-is
	if len(manifest.Parameters.Properties) == 0 {
		fmt.Println("The manifest does not contain parameters that need to be configured.")
		return manifest, nil
	}

	fmt.Println("The manifest contains parameters that need to be configured:")
	fmt.Println()

	// Collect parameter values from user
	paramValues, err := promptForYamlParameterValues(ctx, manifest.Parameters, azdClient, noPrompt)
	if err != nil {
		return nil, fmt.Errorf("failed to collect parameter values: %w", err)
	}

	// Inject parameter values into the manifest
	processedManifest, err := InjectParameterValuesIntoManifest(manifest, paramValues)
	if err != nil {
		return nil, fmt.Errorf("failed to inject parameter values into manifest: %w", err)
	}

	return processedManifest, nil
}

// promptForYamlParameterValues prompts the user for values for each YAML parameter
func promptForYamlParameterValues(ctx context.Context, parameters agent_yaml.PropertySchema, azdClient *azdext.AzdClient, noPrompt bool) (ParameterValues, error) {
	paramValues := make(ParameterValues)

	for _, property := range parameters.Properties {
		fmt.Printf("Parameter: %s\n", property.Name)
		if property.Description != nil && *property.Description != "" {
			fmt.Printf("  Description: %s\n", *property.Description)
		}

		// Get default value
		var defaultValue interface{}
		if property.Default != nil {
			defaultValue = *property.Default
		}

		// Get enum values if available
		var enumValues []string
		if property.EnumValues != nil && len(*property.EnumValues) > 0 {
			enumValues = make([]string, len(*property.EnumValues))
			for i, val := range *property.EnumValues {
				enumValues[i] = fmt.Sprintf("%v", val)
			}
		}

		// Show available options if it's an enum
		if len(enumValues) > 0 {
			fmt.Printf("  Available values: %v\n", enumValues)
		}

		// Show default value if available
		if defaultValue != nil {
			fmt.Printf("  Default: %v\n", defaultValue)
		}

		fmt.Println()

		// Prompt for value
		var value interface{}
		var err error
		isRequired := property.Required != nil && *property.Required
		if len(enumValues) > 0 {
			// Use selection for enum parameters
			value, err = promptForEnumValue(ctx, property.Name, enumValues, defaultValue, azdClient, noPrompt)
		} else {
			// Use text input for other parameters
			value, err = promptForTextValue(ctx, property.Name, defaultValue, isRequired, azdClient)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to get value for parameter %s: %w", property.Name, err)
		}

		paramValues[property.Name] = value
	}

	return paramValues, nil
}

// InjectParameterValuesIntoManifest replaces parameter placeholders in the manifest with actual values
func InjectParameterValuesIntoManifest(manifest *agent_yaml.AgentManifest, paramValues ParameterValues) (*agent_yaml.AgentManifest, error) {
	// Convert manifest to JSON for processing
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal manifest: %w", err)
	}

	// Inject parameter values
	processedBytes, err := injectParameterValues(json.RawMessage(manifestBytes), paramValues)
	if err != nil {
		return nil, fmt.Errorf("failed to inject parameter values: %w", err)
	}

	// Convert back to AgentManifest
	processedManifest, err := agent_yaml.LoadAndValidateAgentManifest(processedBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to reload processed manifest: %w", err)
	}

	return processedManifest, nil
}

// promptForEnumValue prompts the user to select from enumerated values
func promptForEnumValue(ctx context.Context, paramName string, enumValues []string, defaultValue interface{}, azdClient *azdext.AzdClient, noPrompt bool) (interface{}, error) {
	// Convert default value to string for comparison
	var defaultStr string
	if defaultValue != nil {
		defaultStr = fmt.Sprintf("%v", defaultValue)

		if noPrompt {
			fmt.Printf("No prompt mode enabled, selecting default for parameter '%s': %s\n", paramName, defaultStr)
			return defaultStr, nil
		}
	}

	// Create choices for the select prompt
	choices := make([]*azdext.SelectChoice, len(enumValues))
	defaultIndex := int32(0)
	for i, val := range enumValues {
		choices[i] = &azdext.SelectChoice{
			Value: val,
			Label: val,
		}
		if val == defaultStr {
			defaultIndex = int32(i)
		}
	}

	resp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       fmt.Sprintf("Select value for parameter '%s':", paramName),
			Choices:       choices,
			SelectedIndex: &defaultIndex,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for enum value: %w", err)
	}

	// Return the selected value
	if resp.Value != nil && int(*resp.Value) < len(enumValues) {
		return enumValues[*resp.Value], nil
	}

	return enumValues[0], nil // fallback to first option
}

// promptForTextValue prompts the user for a text value
func promptForTextValue(ctx context.Context, paramName string, defaultValue interface{}, required bool, azdClient *azdext.AzdClient) (interface{}, error) {
	var defaultStr string
	if defaultValue != nil {
		defaultStr = fmt.Sprintf("%v", defaultValue)
	}

	message := fmt.Sprintf("Enter value for parameter '%s':", paramName)
	if defaultStr != "" {
		message += fmt.Sprintf(" (default: %s)", defaultStr)
	}

	resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        message,
			IgnoreHintKeys: true,
			DefaultValue:   defaultStr,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for text value: %w", err)
	}

	// Use default value if user provided empty input
	if strings.TrimSpace(resp.Value) == "" {
		if defaultValue != nil {
			return defaultValue, nil
		}
		if required {
			return nil, fmt.Errorf("parameter '%s' is required but no value was provided", paramName)
		}
	}

	return resp.Value, nil
}

// injectParameterValues replaces parameter placeholders in the template with actual values
func injectParameterValues(template json.RawMessage, paramValues ParameterValues) ([]byte, error) {
	// Convert template to string for processing
	templateStr := string(template)

	// Replace each parameter placeholder with its value
	for paramName, paramValue := range paramValues {
		placeholder := fmt.Sprintf("{{%s}}", paramName)
		valueStr := fmt.Sprintf("%v", paramValue)
		templateStr = strings.ReplaceAll(templateStr, placeholder, valueStr)

		placeholder = fmt.Sprintf("{{ %s }}", paramName)
		templateStr = strings.ReplaceAll(templateStr, placeholder, valueStr)
	}

	// Check for any remaining unreplaced placeholders
	if strings.Contains(templateStr, "{{") && strings.Contains(templateStr, "}}") {
		fmt.Println("Warning: Template contains unresolved placeholders.")
	} else {
		fmt.Println("No remaining placeholders found.")
	}

	return []byte(templateStr), nil
}

// ValidateParameterValue validates a parameter value against its schema
func ValidateParameterValue(value interface{}, schema *OpenApiSchema) error {
	if schema == nil {
		return nil
	}

	// Validate type if specified
	if schema.Type != "" {
		if err := validateType(value, schema.Type); err != nil {
			return err
		}
	}

	// Validate enum if specified
	if len(schema.Enum) > 0 {
		if err := validateEnum(value, schema.Enum); err != nil {
			return err
		}
	}

	// Additional validations can be added here (min/max length, patterns, etc.)

	return nil
}

// validateType validates that a value matches the expected type
func validateType(value interface{}, expectedType string) error {
	switch expectedType {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string, got %T", value)
		}
	case "integer":
		switch v := value.(type) {
		case int, int32, int64:
			// Valid integer types
		case string:
			// Try to parse string as integer
			if _, err := strconv.Atoi(v); err != nil {
				return fmt.Errorf("expected integer, got string that cannot be parsed as integer: %s", v)
			}
		default:
			return fmt.Errorf("expected integer, got %T", value)
		}
	case "number":
		switch v := value.(type) {
		case int, int32, int64, float32, float64:
			// Valid numeric types
		case string:
			// Try to parse string as number
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				return fmt.Errorf("expected number, got string that cannot be parsed as number: %s", v)
			}
		default:
			return fmt.Errorf("expected number, got %T", value)
		}
	case "boolean":
		switch v := value.(type) {
		case bool:
			// Valid boolean
		case string:
			// Try to parse string as boolean
			if _, err := strconv.ParseBool(v); err != nil {
				return fmt.Errorf("expected boolean, got string that cannot be parsed as boolean: %s", v)
			}
		default:
			return fmt.Errorf("expected boolean, got %T", value)
		}
	}

	return nil
}

// validateEnum validates that a value is one of the allowed enum values
func validateEnum(value interface{}, enumValues []interface{}) error {
	valueStr := fmt.Sprintf("%v", value)

	for _, enumVal := range enumValues {
		enumStr := fmt.Sprintf("%v", enumVal)
		if valueStr == enumStr {
			return nil
		}
	}

	return fmt.Errorf("value '%v' is not one of the allowed values: %v", value, enumValues)
}

// MergeManifestIntoAgentDefinition takes a Manifest and an AgentDefinition and updates
// the AgentDefinition with values from the Manifest for properties that are empty/zero.
// Returns the updated AgentDefinition.
func MergeManifestIntoAgentDefinition(manifest *Manifest, agentDef *agent_yaml.AgentDefinition) *agent_yaml.AgentDefinition {
	// Create a copy of the agent definition to avoid modifying the original
	result := *agentDef

	// Use reflection to iterate through AgentDefinition fields
	resultValue := reflect.ValueOf(&result).Elem()
	resultType := resultValue.Type()

	// Get manifest properties as a map using reflection
	manifestValue := reflect.ValueOf(manifest).Elem()
	manifestType := manifestValue.Type()

	// Create a map of manifest field names to values for easier lookup
	manifestFields := make(map[string]reflect.Value)
	for i := 0; i < manifestValue.NumField(); i++ {
		field := manifestType.Field(i)
		fieldValue := manifestValue.Field(i)

		// Use the json tag name if available, otherwise use the field name
		jsonTag := field.Tag.Get("json")
		fieldName := field.Name
		if jsonTag != "" && jsonTag != "-" {
			// Remove omitempty and other options from json tag
			parts := strings.Split(jsonTag, ",")
			if parts[0] != "" {
				fieldName = parts[0]
			}
		}
		manifestFields[strings.ToLower(fieldName)] = fieldValue
	}

	// Iterate through AgentDefinition fields
	for i := 0; i < resultValue.NumField(); i++ {
		field := resultType.Field(i)
		fieldValue := resultValue.Field(i)

		// Skip unexported fields
		if !fieldValue.CanSet() {
			continue
		}

		// Get the field name to match with manifest
		jsonTag := field.Tag.Get("json")
		fieldName := field.Name
		if jsonTag != "" && jsonTag != "-" {
			// Remove omitempty and other options from json tag
			parts := strings.Split(jsonTag, ",")
			if parts[0] != "" {
				fieldName = parts[0]
			}
		}

		// Check if this field exists in the manifest and if the agent definition field is empty
		if manifestFieldValue, exists := manifestFields[strings.ToLower(fieldName)]; exists {
			if isEmptyValue(fieldValue) && !isEmptyValue(manifestFieldValue) {
				// Only set if types are compatible
				if manifestFieldValue.Type().AssignableTo(fieldValue.Type()) {
					fieldValue.Set(manifestFieldValue)
				} else {
					// Handle type conversion for common cases
					if fieldValue.Kind() == reflect.String && manifestFieldValue.Kind() == reflect.String {
						fieldValue.SetString(manifestFieldValue.String())
					}
				}
			}
		}
	}

	return &result
}

// isEmptyValue checks if a reflect.Value represents an empty/zero value
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.String() == ""
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return v.IsNil()
	case reflect.Array:
		return v.Len() == 0
	case reflect.Struct:
		// For structs, check if all fields are empty
		for i := 0; i < v.NumField(); i++ {
			if !isEmptyValue(v.Field(i)) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
