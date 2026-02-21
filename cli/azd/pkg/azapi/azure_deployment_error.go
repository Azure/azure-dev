// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type DeploymentErrorLine struct {
	// The code of the error line, if applicable
	Code string
	// The message that represents the error
	Message string
	// Inner errors
	Inner []*DeploymentErrorLine
}

// Error implements the error interface for DeploymentErrorLine.
func (e *DeploymentErrorLine) Error() string {
	if e.Code != "" && e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return "deployment error"
}

// Unwrap returns the inner errors for use with errors.As/errors.Is.
// This enables Go's standard error unwrapping to traverse the full
// ARM deployment error tree.
func (e *DeploymentErrorLine) Unwrap() []error {
	if len(e.Inner) == 0 {
		return nil
	}
	errs := make([]error, 0, len(e.Inner))
	for _, inner := range e.Inner {
		if inner != nil {
			errs = append(errs, inner)
		}
	}
	return errs
}

func newErrorLine(code string, message string, inner []*DeploymentErrorLine) *DeploymentErrorLine {
	return &DeploymentErrorLine{
		Message: message,
		Code:    code,
		Inner:   inner,
	}
}

type AzureDeploymentError struct {
	Json      string
	Inner     error
	Title     string
	Operation DeploymentOperation

	Details *DeploymentErrorLine
}

func NewAzureDeploymentError(title string, jsonErrorResponse string, operation DeploymentOperation) *AzureDeploymentError {
	err := &AzureDeploymentError{Title: title, Json: jsonErrorResponse, Operation: operation}
	err.init()
	return err
}

func (e *AzureDeploymentError) init() {
	var errorMap map[string]interface{}
	if err := json.Unmarshal([]byte(e.Json), &errorMap); err == nil {
		e.Details = getErrorsFromMap(errorMap)
	}
}

func (e *AzureDeploymentError) Error() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n\n%s:\n", e.Title))

	// Return the original error string if we can't parse the JSON
	if e.Details == nil {
		sb.WriteString(e.Json)
		return sb.String()
	}

	lines := generateErrorOutput(e.Details)
	for _, line := range lines {
		sb.WriteString(fmt.Sprintln(output.WithErrorFormat(line)))
	}

	return sb.String()
}

// Unwrap returns the inner errors for use with errors.As/errors.Is.
// It returns both the wrapped Inner error and the Details error tree,
// enabling Go's standard error traversal across the full error structure.
func (e *AzureDeploymentError) Unwrap() []error {
	var errs []error
	if e.Inner != nil {
		errs = append(errs, e.Inner)
	}
	if e.Details != nil {
		errs = append(errs, e.Details)
	}
	return errs
}

func generateErrorOutput(err *DeploymentErrorLine) []string {
	lines := []string{}

	if strings.TrimSpace(err.Message) != "" {
		lines = append(lines, err.Message)
	}

	for _, innerError := range err.Inner {
		if innerError != nil {
			lines = append(lines, generateErrorOutput(innerError)...)
		}
	}

	return lines
}

func getErrorsFromMap(errorMap map[string]interface{}) *DeploymentErrorLine {
	var output *DeploymentErrorLine
	var code, message string

	// Size of nested output is not known ahead of time.
	nestedOutput := []*DeploymentErrorLine{}

	for key, value := range errorMap {
		switch strings.ToLower(key) {
		case "code":
			code = fmt.Sprint(value)
		case "message":
			rawMessage := fmt.Sprint(value)
			var messageMap map[string]interface{}
			err := json.Unmarshal([]byte(rawMessage), &messageMap)
			if err == nil {
				nestedOutput = append(nestedOutput, getErrorsFromMap(messageMap))
			} else {
				message = rawMessage
			}
		case "error":
			errorMap, ok := value.(map[string]interface{})
			var line *DeploymentErrorLine
			if !ok {
				line = &DeploymentErrorLine{Message: fmt.Sprintf("%s", value)}
			} else {
				line = getErrorsFromMap(errorMap)
			}

			if line != nil {
				nestedOutput = append(nestedOutput, line)
			}
		case "details":
			var lines []*DeploymentErrorLine
			errorArray, ok := value.([]interface{})
			if !ok {
				line := &DeploymentErrorLine{Message: fmt.Sprintf("%s", value)}
				lines = []*DeploymentErrorLine{line}
			} else {
				lines = getErrorsFromArray(errorArray)
			}
			nestedOutput = append(nestedOutput, lines...)
		}
	}

	// Append status, codes, messages first
	var errorMessage string

	// Omit generic deployment failed messages
	if code == "DeploymentFailed" || code == "ResourceDeploymentFailure" {
		return newErrorLine("", errorMessage, nestedOutput)
	}

	if code != "" && message != "" {
		errorMessage = fmt.Sprintf("%s: %s", code, message)
	} else if message != "" {
		errorMessage = fmt.Sprintf("- %s", message)
	}

	output = newErrorLine(code, errorMessage, nestedOutput)

	return output
}

func getErrorsFromArray(errorArray []interface{}) []*DeploymentErrorLine {
	output := make([]*DeploymentErrorLine, len(errorArray))
	for index, value := range errorArray {
		errorMap, ok := value.(map[string]interface{})
		if !ok {
			output[index] = &DeploymentErrorLine{Message: fmt.Sprintf("%s", value)}
		} else {
			output[index] = getErrorsFromMap(errorMap)
		}
	}

	return output
}
