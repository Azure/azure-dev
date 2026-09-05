// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"fmt"
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

// MissingInputError renders missing-input guidance through the extension SDK's
// currently supported LocalError transport.
type MissingInputError struct {
	LocalError *azdext.LocalError
	cause      error
}

// NewMissingInputError creates a structured local error with actionable
// missing-input guidance and executable examples.
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

// Unwrap exposes the LocalError transport and optional cause.
func (e *MissingInputError) Unwrap() []error {
	errs := []error{e.LocalError}
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
