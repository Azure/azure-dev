// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProjectUnsetCommand_AcceptsNoArgs(t *testing.T) {
	t.Parallel()
	cmd := newProjectUnsetCommand(nil)
	assert.NoError(t, cmd.Args(cmd, []string{}))
}

func TestProjectUnsetCommand_RejectsArgs(t *testing.T) {
	t.Parallel()
	cmd := newProjectUnsetCommand(nil)
	assert.Error(t, cmd.Args(cmd, []string{"extra"}))
}

func TestProjectUnsetCommand_DefaultOutputFormat(t *testing.T) {
	t.Parallel()
	cmd := newProjectUnsetCommand(nil)
	assertOutputFlagOptions(t, cmd, "table", []string{"json", "table"})
}
