// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// ErrorWithSuggestion displays an error with user-friendly messaging.
// Layout:
//  1. User-friendly message (what went wrong)
//  2. Suggestion (actionable next steps)
//  3. Doc link (optional, for more info)
//  4. Original error (grey, de-emphasized technical details)
type ErrorWithSuggestion struct {
	// Err is the original underlying error
	Err error

	// Message is a user-friendly explanation of what went wrong
	Message string

	// Suggestion is actionable next steps to resolve the issue
	Suggestion string

	// DocUrl is an optional link to documentation for more information
	DocUrl string
}

func (e *ErrorWithSuggestion) ToString(currentIndentation string) string {
	var sb strings.Builder

	// 1. User-friendly message (or fall back to raw error if no message)
	errorMsg := e.Message
	if errorMsg == "" && e.Err != nil {
		errorMsg = e.Err.Error()
	}
	sb.WriteString(output.WithErrorFormat("%sERROR: %s", currentIndentation, errorMsg))
	sb.WriteString("\n")

	// 2. Suggestion (actionable next steps)
	if e.Suggestion != "" {
		sb.WriteString(fmt.Sprintf("\n%s%s %s\n",
			currentIndentation,
			output.WithHighLightFormat("Suggestion:"),
			e.Suggestion))
	}

	// 3. Documentation link (if provided)
	if e.DocUrl != "" {
		sb.WriteString(fmt.Sprintf("%s%s %s\n",
			currentIndentation,
			output.WithGrayFormat("Learn more:"),
			output.WithLinkFormat(e.DocUrl)))
	}

	// 4. Original error in grey (technical details, de-emphasized)
	if e.Message != "" && e.Err != nil {
		sb.WriteString(fmt.Sprintf("\n%s%s\n",
			currentIndentation,
			output.WithGrayFormat(e.Err.Error())))
	}

	return sb.String()
}

func (e *ErrorWithSuggestion) MarshalJSON() ([]byte, error) {
	errStr := ""
	if e.Err != nil {
		errStr = e.Err.Error()
	}

	result := struct {
		Error      string `json:"error"`
		Message    string `json:"message,omitempty"`
		Suggestion string `json:"suggestion,omitempty"`
		DocUrl     string `json:"docUrl,omitempty"`
	}{
		Error:      errStr,
		Message:    e.Message,
		Suggestion: e.Suggestion,
		DocUrl:     e.DocUrl,
	}

	return json.Marshal(result)
}
