// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWarningMessage_ToString_Basic(t *testing.T) {
	warning := &WarningMessage{
		Description: "Something went wrong",
		HidePrefix:  false,
	}

	result := warning.ToString("")
	require.Contains(t, result, "WARNING:")
	require.Contains(t, result, "Something went wrong")
}

func TestWarningMessage_ToString_HiddenPrefix(t *testing.T) {
	warning := &WarningMessage{
		Description: "Something went wrong",
		HidePrefix:  true,
	}

	result := warning.ToString("")
	require.NotContains(t, result, "WARNING:")
	require.Contains(t, result, "Something went wrong")
}

func TestWarningMessage_ToString_WithIndentation(t *testing.T) {
	warning := &WarningMessage{
		Description: "Something went wrong",
		HidePrefix:  false,
	}

	result := warning.ToString("  ")
	require.Contains(t, result, "Something went wrong")
}

func TestWarningMessage_ToString_WithHints(t *testing.T) {
	warning := &WarningMessage{
		Description: "Extension update available",
		HidePrefix:  false,
		Hints: []string{
			"To update: azd extension update test.ext",
			"To update all: azd extension update --all",
		},
	}

	result := warning.ToString("")
	require.Contains(t, result, "WARNING:")
	require.Contains(t, result, "Extension update available")
	require.Contains(t, result, "•")
	require.Contains(t, result, "To update: azd extension update test.ext")
	require.Contains(t, result, "To update all: azd extension update --all")
}

func TestWarningMessage_ToString_WithHintsAndIndentation(t *testing.T) {
	warning := &WarningMessage{
		Description: "Extension update available",
		HidePrefix:  false,
		Hints: []string{
			"First hint",
			"Second hint",
		},
	}

	result := warning.ToString("  ")
	require.Contains(t, result, "Extension update available")
	require.Contains(t, result, "First hint")
	require.Contains(t, result, "Second hint")
}

func TestWarningMessage_ToString_EmptyHints(t *testing.T) {
	warning := &WarningMessage{
		Description: "Something went wrong",
		HidePrefix:  false,
		Hints:       []string{},
	}

	result := warning.ToString("")
	require.Contains(t, result, "WARNING:")
	require.Contains(t, result, "Something went wrong")
	// Should not contain bullet point when no hints
	require.NotContains(t, result, "•")
}

func TestWarningMessage_MarshalJSON_Basic(t *testing.T) {
	warning := &WarningMessage{
		Description: "Something went wrong",
		HidePrefix:  false,
	}

	data, err := json.Marshal(warning)
	require.NoError(t, err)
	require.Contains(t, string(data), "WARNING:")
	require.Contains(t, string(data), "Something went wrong")
}

func TestWarningMessage_MarshalJSON_HiddenPrefix(t *testing.T) {
	warning := &WarningMessage{
		Description: "Something went wrong",
		HidePrefix:  true,
	}

	data, err := json.Marshal(warning)
	require.NoError(t, err)
	require.NotContains(t, string(data), "WARNING:")
	require.Contains(t, string(data), "Something went wrong")
}

func TestWarningMessage_MarshalJSON_WithHints(t *testing.T) {
	warning := &WarningMessage{
		Description: "Extension update available",
		HidePrefix:  false,
		Hints: []string{
			"To update: azd extension update test.ext",
			"To update all: azd extension update --all",
		},
	}

	data, err := json.Marshal(warning)
	require.NoError(t, err)
	jsonStr := string(data)
	require.Contains(t, jsonStr, "WARNING:")
	require.Contains(t, jsonStr, "Extension update available")
	require.Contains(t, jsonStr, "To update: azd extension update test.ext")
	require.Contains(t, jsonStr, "To update all: azd extension update --all")
}

func TestWarningMessage_MarshalJSON_EmptyHints(t *testing.T) {
	warning := &WarningMessage{
		Description: "Something went wrong",
		HidePrefix:  false,
		Hints:       []string{},
	}

	data, err := json.Marshal(warning)
	require.NoError(t, err)
	require.Contains(t, string(data), "WARNING:")
	require.Contains(t, string(data), "Something went wrong")
}
