// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOptimizeCancelCommand_RequiresPositionalArg(t *testing.T) {
	cmd := newOptimizeCancelCommand()

	err := cmd.Args(cmd, []string{})
	assert.Error(t, err)

	err = cmd.Args(cmd, []string{"opt_abc123"})
	assert.NoError(t, err)

	err = cmd.Args(cmd, []string{"opt_abc123", "extra"})
	assert.Error(t, err)
}

func TestOptimizeCancelCommand_HasConnectionFlags(t *testing.T) {
	cmd := newOptimizeCancelCommand()

	assert.NotNil(t, cmd.Flags().Lookup("endpoint"))
	assert.NotNil(t, cmd.Flags().Lookup("project-endpoint"))

	assert.Nil(t, cmd.Flags().Lookup("subscription"))
	assert.Nil(t, cmd.Flags().Lookup("resource-group"))
	assert.Nil(t, cmd.Flags().Lookup("workspace"))
}
