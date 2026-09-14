// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectServiceTargetPreviewDoesNotProvision(t *testing.T) {
	t.Parallel()
	// No client or initialization: project deployment is a no-op.
	target := &projectServiceTarget{}
	result, err := target.Preview(t.Context(), &azdext.ServiceConfig{Name: "ai-project", Host: aiProjectHost})
	require.NoError(t, err)
	assert.Contains(t, result.Message, "azd provision --preview")
	assert.Equal(t, "none", result.Data.Fields["operation"].GetStringValue())
	assert.False(t, result.Data.Fields["hasChanges"].GetBoolValue())
	assert.Nil(t, target.serviceConfig)
}
