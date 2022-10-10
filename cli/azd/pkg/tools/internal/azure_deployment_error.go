package internal

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type errorLine struct {
	message     string
	innerErrors []*errorLine
}

func newErrorLine(message string, innerErrors []*errorLine) *errorLine {
	return &errorLine{
		message:     message,
		innerErrors: innerErrors,
	}
}

type AzureDeploymentError struct {
	Json string
}

func NewAzureDeploymentError(jsonErrorResponse string) *AzureDeploymentError {
	return &AzureDeploymentError{Json: jsonErrorResponse}
}

func (e *AzureDeploymentError) Error() string {
	var errorMap map[string]interface{}
	err := json.Unmarshal([]byte(e.Json), &errorMap)

	// Return the original error string in the event of JSON marshaling error
	if err != nil {
		return e.Json
	}

	errors := getErrorsFromMap(errorMap)
	lines := generateErrorOutput(errors)

	var sb strings.Builder

	for _, line := range lines {
		sb.WriteString(fmt.Sprintln(output.WithErrorFormat(line)))
	}

	return sb.String()
}

func generateErrorOutput(error *errorLine) []string {
	lines := []string{}

	if strings.TrimSpace(error.message) != "" {
		lines = append(lines, error.message)
	}

	for _, innerError := range error.innerErrors {
		if innerError != nil {
			lines = append(lines, generateErrorOutput(innerError)...)
		}
	}

	return lines
}

func getErrorsFromMap(errorMap map[string]interface{}) *errorLine {
	var output *errorLine
	var code, message string

	// Size of nested output is not known ahead of time.
	nestedOutput := []*errorLine{}

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
			var line *errorLine
			if !ok {
				line = &errorLine{message: fmt.Sprintf("%s", value)}
			} else {
				line = getErrorsFromMap(errorMap)
			}

			if line != nil {
				nestedOutput = append(nestedOutput, line)
			}
		case "details":
			var lines []*errorLine
			errorArray, ok := value.([]interface{})
			if !ok {
				line := &errorLine{message: fmt.Sprintf("%s", value)}
				lines = []*errorLine{line}
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
		return newErrorLine(errorMessage, nestedOutput)
	}

	if code != "" && message != "" {
		errorMessage = fmt.Sprintf("%s: %s", code, message)
	} else if message != "" {
		errorMessage = fmt.Sprintf("- %s", message)
	}

	output = newErrorLine(errorMessage, nestedOutput)

	return output
}

func getErrorsFromArray(errorArray []interface{}) []*errorLine {
	output := make([]*errorLine, len(errorArray))
	for index, value := range errorArray {
		errorMap, ok := value.(map[string]interface{})
		if !ok {
			output[index] = &errorLine{message: fmt.Sprintf("%s", value)}
		} else {
			output[index] = getErrorsFromMap(errorMap)
		}
	}

	return output
}
