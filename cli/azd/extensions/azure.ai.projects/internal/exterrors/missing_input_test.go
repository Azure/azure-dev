// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissingEnvironmentName(t *testing.T) {
	cause := errors.New("no current azd environment is selected")
	err := MissingEnvironmentName(CodeEnvironmentNotFound, "provision", cause)
	assert.Equal(t, "azd environment name is required", err.Error())
	assert.ErrorIs(t, err, cause)

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
