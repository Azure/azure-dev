// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorWithSuggestion_ToString_AllFields(t *testing.T) {
	err := &ErrorWithSuggestion{
		Err:        errors.New("QuotaExceeded: raw error details here"),
		Message:    "Your subscription has reached a quota limit.",
		Suggestion: "Request a quota increase through the Azure portal.",
		DocUrl:     "https://learn.microsoft.com/azure/quotas/",
	}

	result := err.ToString("")

	// Message should be the ERROR line
	assert.Contains(t, result, "ERROR:")
	assert.Contains(t, result, "Your subscription has reached a quota limit")

	// Suggestion should be present
	assert.Contains(t, result, "Suggestion:")
	assert.Contains(t, result, "Request a quota increase")

	// Doc link should be present
	assert.Contains(t, result, "Learn more:")
	assert.Contains(t, result, "https://learn.microsoft.com/azure/quotas/")

	// Raw error should be at the end
	assert.Contains(t, result, "QuotaExceeded: raw error details")
}

func TestErrorWithSuggestion_ToString_WithoutDocUrl(t *testing.T) {
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
	assert.NotContains(t, result, "Learn more:")
	// Raw error should still be shown
	assert.Contains(t, result, "some raw error")
}

func TestErrorWithSuggestion_ToString_WithoutMessage(t *testing.T) {
	err := &ErrorWithSuggestion{
		Err:        errors.New("raw error only"),
		Suggestion: "Try this.",
	}

	result := err.ToString("")

	// Should fall back to showing the raw error as the ERROR line
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
	// Raw error should be shown in grey at the end
	assert.Contains(t, result, "raw error")
}

func TestErrorWithSuggestion_MarshalJSON(t *testing.T) {
	err := &ErrorWithSuggestion{
		Err:        errors.New("test error"),
		Message:    "test message",
		Suggestion: "test suggestion",
		DocUrl:     "https://example.com",
	}

	data, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr)

	var result map[string]string
	require.NoError(t, json.Unmarshal(data, &result))

	assert.Equal(t, "test error", result["error"])
	assert.Equal(t, "test message", result["message"])
	assert.Equal(t, "test suggestion", result["suggestion"])
	assert.Equal(t, "https://example.com", result["docUrl"])
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
	// These should be omitted when empty
	_, hasSuggestion := result["suggestion"]
	_, hasDocUrl := result["docUrl"]
	assert.False(t, hasSuggestion)
	assert.False(t, hasDocUrl)
}

func TestErrorWithSuggestion_WithIndentation(t *testing.T) {
	err := &ErrorWithSuggestion{
		Err:        errors.New("test error"),
		Message:    "test message",
		Suggestion: "test suggestion",
		DocUrl:     "https://example.com",
	}

	result := err.ToString("  ")

	// Lines should respect indentation (allowing for ANSI codes at start)
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		if len(line) > 0 {
			// Either starts with indentation or ANSI escape code
			assert.True(t, strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\x1b"),
				"Line should be indented: %q", line)
		}
	}
}
