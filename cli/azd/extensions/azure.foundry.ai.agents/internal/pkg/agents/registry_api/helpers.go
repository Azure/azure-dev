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

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ParameterValues represents the user-provided values for manifest parameters
type ParameterValues map[string]interface{}

func ProcessRegistryManifest(ctx context.Context, manifest *Manifest, azdClient *azdext.AzdClient) (json.RawMessage, error) {
	processedTemplate, err := ProcessManifestWithParameters(ctx, manifest, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to process manifest: %w", err)
	}

	processedTemplate, err = transformModelField(processedTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to transform model field: %w", err)
	}

	return processedTemplate, nil
}

// ProcessManifestWithParameters prompts the user for parameter values and injects them into the template
func ProcessManifestWithParameters(ctx context.Context, manifest *Manifest, azdClient *azdext.AzdClient) (json.RawMessage, error) {
	// If no parameters are defined, return the template as-is
	if len(manifest.Parameters) == 0 {
		return manifest.Template, nil
	}

	fmt.Println("The manifest contains parameters that need to be configured:")
	fmt.Println()

	// Collect parameter values from user
	paramValues, err := promptForParameterValues(ctx, manifest.Parameters, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to collect parameter values: %w", err)
	}

	// Inject parameter values into the template
	processedTemplate, err := injectParameterValues(manifest.Template, paramValues)
	if err != nil {
		return nil, fmt.Errorf("failed to inject parameter values into template: %w", err)
	}

	return json.RawMessage(processedTemplate), nil
}

// promptForParameterValues prompts the user for values for each parameter
func promptForParameterValues(ctx context.Context, parameters map[string]OpenApiParameter, azdClient *azdext.AzdClient) (ParameterValues, error) {
	paramValues := make(ParameterValues)

	for paramName, param := range parameters {
		fmt.Printf("Parameter: %s\n", paramName)
		if param.Description != "" {
			fmt.Printf("  Description: %s\n", param.Description)
		}

		// Get default value from schema if available
		defaultValue, err := getDefaultValueFromSchema(param.Schema)
		if err != nil {
			fmt.Printf("  Warning: Could not parse schema for parameter %s: %v\n", paramName, err)
		}

		// Get enum values if available
		enumValues, err := getEnumValuesFromSchema(param.Schema)
		if err != nil {
			fmt.Printf("  Warning: Could not parse enum values for parameter %s: %v\n", paramName, err)
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
		if len(enumValues) > 0 {
			// Use selection for enum parameters
			value, err = promptForEnumValue(ctx, paramName, enumValues, defaultValue, azdClient)
		} else {
			// Use text input for other parameters
			value, err = promptForTextValue(ctx, paramName, defaultValue, param.Required, azdClient)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to get value for parameter %s: %w", paramName, err)
		}

		paramValues[paramName] = value
	}

	return paramValues, nil
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

// getDefaultValueFromSchema extracts the default value from an OpenAPI schema
func getDefaultValueFromSchema(schema *OpenApiSchema) (interface{}, error) {
	if schema == nil {
		return nil, nil
	}

	return schema.Default, nil
}

// getEnumValuesFromSchema extracts enum values from an OpenAPI schema
func getEnumValuesFromSchema(schema *OpenApiSchema) ([]string, error) {
	if schema == nil || len(schema.Enum) == 0 {
		return nil, nil
	}

	enumValues := make([]string, len(schema.Enum))
	for i, val := range schema.Enum {
		enumValues[i] = fmt.Sprintf("%v", val)
	}

	return enumValues, nil
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

// transformModelField converts a string "model" field to an object with "id" field
func transformModelField(template json.RawMessage) (json.RawMessage, error) {
	// Parse the template JSON
	var templateData map[string]interface{}
	if err := json.Unmarshal(template, &templateData); err != nil {
		return nil, fmt.Errorf("failed to parse template JSON: %w", err)
	}

	// Check if the model field exists and is a string
	if modelValue, exists := templateData["model"]; exists {
		if modelStr, isString := modelValue.(string); isString {
			// Transform string to object with "id" field
			templateData["model"] = map[string]interface{}{
				"id": modelStr,
			}
		}
	}

	// Convert back to JSON
	transformedJSON, err := json.Marshal(templateData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal transformed template: %w", err)
	}

	return json.RawMessage(transformedJSON), nil
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
