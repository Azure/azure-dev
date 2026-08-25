// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissingActivityEnvironmentError(t *testing.T) {
	cause := errors.New("no current azd environment is selected")
	err := missingActivityEnvironmentError(cause)

	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.ErrorIs(t, err, cause)

	suggestion := local.Suggestion
	assert.Contains(t, suggestion, "azd -e dev provision")
	assert.Contains(t, suggestion, `$env:AZD_ENVIRONMENT = "dev"; azd provision`)
	assert.Contains(t, suggestion, "azd env select dev; azd provision")
}

func TestResolveActivityEnvironmentNameUsesCommandSelection(t *testing.T) {
	envName, err := resolveActivityEnvironmentName(t.Context(), nil, " dev ")
	require.NoError(t, err)
	assert.Equal(t, "dev", envName)
}
