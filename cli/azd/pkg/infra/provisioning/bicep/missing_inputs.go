// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
)

// InputConstraints captures the numeric/length constraints from an ARM parameter definition.
type InputConstraints struct {
	MinLength *int `json:"minLength,omitempty"`
	MaxLength *int `json:"maxLength,omitempty"`
	MinValue  *int `json:"minValue,omitempty"`
	MaxValue  *int `json:"maxValue,omitempty"`
}

// MissingInput represents a missing required input for infrastructure provisioning.
type MissingInput struct {
	Name          string           `json:"name"`
	Type          string           `json:"type"`
	Secure        bool             `json:"secure"`
	Description   string           `json:"description"`
	EnvVarNames   []string         `json:"envVarNames"`
	ConfigKey     string           `json:"configKey"`
	AllowedValues []string         `json:"allowedValues"`
	Constraints   InputConstraints `json:"constraints"`
}

// MissingInputsError is an error that contains information about all missing required inputs.
type MissingInputsError struct {
	Inputs []MissingInput
}

// Error implements the error interface for MissingInputsError.
func (e *MissingInputsError) Error() string {
	return "missing required inputs"
}

// ToString returns a formatted message with the missing inputs and resolution guidance.
func (e *MissingInputsError) ToString(currentIndentation string) string {
	var buf strings.Builder
	separator := "──────────────────────────────────────────────────────────────"

	buf.WriteString(separator + "\n")
	buf.WriteString("Provision cannot continue (interactive prompts disabled)\n")
	buf.WriteString(separator + "\n\n")

	count := len(e.Inputs)
	if count == 1 {
		buf.WriteString("1 required input is missing.\n")
	} else {
		buf.WriteString(fmt.Sprintf("%d required inputs are missing.\n", count))
	}

	buf.WriteString("\nMissing required inputs:\n\n")

	for _, input := range e.Inputs {
		buf.WriteString(fmt.Sprintf("• %s\n", input.Name))

		if len(input.EnvVarNames) > 0 {
			buf.WriteString(fmt.Sprintf("    Environment variable: %s\n", strings.Join(input.EnvVarNames, ", ")))
		}

		buf.WriteString(fmt.Sprintf("    Environment configuration key: %s\n", input.ConfigKey))

		if input.Type != "" {
			buf.WriteString(fmt.Sprintf("    Type: %s\n", input.Type))
		}

		details := constraintDetails(input)
		if len(details) > 0 {
			buf.WriteString("    Constraints:\n")
			for _, detail := range details {
				buf.WriteString(fmt.Sprintf("        %s\n", detail))
			}
		}

		if input.Description != "" {
			buf.WriteString(fmt.Sprintf("    Description: %s\n", input.Description))
		}

		buf.WriteString("\n")
	}

	buf.WriteString(separator + "\n\n")
	buf.WriteString("You can resolve these by:\n\n")

	if e.hasEnvVars() {
		buf.WriteString("1) Setting environment variables\n")
		buf.WriteString("     azd env set <ENV_VAR_NAME> <value>\n\n")
	}

	buf.WriteString("2) Setting environment configuration\n")
	buf.WriteString("     azd env config set infra.parameters.<paramName> <value>\n\n")

	buf.WriteString("Then re-run:\n")
	buf.WriteString("     azd provision\n")

	return buf.String()
}

// constraintDetails returns human-readable constraint lines for text output.
func constraintDetails(input MissingInput) []string {
	var details []string

	if len(input.AllowedValues) > 0 {
		details = append(details, fmt.Sprintf("Allowed values: %s", strings.Join(input.AllowedValues, ", ")))
	}

	c := input.Constraints
	if c.MinLength != nil && c.MaxLength != nil {
		details = append(details, fmt.Sprintf("Length: %d–%d", *c.MinLength, *c.MaxLength))
	} else if c.MinLength != nil {
		details = append(details, fmt.Sprintf("Min length: %d", *c.MinLength))
	} else if c.MaxLength != nil {
		details = append(details, fmt.Sprintf("Max length: %d", *c.MaxLength))
	}

	if c.MinValue != nil && c.MaxValue != nil {
		details = append(details, fmt.Sprintf("Value: %d–%d", *c.MinValue, *c.MaxValue))
	} else if c.MinValue != nil {
		details = append(details, fmt.Sprintf("Min value: %d", *c.MinValue))
	} else if c.MaxValue != nil {
		details = append(details, fmt.Sprintf("Max value: %d", *c.MaxValue))
	}

	if input.Secure {
		details = append(details, "Secure: true")
	}

	return details
}

// hasEnvVars returns true if at least one input has environment variable mappings.
func (e *MissingInputsError) hasEnvVars() bool {
	for _, input := range e.Inputs {
		if len(input.EnvVarNames) > 0 {
			return true
		}
	}
	return false
}

// MarshalJSON implements json.Marshaler for MissingInputsError.
func (e *MissingInputsError) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Error   string         `json:"error"`
		Message string         `json:"message"`
		Inputs  []MissingInput `json:"inputs"`
	}{
		Error:   e.Error(),
		Message: "Provision cannot continue (interactive prompts disabled)",
		Inputs:  e.Inputs,
	})
}

// buildMissingInputsError creates a MissingInputsError from parameter prompts and environment mapping.
func (p *BicepProvider) buildMissingInputsError(
	parameterPrompts []struct {
		key   string
		param azure.ArmTemplateParameterDefinition
	},
	envMapping map[string][]string,
) *MissingInputsError {
	var inputs []MissingInput

	for _, prompt := range parameterPrompts {
		param := prompt.param

		// Normalize type for display (securestring → string, secureobject → object)
		displayType := param.Type
		if strings.EqualFold(displayType, "securestring") {
			displayType = "string"
		} else if strings.EqualFold(displayType, "secureobject") {
			displayType = "object"
		}

		// Get description if available
		description := ""
		if desc, ok := param.Description(); ok {
			description = desc
		}

		// Get allowed values if specified
		var allowedValues []string
		if param.AllowedValues != nil {
			for _, val := range *param.AllowedValues {
				allowedValues = append(allowedValues, fmt.Sprintf("%v", val))
			}
		}

		input := MissingInput{
			Name:          prompt.key,
			Type:          displayType,
			Secure:        param.Secure(),
			Description:   description,
			EnvVarNames:   envMapping[prompt.key],
			ConfigKey:     fmt.Sprintf("infra.parameters.%s", prompt.key),
			AllowedValues: allowedValues,
			Constraints: InputConstraints{
				MinLength: param.MinLength,
				MaxLength: param.MaxLength,
				MinValue:  param.MinValue,
				MaxValue:  param.MaxValue,
			},
		}

		inputs = append(inputs, input)
	}

	return &MissingInputsError{
		Inputs: inputs,
	}
}
