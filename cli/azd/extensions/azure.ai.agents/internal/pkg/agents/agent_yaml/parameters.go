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
	result := manifest

	if len(manifest.Parameters.Properties) == 0 {
		// No declared parameters — nothing to prompt for. The warning at
		// the end still runs in case the manifest contains literal
		// `{{NAME}}` tokens that the author forgot to declare under
		// `parameters:` (typo / drift); we want to surface those.
		log.Print("The manifest does not contain parameters that need to be configured.")
	} else {
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
		result = processedManifest
	}

	// Surface a warning for any placeholders that survive substitution.
	//
	// This is the right call site for the warning because all declared
	// parameters have just been prompted-for and substituted, and
	// model-resource placeholders were already substituted earlier by
	// `ProcessModels` (init.go calls configureModelChoice before
	// ProcessManifestParameters). Any `{{NAME}}` still present here is
	// either a parameter the manifest author forgot to declare under
	// `parameters:` (typo / drift) or a literal `{{...}}` token the
	// author intends to ship as-is. Either case warrants the warning,
	// and the `Edit azure.yaml` advice is actionable from this point
	// onward (the caller will write the definition to azure.yaml).
	//
	// Earlier call sites of `InjectParameterValuesIntoManifest` (notably
	// `ProcessModels` in init_models.go which substitutes only model
	// deployment names) must NOT warn — at those points, user-configurable
	// placeholders are expected to still be present and are about to be
	// prompted-for here in `ProcessManifestParameters`.
	if err := warnUnresolvedManifestPlaceholders(result); err != nil {
		// Non-fatal: the manifest was either passed in by the caller
		// or just successfully re-loaded by InjectParameterValuesIntoManifest,
		// so a marshal failure here would be surprising. Log it and continue.
		log.Printf("failed to scan manifest for unresolved placeholders: %v", err)
	}

	return result, nil
}

// warnUnresolvedManifestPlaceholders re-marshals the template that will be
// written to azure.yaml and prints a stdout warning naming any surviving
// `{{NAME}}` placeholders. The nextstep guidance system uses the same
// `PlaceholderPattern` so the warning names and the post-init `Next:` block
// stay aligned (a placeholder reported in the warning must show up in the
// Next: block, and vice versa).
//
// Scans only `manifest.Template` because that's what gets written to
// azure.yaml; placeholders in other manifest sections (parameters,
// resources) never reach the on-disk file, so naming them in an
// "Edit azure.yaml" warning would mislead the user.
func warnUnresolvedManifestPlaceholders(manifest *AgentManifest) error {
	templateBytes, err := yaml.Marshal(manifest.Template)
	if err != nil {
		return fmt.Errorf("failed to marshal template for placeholder scan: %w", err)
	}

	remaining := ExtractUnresolvedPlaceholders(string(templateBytes))
	if len(remaining) == 0 {
		return nil
	}

	fmt.Printf(
		"Warning: azure.yaml has %d unresolved placeholder(s): %s. "+
			"Edit azure.yaml and replace each `{{NAME}}` with the actual value before deploying.\n",
		len(remaining),
		strings.Join(remaining, ", "),
	)
	return nil
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
			defaultIndex = int32(i) //nolint:gosec // enum value list length is bounded by manifest schema
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

// injectParameterValues replaces parameter placeholders in the template with
// actual values. Both compact (`{{NAME}}`) and spaced (`{{ NAME }}`) forms
// are substituted.
//
// This helper is intentionally silent about any placeholders that remain
// unresolved after substitution. It is called from two paths during init —
// `ProcessModels` (which substitutes only model-deployment-name parameters
// and intentionally leaves user-configurable placeholders for later) and
// `ProcessManifestParameters` (which substitutes user parameters and is
// the final substitution step before the manifest is written to disk).
// Emitting a "you have unresolved placeholders" warning here would
// false-positive from the `ProcessModels` path with names that are about
// to be prompted-for in `ProcessManifestParameters`. The warning therefore
// lives in `warnUnresolvedManifestPlaceholders`, which is called only from
// `ProcessManifestParameters` after both substitution steps complete.
func injectParameterValues(template string, paramValues ParameterValues) ([]byte, error) {
	for paramName, paramValue := range paramValues {
		placeholder := fmt.Sprintf("{{%s}}", paramName)
		valueStr := fmt.Sprintf("%v", paramValue)
		template = strings.ReplaceAll(template, placeholder, valueStr)

		placeholder = fmt.Sprintf("{{ %s }}", paramName)
		template = strings.ReplaceAll(template, placeholder, valueStr)
	}

	return []byte(template), nil
}
