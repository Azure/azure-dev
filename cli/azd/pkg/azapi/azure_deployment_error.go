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

// armErrorHints maps common ARM error codes to actionable user guidance.
var armErrorHints = map[string]string{
	"InsufficientQuota": "Your subscription has insufficient quota. " +
		"Check usage with 'az vm list-usage --location <region>' " +
		"or request an increase in the Azure portal.",
	"SkuNotAvailable": "The requested VM size or SKU is not available " +
		"in this region. Try a different region with " +
		"'azd env set AZURE_LOCATION <region>'.",
	"SubscriptionIsOverQuotaForSku": "Your subscription quota for this " +
		"SKU is exceeded. Request a quota increase or use a different SKU.",
	"LocationIsOfferRestricted": "This resource type is restricted in " +
		"the selected region. Try a different region with " +
		"'azd env set AZURE_LOCATION <region>'.",
	"AuthorizationFailed": "You do not have sufficient permissions. " +
		"Ensure you have the required RBAC role " +
		"(e.g., Owner or Contributor) on the target subscription.",
	"InvalidTemplate": "The deployment template contains errors. " +
		"Run 'azd provision --preview' to validate before deploying.",
	"ValidationError": "The deployment failed validation. " +
		"Check resource property values and API versions " +
		"in your Bicep/Terraform files.",
	"Conflict": "A resource with this name already exists or is in " +
		"a conflicting state. Check for soft-deleted resources " +
		"in the Azure portal.",
	"FlagMustBeSetForRestore": "A soft-deleted resource with this " +
		"name exists. Purge it in the Azure portal or use " +
		"a different name.",
	"ResourceNotFound": "A referenced resource was not found. " +
		"Check resource dependencies and deployment ordering " +
		"in your template.",
}

// RootCause returns the deepest (most specific) error code from the deployment error tree.
// This is typically the actual root cause of the failure, as opposed to wrapper error codes
// like DeploymentFailed or ResourceDeploymentFailure.
func (e *AzureDeploymentError) RootCause() *DeploymentErrorLine {
	if e.Details == nil {
		return nil
	}
	return findDeepestError(e.Details)
}

// RootCauseHint returns actionable guidance for the root cause error, if available.
func (e *AzureDeploymentError) RootCauseHint() string {
	root := e.RootCause()
	if root == nil || root.Code == "" {
		return ""
	}
	if hint, ok := armErrorHints[root.Code]; ok {
		return hint
	}
	return ""
}

func findDeepestError(line *DeploymentErrorLine) *DeploymentErrorLine {
	if line == nil {
		return nil
	}
	result, _ := findDeepestErrorWithDepth(line, 0)
	return result
}

func findDeepestErrorWithDepth(line *DeploymentErrorLine, depth int) (*DeploymentErrorLine, int) {
	if line == nil {
		return nil, -1
	}

	var bestNode *DeploymentErrorLine
	bestDepth := -1

	// Search inner errors, tracking the deepest by level
	for _, inner := range line.Inner {
		candidate, candidateDepth := findDeepestErrorWithDepth(inner, depth+1)
		if candidate != nil && candidate.Code != "" && candidateDepth > bestDepth {
			bestNode = candidate
			bestDepth = candidateDepth
		}
	}

	if bestNode != nil {
		return bestNode, bestDepth
	}

	// If no deeper error with code found, return this line if it has a code
	if line.Code != "" {
		return line, depth
	}

	return nil, -1
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
