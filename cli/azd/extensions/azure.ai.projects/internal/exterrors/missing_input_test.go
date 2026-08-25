// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissingEnvironmentName(t *testing.T) {
	cause := errors.New("no current azd environment is selected")
	err := MissingEnvironmentName(CodeEnvironmentNotFound, "provision", cause)
	assert.Equal(t, "azd environment name is required", err.Error())
	assert.ErrorIs(t, err, cause)

	var missing *MissingInputError
	require.ErrorAs(t, err, &missing)
	require.Len(t, missing.Inputs, 1)
	assert.Equal(t, "azd environment name", missing.Inputs[0].Name)
	require.Len(t, missing.Inputs[0].Sources, 3)
	assert.Equal(t, input.InputSourceFlag, missing.Inputs[0].Sources[0].Kind)
	assert.Equal(t, "--environment <name> (or -e <name>)", missing.Inputs[0].Sources[0].Name)
	assert.Equal(t, "azd -e dev provision", missing.Inputs[0].Sources[0].Example)
	assert.Equal(t, input.InputSourceEnvironment, missing.Inputs[0].Sources[1].Kind)
	assert.Equal(t, "AZD_ENVIRONMENT", missing.Inputs[0].Sources[1].Name)
	assert.Equal(t, `$env:AZD_ENVIRONMENT = "dev"; azd provision`, missing.Inputs[0].Sources[1].Example)
	assert.Equal(t, input.InputSourceConfig, missing.Inputs[0].Sources[2].Kind)
	assert.Equal(t, "current environment selection", missing.Inputs[0].Sources[2].Name)
	assert.Equal(t, "azd env select dev", missing.Inputs[0].Sources[2].Example)

	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Equal(t, CodeEnvironmentNotFound, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryDependency, local.Category)
	assert.Contains(t, local.Suggestion, "Missing required input: azd environment name")
	assert.Contains(t, local.Suggestion, "Flag: --environment <name> (or -e <name>)")
	assert.Contains(t, local.Suggestion, "Environment: AZD_ENVIRONMENT")
	assert.Contains(t, local.Suggestion, "Config: current environment selection")
	assert.Contains(t, local.Suggestion, "azd -e dev provision")
	assert.Contains(t, local.Suggestion, `$env:AZD_ENVIRONMENT = "dev"; azd provision`)
	assert.Contains(t, local.Suggestion, "azd env select dev")

	wrapped := azdext.WrapError(err)
	require.NotNil(t, wrapped.GetLocalError())
	assert.Equal(t, CodeEnvironmentNotFound, wrapped.GetLocalError().GetCode())
	assert.NotEmpty(t, wrapped.GetSuggestion())
}
