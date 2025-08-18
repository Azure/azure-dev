// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package common

import "github.com/tmc/langchaingo/tools"

// ToPtr converts a value to a pointer
func ToPtr[T any](value T) *T {
	return &value
}

// ToLangChainTools converts a slice of AnnotatedTool to a slice of tools.Tool
func ToLangChainTools(annotatedTools []AnnotatedTool) []tools.Tool {
	var rawTools []tools.Tool
	for _, tool := range annotatedTools {
		rawTools = append(rawTools, tool)
	}

	return rawTools
}
