// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrorWithSuggestion(t *testing.T) {
	baseErr := errors.New("some error")
	suggestion := "To run without prompts, use: azd provision --no-prompt"

	errWithSuggestion := &ErrorWithSuggestion{
		Err:        baseErr,
		Suggestion: suggestion,
	}

	require.Equal(t, "some error", errWithSuggestion.Error())
	require.Equal(t, baseErr, errWithSuggestion.Unwrap())
}
