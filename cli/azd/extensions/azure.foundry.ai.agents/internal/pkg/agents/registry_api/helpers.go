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
	agentDef, err := ConvertAgentDefinition(manifest.Template)
	if err != nil {
		return nil, fmt.Errorf("failed to convert agentDefinition: %w", err)
	}

	// Inject Agent API Manifest properties into MAML Agent properties as needed
	agentDef = MergeManifestIntoAgentDefinition(manifest, agentDef)

	// Convert the agent API parameters into MAML parameters
	parameters, err := ConvertParameters(manifest.Parameters)
	if err != nil {
		return nil, fmt.Errorf("failed to convert parameters: %w", err)
	}

	// Create the AgentManifest with the converted AgentDefinition
	result := &agent_yaml.AgentManifest{
		Agent:      *agentDef,
		Parameters: parameters,
	}

	return result, nil
}

func ConvertAgentDefinition(template agent_api.PromptAgentDefinition) (*agent_yaml.AgentDefinition, error) {
	// Convert the model string to Model struct
	model := agent_yaml.Model{
		Id: template.Model,
	}

	// Convert tools from agent_api.Tool to agent_yaml.Tool
	var tools []agent_yaml.Tool
	for _, apiTool := range template.Tools {
		yamlTool := agent_yaml.Tool{
			Name: apiTool.Type, // Use Type as Name
			Kind: "",           // TODO: Where does this come from?
		}
		tools = append(tools, yamlTool)
	}

	// Get instructions, defaulting to empty string if nil
	instructions := ""
	if template.Instructions != nil {
		instructions = *template.Instructions
	}

	// Create the AgentDefinition
	agentDef := &agent_yaml.AgentDefinition{
		Kind:         agent_yaml.AgentKindPrompt, // Set to prompt kind
		Name:         "",                         // Will be set later from manifest or user input
		Description:  "",                         // Will be set later from manifest or user input
		Instructions: instructions,
		Model:        model,
		Tools:        tools,
		// Metadata:     make(map[string]interface{}), // TODO, Where does this come from?
	}

	return agentDef, nil
}

func ConvertParameters(parameters map[string]OpenApiParameter) ([]agent_yaml.Parameter, error) {
	if len(parameters) == 0 {
		return []agent_yaml.Parameter{}, nil
	}

	result := make([]agent_yaml.Parameter, 0, len(parameters))

	for paramName, openApiParam := range parameters {
		// Create a basic Parameter from the OpenApiParameter
		param := agent_yaml.Parameter{
			Name:        paramName,
			Description: openApiParam.Description,
			Required:    openApiParam.Required,
		}

		// Extract type/kind from schema if available
		if openApiParam.Schema != nil {
			param.Kind = openApiParam.Schema.Type
			param.Default = openApiParam.Schema.Default

			// Convert enum values if present
			if len(openApiParam.Schema.Enum) > 0 {
				param.Enum = openApiParam.Schema.Enum
			}
		}

		// Use example as default if no schema default is provided
		if param.Default == nil && openApiParam.Example != nil {
			param.Default = openApiParam.Example
		}

		// Fallback to string type if no type specified
		if param.Kind == "" {
			param.Kind = "string"
		}

		result = append(result, param)
	}

	return result, nil
}

// ProcessManifestParameters prompts the user for parameter values and injects them into the template
func ProcessManifestParameters(ctx context.Context, manifest *agent_yaml.AgentManifest, azdClient *azdext.AzdClient) (*agent_yaml.AgentManifest, error) {
	// If no parameters are defined, return the manifest as-is
	if len(manifest.Parameters) == 0 {
		fmt.Println("The manifest does not contain parameters that need to be configured.")
		return manifest, nil
	}

	fmt.Println("The manifest contains parameters that need to be configured:")
	fmt.Println()

	// Collect parameter values from user
	paramValues, err := promptForYamlParameterValues(ctx, manifest.Parameters, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to collect parameter values: %w", err)
	}

	// Inject parameter values into the manifest
	processedManifest, err := injectParameterValuesIntoManifest(manifest, paramValues)
	if err != nil {
		return nil, fmt.Errorf("failed to inject parameter values into manifest: %w", err)
	}

	return processedManifest, nil
}

// promptForYamlParameterValues prompts the user for values for each YAML parameter
func promptForYamlParameterValues(ctx context.Context, parameters []agent_yaml.Parameter, azdClient *azdext.AzdClient) (ParameterValues, error) {
	paramValues := make(ParameterValues)

	for _, param := range parameters {
		fmt.Printf("Parameter: %s\n", param.Name)
		if param.Description != "" {
			fmt.Printf("  Description: %s\n", param.Description)
		}

		// Get default value
		defaultValue := param.Default

		// Get enum values if available
		var enumValues []string
		if len(param.Enum) > 0 {
			enumValues = make([]string, len(param.Enum))
			for i, val := range param.Enum {
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
		if len(enumValues) > 0 {
			// Use selection for enum parameters
			value, err = promptForEnumValue(ctx, param.Name, enumValues, defaultValue, azdClient)
		} else {
			// Use text input for other parameters
			value, err = promptForTextValue(ctx, param.Name, defaultValue, param.Required, azdClient)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to get value for parameter %s: %w", param.Name, err)
		}

		paramValues[param.Name] = value
	}

	return paramValues, nil
}

// injectParameterValuesIntoManifest replaces parameter placeholders in the manifest with actual values
func injectParameterValuesIntoManifest(manifest *agent_yaml.AgentManifest, paramValues ParameterValues) (*agent_yaml.AgentManifest, error) {
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
	var processedManifest agent_yaml.AgentManifest
	if err := json.Unmarshal(processedBytes, &processedManifest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal processed manifest: %w", err)
	}

	return &processedManifest, nil
}

// promptForEnumValue prompts the user to select from enumerated values
func promptForEnumValue(ctx context.Context, paramName string, enumValues []string, defaultValue interface{}, azdClient *azdext.AzdClient) (interface{}, error) {
	// Convert default value to string for comparison
	var defaultStr string
	if defaultValue != nil {
		defaultStr = fmt.Sprintf("%v", defaultValue)
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
	}

	// Check for any remaining unreplaced placeholders
	if strings.Contains(templateStr, "{{") && strings.Contains(templateStr, "}}") {
		fmt.Printf("Warning: Template contains unresolved placeholders:\n%s\n", templateStr)
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
