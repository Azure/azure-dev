// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
)

// DefaultPromptRequiredMessage is the default headline used when a command cannot continue without prompting.
const DefaultPromptRequiredMessage = "This command cannot continue (interactive prompts disabled)"

const (
	promptRequiredCode                    = "promptRequired"
	promptRequiredMissingInputsType       = "missingRequiredInputs"
	promptRequiredInteractiveRequiredType = "interactiveRequired"
)

// InputSourceKind identifies the kind of source that can satisfy a required input.
type InputSourceKind string

const (
	InputSourceFlag        InputSourceKind = "flag"
	InputSourceEnvironment InputSourceKind = "environment"
	InputSourceConfig      InputSourceKind = "config"
)

// InputSource describes one way a required input can be supplied.
type InputSource struct {
	Kind         InputSourceKind `json:"kind"`
	Name         string          `json:"name"`
	ExampleValue string          `json:"exampleValue,omitempty"`
}

// RequiredInput describes a missing input and the supported sources that can provide it.
type RequiredInput struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Sources     []InputSource `json:"sources,omitempty"`
}

// PromptRequiredError is returned when --no-prompt mode prevents collecting required inputs interactively.
//
// Either Inputs or PromptMessage is set, but not both.
type PromptRequiredError struct {
	// Inputs is the list of required inputs that are missing.
	Inputs []RequiredInput
	// Message is the headline used for structured missing-input output.
	Message string

	// PromptMessage is the prompt text that would have been displayed to the user if interactive prompts were allowed.
	PromptMessage string
}

// Error implements the error interface.
func (e *PromptRequiredError) Error() string {
	return "prompt required"
}

// ToString returns a formatted message with the missing inputs and short remediation guidance.
func (e *PromptRequiredError) ToString(currentIndentation string) string {
	if len(e.Inputs) == 0 && e.PromptMessage != "" {
		return e.promptMessageToString()
	}

	var buf strings.Builder
	separator := "──────────────────────────────────────────────────────────────"

	buf.WriteString(separator + "\n")
	buf.WriteString(e.message() + "\n")
	buf.WriteString(separator + "\n\n")

	switch len(e.Inputs) {
	case 0:
		buf.WriteString("Required input is missing.\n")
	case 1:
		buf.WriteString("1 required input is missing.\n")
	default:
		buf.WriteString(fmt.Sprintf("%d required inputs are missing.\n", len(e.Inputs)))
	}

	if len(e.Inputs) > 0 {
		buf.WriteString("\nMissing required inputs:\n\n")
	}

	for _, input := range e.Inputs {
		buf.WriteString(fmt.Sprintf("• %s\n", input.Name))

		if len(input.Sources) > 0 {
			buf.WriteString("    Provide one of:\n")
			for _, source := range input.Sources {
				buf.WriteString(fmt.Sprintf("      %s: %s\n", sourceKindLabel(source.Kind), source.Name))
			}
		}

		if input.Description != "" {
			buf.WriteString(fmt.Sprintf("    Description: %s\n", input.Description))
		}

		buf.WriteString("\n")
	}

	if e.hasEnvironmentSource() {
		exampleSources := e.environmentSourcesWithExamples()
		if len(exampleSources) == 0 {
			buf.WriteString("Example:\n")
			buf.WriteString("  azd env set <ENV_VAR_NAME> <value>\n\n")
		} else {
			if len(exampleSources) == 1 {
				buf.WriteString("Example:\n")
			} else {
				buf.WriteString("Examples:\n")
			}
			for _, source := range exampleSources {
				buf.WriteString(fmt.Sprintf("  azd env set %s %s\n", source.Name, source.ExampleValue))
			}
			buf.WriteString("\n")
		}
	}

	return buf.String()
}

// MarshalJSON implements json.Marshaler.
func (e *PromptRequiredError) MarshalJSON() ([]byte, error) {
	type details struct {
		Type          string          `json:"type"`
		PromptMessage string          `json:"promptMessage,omitempty"`
		Inputs        []RequiredInput `json:"inputs,omitempty"`
	}

	d := details{Inputs: e.Inputs}
	if len(e.Inputs) == 0 && e.PromptMessage != "" {
		d.Type = promptRequiredInteractiveRequiredType
		d.PromptMessage = e.PromptMessage
	} else {
		d.Type = promptRequiredMissingInputsType
	}

	return json.Marshal(contracts.ErrorEnvelope[details]{
		Code:    promptRequiredCode,
		Message: e.message(),
		Details: d,
	})
}

func (e *PromptRequiredError) message() string {
	if e.Message == "" {
		return DefaultPromptRequiredMessage
	}

	return e.Message
}

func (e *PromptRequiredError) promptMessageToString() string {
	var buf strings.Builder

	buf.WriteString("The following prompt requires user input:\n\n")
	buf.WriteString(fmt.Sprintf("  ? %s\n\n", e.PromptMessage))
	buf.WriteString("This prompt cannot be answered non-interactively. ")
	buf.WriteString("To proceed, run this command in interactive mode.\n")

	return buf.String()
}

func (e *PromptRequiredError) hasEnvironmentSource() bool {
	for _, input := range e.Inputs {
		for _, source := range input.Sources {
			if source.Kind == InputSourceEnvironment {
				return true
			}
		}
	}

	return false
}

func (e *PromptRequiredError) environmentSourcesWithExamples() []InputSource {
	var sources []InputSource

	for _, input := range e.Inputs {
		for _, source := range input.Sources {
			if source.Kind == InputSourceEnvironment && source.ExampleValue != "" {
				sources = append(sources, source)
			}
		}
	}

	return sources
}

func sourceKindLabel(kind InputSourceKind) string {
	switch kind {
	case InputSourceFlag:
		return "Flag"
	case InputSourceEnvironment:
		return "Environment"
	case InputSourceConfig:
		return "Config"
	default:
		return "Source"
	}
}
