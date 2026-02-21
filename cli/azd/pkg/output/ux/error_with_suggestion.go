// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// ErrorWithSuggestion displays an error with user-friendly messaging.
// Layout:
//  1. User-friendly message (red ERROR: line)
//  2. Suggestion (actionable next steps)
//  3. Reference links (optional, as a list)
//  4. Original error (grey, de-emphasized technical details)
type ErrorWithSuggestion struct {
	// Err is the original underlying error
	Err error

	// Message is a user-friendly explanation of what went wrong
	Message string

	// Suggestion is actionable next steps to resolve the issue
	Suggestion string

	// Links is an optional list of reference links
	Links []errorhandler.ErrorLink
}

func (e *ErrorWithSuggestion) ToString(currentIndentation string) string {
	var sb strings.Builder

	// 1. User-friendly message (or fall back to raw error)
	errorMsg := e.Message
	if errorMsg == "" && e.Err != nil {
		errorMsg = e.Err.Error()
	}
	sb.WriteString(output.WithErrorFormat("ERROR: %s\n", errorMsg))

	// 2. Suggestion
	if e.Suggestion != "" {
		sb.WriteString(fmt.Sprintf("\n%s %s\n",
			output.WithHighLightFormat("Suggestion:"),
			e.Suggestion))
	}

	// 3. Reference links
	if len(e.Links) > 0 {
		sb.WriteString("\n")
		for _, link := range e.Links {
			if link.Title != "" {
				sb.WriteString(fmt.Sprintf("  • %s\n",
					output.WithHyperlink(link.URL, link.Title)))
			} else {
				sb.WriteString(fmt.Sprintf("  • %s\n",
					output.WithLinkFormat(link.URL)))
			}
		}
	}

	// 4. Original error in grey (technical details, de-emphasized)
	if e.Message != "" && e.Err != nil {
		sb.WriteString(fmt.Sprintf("\n%s\n",
			output.WithGrayFormat(e.Err.Error())))
	}

	return sb.String()
}

type jsonLink struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

func (e *ErrorWithSuggestion) MarshalJSON() ([]byte, error) {
	errStr := ""
	if e.Err != nil {
		errStr = e.Err.Error()
	}

	var links []jsonLink
	for _, l := range e.Links {
		links = append(links, jsonLink{URL: l.URL, Title: l.Title})
	}

	result := struct {
		Error      string     `json:"error"`
		Message    string     `json:"message,omitempty"`
		Suggestion string     `json:"suggestion,omitempty"`
		Links      []jsonLink `json:"links,omitempty"`
	}{
		Error:      errStr,
		Message:    e.Message,
		Suggestion: e.Suggestion,
		Links:      links,
	}

	return json.Marshal(result)
}
