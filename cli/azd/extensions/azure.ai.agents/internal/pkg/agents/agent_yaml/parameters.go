// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
)

// ParameterValues represents the user-provided values for manifest parameters
type ParameterValues map[string]any

// ProcessManifestParameters prompts the user for parameter values and injects them into the template
func ProcessManifestParameters(
	ctx context.Context,
	manifest *AgentManifest,
	azdClient *azdext.AzdClient,
	noPrompt bool) (*AgentManifest, error) {
	// If no parameters are defined, return the manifest as-is
	if len(manifest.Parameters.Properties) == 0 {
		log.Print("The manifest does not contain parameters that need to be configured.")
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
func promptForYamlParameterValues(
	ctx context.Context,
	parameters PropertySchema,
	azdClient *azdext.AzdClient,
	noPrompt bool) (ParameterValues, error) {
	paramValues := make(ParameterValues)

	for _, property := range parameters.Properties {
		fmt.Printf("Parameter: %s\n", property.Name)
		if property.Description != nil && *property.Description != "" {
			fmt.Printf("  Description: %s\n", *property.Description)
		}

		// Get default value
		var defaultValue any
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
		var value any
		var err error
		isRequired := property.Required != nil && *property.Required
		isSecret := property.Secret != nil && *property.Secret
		if len(enumValues) > 0 {
			// Use selection for enum parameters
			value, err = promptForEnumValue(ctx, property.Name, enumValues, defaultValue, azdClient, noPrompt)
		} else if isSecret && noPrompt {
			return nil, fmt.Errorf(
				"unable to prompt for secret parameter '%s' in no-prompt mode; "+
					"provide the value via environment variable",
				property.Name,
			)
		} else {
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
func InjectParameterValuesIntoManifest(
	manifest *AgentManifest, paramValues ParameterValues) (*AgentManifest, error) {
	// Convert manifest to YAML for processing
	manifestBytes, err := yaml.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal manifest: %w", err)
	}

	// Inject parameter values
	processedBytes, err := injectParameterValues(string(manifestBytes), paramValues)
	if err != nil {
		return nil, fmt.Errorf("failed to inject parameter values: %w", err)
	}

	// Convert back to AgentManifest
	processedManifest, err := LoadAndValidateAgentManifest(processedBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to reload processed manifest: %w", err)
	}

	return processedManifest, nil
}

// promptForEnumValue prompts the user to select from enumerated values
func promptForEnumValue(
	ctx context.Context,
	paramName string,
	enumValues []string,
	defaultValue any,
	azdClient *azdext.AzdClient,
	noPrompt bool) (any, error) {
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
func promptForTextValue(
	ctx context.Context,
	paramName string,
	defaultValue any,
	required bool,
	azdClient *azdext.AzdClient) (any, error) {
	var defaultStr string
	if defaultValue != nil {
		defaultStr = fmt.Sprintf("%v", defaultValue)
	}

	message := fmt.Sprintf("Enter value for parameter '%s'", paramName)
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
func injectParameterValues(template string, paramValues ParameterValues) ([]byte, error) {
	// Replace each parameter placeholder with its value
	for paramName, paramValue := range paramValues {
		placeholder := fmt.Sprintf("{{%s}}", paramName)
		valueStr := fmt.Sprintf("%v", paramValue)
		template = strings.ReplaceAll(template, placeholder, valueStr)

		placeholder = fmt.Sprintf("{{ %s }}", paramName)
		template = strings.ReplaceAll(template, placeholder, valueStr)
	}

	// Check for any remaining unreplaced placeholders
	if strings.Contains(template, "{{") && strings.Contains(template, "}}") {
		fmt.Println("Warning: Template contains unresolved placeholders.")
	}

	return []byte(template), nil
}
