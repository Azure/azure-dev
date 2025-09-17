// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package common

import (
	"encoding/json"
	"fmt"

	"github.com/tmc/langchaingo/tools"
)

// ToPtr converts a value to a pointer
func ToPtr[T any](value T) *T {
	return &value
}

// ToLangChainTools converts a slice of AnnotatedTool to a slice of tools.Tool
func ToLangChainTools(annotatedTools []AnnotatedTool) []tools.Tool {
	rawTools := make([]tools.Tool, len(annotatedTools))
	for i, tool := range annotatedTools {
		rawTools[i] = tool
	}
	return rawTools
}

// CreateErrorResponse creates a JSON error response with consistent formatting
// Used by all IO tools to maintain consistent error response structure
func CreateErrorResponse(err error, message string) (string, error) {
	if message == "" {
		message = err.Error()
	}

	errorResp := ErrorResponse{
		Error:   true,
		Message: message,
	}

	jsonData, jsonErr := json.MarshalIndent(errorResp, "", "  ")
	if jsonErr != nil {
		// Fallback to simple error message if JSON marshalling fails
		fallbackMsg := fmt.Sprintf(`{"error": true, "message": "JSON marshalling failed: %s"}`, jsonErr.Error())
		return fallbackMsg, nil
	}

	return string(jsonData), nil
}
