// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import "github.com/azure/azure-dev/cli/azd/pkg/input"

const missingInputErrorCode = "missing_required_input"

// NewMissingInputError creates a structured local error for required inputs.
func NewMissingInputError(
	code string,
	category LocalErrorCategory,
	message string,
	inputs ...input.RequiredInput,
) *LocalError {
	promptErr := newPromptRequiredError(message, inputs...)
	return &LocalError{
		Message:             message,
		Code:                code,
		Category:            category,
		Suggestion:          promptErr.Suggestion(),
		PromptRequiredError: promptErr,
	}
}

// NewMissingInputErrorDetail creates prompt metadata for generic prompt options.
// When prompting is disabled, the azd host returns this metadata to the extension as structured error details.
func NewMissingInputErrorDetail(message string, inputs ...input.RequiredInput) *PromptRequiredErrorDetail {
	return WrapPromptRequiredError(newPromptRequiredError(message, inputs...))
}

// WrapPromptRequiredError converts a prompt-required error to its gRPC representation.
func WrapPromptRequiredError(err *input.PromptRequiredError) *PromptRequiredErrorDetail {
	if err == nil {
		return nil
	}

	inputs := make([]*RequiredInput, len(err.Inputs))
	for i, requiredInput := range err.Inputs {
		sources := make([]*InputSource, len(requiredInput.Sources))
		for j, source := range requiredInput.Sources {
			sources[j] = &InputSource{
				Kind:         wrapInputSourceKind(source.Kind),
				Name:         source.Name,
				ExampleValue: source.ExampleValue,
				Example:      source.Example,
			}
		}

		inputs[i] = &RequiredInput{
			Name:        requiredInput.Name,
			Description: requiredInput.Description,
			Sources:     sources,
		}
	}

	return &PromptRequiredErrorDetail{
		Inputs:        inputs,
		PromptMessage: err.PromptMessage,
		Message:       err.Message,
	}
}

// UnwrapPromptRequiredError converts gRPC prompt-required metadata to the shared input error type.
func UnwrapPromptRequiredError(detail *PromptRequiredErrorDetail) *input.PromptRequiredError {
	if detail == nil {
		return nil
	}

	inputs := make([]input.RequiredInput, len(detail.GetInputs()))
	for i, requiredInput := range detail.GetInputs() {
		if requiredInput == nil {
			continue
		}

		sources := make([]input.InputSource, len(requiredInput.GetSources()))
		for j, source := range requiredInput.GetSources() {
			if source == nil {
				continue
			}

			sources[j] = input.InputSource{
				Kind:         unwrapInputSourceKind(source.GetKind()),
				Name:         source.GetName(),
				ExampleValue: source.GetExampleValue(),
				Example:      source.GetExample(),
			}
		}

		inputs[i] = input.RequiredInput{
			Name:        requiredInput.GetName(),
			Description: requiredInput.GetDescription(),
			Sources:     sources,
		}
	}

	return &input.PromptRequiredError{
		Inputs:        inputs,
		Message:       detail.GetMessage(),
		PromptMessage: detail.GetPromptMessage(),
	}
}

func wrapInputSourceKind(kind input.InputSourceKind) InputSourceKind {
	switch kind {
	case input.InputSourceFlag:
		return InputSourceKind_INPUT_SOURCE_KIND_FLAG
	case input.InputSourceEnvironment:
		return InputSourceKind_INPUT_SOURCE_KIND_ENVIRONMENT
	case input.InputSourceConfig:
		return InputSourceKind_INPUT_SOURCE_KIND_CONFIG
	default:
		return InputSourceKind_INPUT_SOURCE_KIND_UNSPECIFIED
	}
}

func unwrapInputSourceKind(kind InputSourceKind) input.InputSourceKind {
	switch kind {
	case InputSourceKind_INPUT_SOURCE_KIND_FLAG:
		return input.InputSourceFlag
	case InputSourceKind_INPUT_SOURCE_KIND_ENVIRONMENT:
		return input.InputSourceEnvironment
	case InputSourceKind_INPUT_SOURCE_KIND_CONFIG:
		return input.InputSourceConfig
	default:
		return ""
	}
}

func newPromptRequiredError(message string, inputs ...input.RequiredInput) *input.PromptRequiredError {
	return &input.PromptRequiredError{
		Message: message,
		Inputs:  inputs,
	}
}
