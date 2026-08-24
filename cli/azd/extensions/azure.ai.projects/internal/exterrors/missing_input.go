// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

// MissingInputSource describes one supported source for a required input.
type MissingInputSource struct {
	Kind         input.InputSourceKind
	Name         string
	ExampleValue string
	Example      string
}

// MissingInput describes a required input and every supported source.
type MissingInput struct {
	Name        string
	Description string
	Sources     []MissingInputSource
}

// MissingInputError carries missing-input metadata while remaining compatible
// with the extension SDK version currently pinned by this module.
type MissingInputError struct {
	LocalError  *azdext.LocalError
	PromptError *input.PromptRequiredError
	Inputs      []MissingInput
	cause       error
}

// NewMissingInputError creates a structured local error with actionable
// missing-input metadata and executable examples.
func NewMissingInputError(
	code string,
	category azdext.LocalErrorCategory,
	message string,
	inputs ...MissingInput,
) *MissingInputError {
	return &MissingInputError{
		LocalError: &azdext.LocalError{
			Message:    message,
			Code:       code,
			Category:   category,
			Suggestion: missingInputSuggestion(inputs),
		},
		PromptError: &input.PromptRequiredError{
			Message: message,
			Inputs:  promptRequiredInputs(inputs),
		},
		Inputs: inputs,
	}
}

// MissingEnvironmentName returns the common environment-selection remediation.
func MissingEnvironmentName(code, command string, cause error) error {
	err := NewMissingInputError(
		code,
		azdext.LocalErrorCategoryDependency,
		"azd environment name is required",
		MissingInput{
			Name:        "azd environment name",
			Description: "Selects the azd environment used by this command.",
			Sources: []MissingInputSource{
				{
					Kind:         input.InputSourceFlag,
					Name:         "--environment <name> (or -e <name>)",
					ExampleValue: "dev",
					Example:      fmt.Sprintf("azd -e dev %s", command),
				},
				{
					Kind:         input.InputSourceEnvironment,
					Name:         "AZD_ENVIRONMENT",
					ExampleValue: "dev",
					Example:      fmt.Sprintf(`$env:AZD_ENVIRONMENT = "dev"; azd %s`, command),
				},
				{
					Kind:    input.InputSourceConfig,
					Name:    "current environment selection",
					Example: "azd env select dev",
				},
			},
		},
	)
	err.cause = cause
	return err
}

// Error implements the error interface.
func (e *MissingInputError) Error() string {
	return e.LocalError.Error()
}

// Unwrap exposes both the future structured prompt metadata and the currently
// supported LocalError transport.
func (e *MissingInputError) Unwrap() []error {
	errs := []error{e.PromptError, e.LocalError}
	if e.cause != nil {
		errs = append(errs, e.cause)
	}
	return errs
}

func missingInputSuggestion(inputs []MissingInput) string {
	var builder strings.Builder

	for index, missing := range inputs {
		if len(inputs) == 1 {
			fmt.Fprintf(&builder, "Missing required input: %s\n\n", missing.Name)
		} else {
			if index > 0 {
				builder.WriteString("\n")
			}
			fmt.Fprintf(&builder, "%s:\n", missing.Name)
		}

		if len(missing.Sources) > 0 {
			builder.WriteString("Provide one of:\n")
			for _, source := range missing.Sources {
				fmt.Fprintf(&builder, "  %s: %s\n", inputSourceLabel(source.Kind), source.Name)
			}
		}
		if missing.Description != "" {
			fmt.Fprintf(&builder, "\nDescription: %s\n", missing.Description)
		}

		var examples []string
		for _, source := range missing.Sources {
			if source.Example != "" {
				examples = append(examples, source.Example)
			}
		}
		if len(examples) > 0 {
			builder.WriteString("\nExamples:\n")
			for _, example := range examples {
				fmt.Fprintf(&builder, "  %s\n", example)
			}
		}
	}

	return strings.TrimSpace(builder.String())
}

func promptRequiredInputs(inputs []MissingInput) []input.RequiredInput {
	required := make([]input.RequiredInput, len(inputs))
	for i, missing := range inputs {
		sources := make([]input.InputSource, len(missing.Sources))
		for j, source := range missing.Sources {
			sources[j] = promptInputSource(source)
		}
		required[i] = input.RequiredInput{
			Name:        missing.Name,
			Description: missing.Description,
			Sources:     sources,
		}
	}
	return required
}

func promptInputSource(source MissingInputSource) input.InputSource {
	result := input.InputSource{
		Kind:         source.Kind,
		Name:         source.Name,
		ExampleValue: source.ExampleValue,
	}

	// InputSource.Example is additive in the next azd SDK. Reflection keeps this
	// extension buildable on v1.31.0 while populating the field after a merge-safe
	// SDK upgrade becomes available.
	exampleField := reflect.ValueOf(&result).Elem().FieldByName("Example")
	if exampleField.IsValid() && exampleField.CanSet() && exampleField.Kind() == reflect.String {
		exampleField.SetString(source.Example)
	}

	return result
}

func inputSourceLabel(kind input.InputSourceKind) string {
	switch kind {
	case input.InputSourceFlag:
		return "Flag"
	case input.InputSourceEnvironment:
		return "Environment"
	case input.InputSourceConfig:
		return "Config"
	default:
		return "Source"
	}
}
