// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProjectSetCommand_RequiresExactlyOneArg(t *testing.T) {
	t.Parallel()
	cmd := newProjectSetCommand(nil)
	assert.Error(t, cmd.Args(cmd, []string{}))
	assert.NoError(t, cmd.Args(cmd, []string{"https://x.services.ai.azure.com/api/projects/p"}))
	assert.Error(t, cmd.Args(cmd, []string{"a", "b"}))
}

func TestProjectSetCommand_DefaultOutputFormat(t *testing.T) {
	t.Parallel()
	cmd := newProjectSetCommand(nil)
	assertOutputFlagOptions(t, cmd, "table", []string{"json", "table"})
}
