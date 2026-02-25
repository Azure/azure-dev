// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMonitorCommand_RequiredFlags(t *testing.T) {
	cmd := newMonitorCommand()

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestMonitorCommand_MissingVersionFlag(t *testing.T) {
	cmd := newMonitorCommand()

	cmd.SetArgs([]string{"--name", "test-agent"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

func TestValidateMonitorFlags_Valid(t *testing.T) {
	flags := &monitorFlags{
		tail:    50,
		logType: "console",
	}
	err := validateMonitorFlags(flags)
	assert.NoError(t, err)
}

func TestValidateMonitorFlags_ValidSystem(t *testing.T) {
	flags := &monitorFlags{
		tail:    100,
		logType: "system",
	}
	err := validateMonitorFlags(flags)
	assert.NoError(t, err)
}

func TestValidateMonitorFlags_TailTooLow(t *testing.T) {
	flags := &monitorFlags{
		tail:    0,
		logType: "console",
	}
	err := validateMonitorFlags(flags)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--tail must be between 1 and 300")
}

func TestValidateMonitorFlags_TailTooHigh(t *testing.T) {
	flags := &monitorFlags{
		tail:    301,
		logType: "console",
	}
	err := validateMonitorFlags(flags)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--tail must be between 1 and 300")
}

func TestValidateMonitorFlags_TailBoundary(t *testing.T) {
	flags := &monitorFlags{tail: 1, logType: "console"}
	assert.NoError(t, validateMonitorFlags(flags))

	flags = &monitorFlags{tail: 300, logType: "console"}
	assert.NoError(t, validateMonitorFlags(flags))
}

func TestValidateMonitorFlags_InvalidType(t *testing.T) {
	flags := &monitorFlags{
		tail:    50,
		logType: "invalid",
	}
	err := validateMonitorFlags(flags)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--type must be 'console' or 'system'")
}

func TestMonitorCommand_DefaultValues(t *testing.T) {
	cmd := newMonitorCommand()

	// Verify default flag values
	tail, _ := cmd.Flags().GetInt("tail")
	assert.Equal(t, 50, tail)

	logType, _ := cmd.Flags().GetString("type")
	assert.Equal(t, "console", logType)

	follow, _ := cmd.Flags().GetBool("follow")
	assert.Equal(t, false, follow)
}
