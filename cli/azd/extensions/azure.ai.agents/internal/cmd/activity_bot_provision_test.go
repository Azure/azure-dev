// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissingActivityEnvironmentError(t *testing.T) {
	cause := errors.New("no current azd environment is selected")
	err := missingActivityEnvironmentError(cause)

	missingInput, ok := errors.AsType[*exterrors.MissingInputError](err)
	require.True(t, ok)
	require.ErrorIs(t, err, cause)
	require.Len(t, missingInput.Inputs, 1)
	assert.Equal(t, "azd environment", missingInput.Inputs[0].Name)
	require.Len(t, missingInput.Inputs[0].Sources, 3)
	assert.Equal(t, "-e/--environment", missingInput.Inputs[0].Sources[0].Name)
	assert.Equal(t, "AZD_ENVIRONMENT", missingInput.Inputs[0].Sources[1].Name)
	assert.Equal(t, "azd env select <name>", missingInput.Inputs[0].Sources[2].Name)

	suggestion := missingInput.LocalError.Suggestion
	assert.Contains(t, suggestion, "azd -e dev provision")
	assert.Contains(t, suggestion, `$env:AZD_ENVIRONMENT = "dev"; azd provision`)
	assert.Contains(t, suggestion, "azd env select dev; azd provision")
}

func TestResolveActivityEnvironmentNameUsesCommandSelection(t *testing.T) {
	envName, err := resolveActivityEnvironmentName(t.Context(), nil, " dev ")
	require.NoError(t, err)
	assert.Equal(t, "dev", envName)
}
