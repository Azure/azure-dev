// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorWithSuggestion_ToString_AllFields(t *testing.T) {
	err := &ErrorWithSuggestion{
		Err:        errors.New("QuotaExceeded: raw error details here"),
		Message:    "Your subscription has reached a quota limit.",
		Suggestion: "Request a quota increase through the Azure portal.",
		Links: []errorhandler.ErrorLink{
			{
				URL:   "https://learn.microsoft.com/azure/quotas/",
				Title: "Increase Azure quotas",
			},
		},
	}

	result := err.ToString("")

	assert.Contains(t, result, "ERROR:")
	assert.Contains(t, result, "Your subscription has reached a quota limit")
	assert.Contains(t, result, "Suggestion:")
	assert.Contains(t, result, "Request a quota increase")
	assert.Contains(t, result, "https://learn.microsoft.com/azure/quotas/")
	assert.Contains(t, result, "QuotaExceeded: raw error details")
}

func TestErrorWithSuggestion_ToString_MultipleLinks(t *testing.T) {
	err := &ErrorWithSuggestion{
		Err:        errors.New("auth failed"),
		Message:    "Authentication failed.",
		Suggestion: "Sign in again.",
		Links: []errorhandler.ErrorLink{
			{
				URL:   "https://example.com/auth",
				Title: "Authentication guide",
			},
			{
				URL: "https://example.com/troubleshoot",
			},
		},
	}

	result := err.ToString("")

	assert.Contains(t, result, "https://example.com/auth")
	assert.Contains(t, result, "https://example.com/troubleshoot")
	// Both links render as bullet items
	assert.Contains(t, result, "•")
}

func TestErrorWithSuggestion_ToString_NoLinks(t *testing.T) {
	err := &ErrorWithSuggestion{
		Err:        errors.New("some raw error"),
		Message:    "Something went wrong.",
		Suggestion: "Try this fix.",
	}

	result := err.ToString("")

	assert.Contains(t, result, "ERROR:")
	assert.Contains(t, result, "Something went wrong")
	assert.Contains(t, result, "Suggestion:")
	assert.Contains(t, result, "Try this fix")
	assert.NotContains(t, result, "•")
	assert.Contains(t, result, "some raw error")
}

func TestErrorWithSuggestion_ToString_WithoutMessage(t *testing.T) {
	err := &ErrorWithSuggestion{
		Err:        errors.New("raw error only"),
		Suggestion: "Try this.",
	}

	result := err.ToString("")

	assert.Contains(t, result, "ERROR:")
	assert.Contains(t, result, "raw error only")
	assert.Contains(t, result, "Suggestion:")
}

func TestErrorWithSuggestion_ToString_MessageOnly(t *testing.T) {
	err := &ErrorWithSuggestion{
		Err:     errors.New("raw error"),
		Message: "User-friendly message",
	}

	result := err.ToString("")

	assert.Contains(t, result, "ERROR:")
	assert.Contains(t, result, "User-friendly message")
	assert.Contains(t, result, "raw error")
}

func TestErrorWithSuggestion_MarshalJSON(t *testing.T) {
	err := &ErrorWithSuggestion{
		Err:        errors.New("test error"),
		Message:    "test message",
		Suggestion: "test suggestion",
		Links: []errorhandler.ErrorLink{
			{URL: "https://example.com", Title: "Example"},
		},
	}

	data, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &result))

	assert.Equal(t, "test error", result["error"])
	assert.Equal(t, "test message", result["message"])
	assert.Equal(t, "test suggestion", result["suggestion"])

	links := result["links"].([]interface{})
	require.Len(t, links, 1)
	link := links[0].(map[string]interface{})
	assert.Equal(t, "https://example.com", link["url"])
	assert.Equal(t, "Example", link["title"])
}

func TestErrorWithSuggestion_MarshalJSON_OmitsEmpty(t *testing.T) {
	err := &ErrorWithSuggestion{
		Err:     errors.New("test error"),
		Message: "test message",
	}

	data, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &result))

	assert.Equal(t, "test error", result["error"])
	assert.Equal(t, "test message", result["message"])
	_, hasSuggestion := result["suggestion"]
	_, hasLinks := result["links"]
	assert.False(t, hasSuggestion)
	assert.False(t, hasLinks)
}
