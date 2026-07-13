// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptimizeListCommand_AcceptsLimitAndStatusFlags(t *testing.T) {
	cmd := newOptimizeListCommand(&azdext.ExtensionContext{})

	limitFlag := cmd.Flags().Lookup("limit")
	require.NotNil(t, limitFlag, "--limit flag should be registered")

	limitVal, err := cmd.Flags().GetInt("limit")
	require.NoError(t, err)
	assert.Equal(t, 20, limitVal, "--limit should default to 20")

	statusFlag := cmd.Flags().Lookup("status")
	require.NotNil(t, statusFlag, "--status flag should be registered")

	statusVal, err := cmd.Flags().GetString("status")
	require.NoError(t, err)
	assert.Equal(t, "", statusVal, "--status should default to empty")
}

func TestOptimizeListCommand_HasConnectionFlags(t *testing.T) {
	cmd := newOptimizeListCommand(&azdext.ExtensionContext{})

	assert.NotNil(t, cmd.Flags().Lookup("endpoint"))
	assert.NotNil(t, cmd.Flags().Lookup("project-endpoint"))

	assert.Nil(t, cmd.Flags().Lookup("subscription"))
	assert.Nil(t, cmd.Flags().Lookup("resource-group"))
	assert.Nil(t, cmd.Flags().Lookup("workspace"))
}
