// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

type suggestionError struct {
	message    string
	suggestion string
}

func (e *suggestionError) Error() string {
	return e.message
}

func (e *suggestionError) Suggestion() string {
	return e.suggestion
}

func TestWrapErrorWithSuggestion(t *testing.T) {
	t.Parallel()

	require.Nil(t, WrapErrorWithSuggestion(nil))

	plain := fmt.Errorf("plain failure")
	require.Equal(t, plain, WrapErrorWithSuggestion(plain))

	already := &ErrorWithSuggestion{
		Err:        plain,
		Suggestion: "existing",
	}
	require.Equal(t, already, WrapErrorWithSuggestion(already))

	typed := &suggestionError{
		message:    "incompatible",
		suggestion: "update azd",
	}
	wrapped := WrapErrorWithSuggestion(fmt.Errorf("install failed: %w", typed))
	suggestionErr, ok := errors.AsType[*ErrorWithSuggestion](wrapped)
	require.True(t, ok)
	require.Equal(t, "update azd", suggestionErr.Suggestion)
	require.ErrorIs(t, suggestionErr.Err, typed)
}
