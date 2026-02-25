// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetLogsCommand_RequiredFlags(t *testing.T) {
	cmd := newGetLogsCommand()

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestGetLogsCommand_MissingVersionFlag(t *testing.T) {
	cmd := newGetLogsCommand()

	cmd.SetArgs([]string{"--name", "test-agent"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

func TestValidateGetLogsFlags_Valid(t *testing.T) {
	flags := &getLogsFlags{
		tail:    50,
		logType: "console",
	}
	err := validateGetLogsFlags(flags)
	assert.NoError(t, err)
}

func TestValidateGetLogsFlags_ValidSystem(t *testing.T) {
	flags := &getLogsFlags{
		tail:    100,
		logType: "system",
	}
	err := validateGetLogsFlags(flags)
	assert.NoError(t, err)
}

func TestValidateGetLogsFlags_TailTooLow(t *testing.T) {
	flags := &getLogsFlags{
		tail:    0,
		logType: "console",
	}
	err := validateGetLogsFlags(flags)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--tail must be between 1 and 300")
}

func TestValidateGetLogsFlags_TailTooHigh(t *testing.T) {
	flags := &getLogsFlags{
		tail:    301,
		logType: "console",
	}
	err := validateGetLogsFlags(flags)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--tail must be between 1 and 300")
}

func TestValidateGetLogsFlags_TailBoundary(t *testing.T) {
	// Test boundary values
	flags := &getLogsFlags{tail: 1, logType: "console"}
	assert.NoError(t, validateGetLogsFlags(flags))

	flags = &getLogsFlags{tail: 300, logType: "console"}
	assert.NoError(t, validateGetLogsFlags(flags))
}

func TestValidateGetLogsFlags_InvalidType(t *testing.T) {
	flags := &getLogsFlags{
		tail:    50,
		logType: "invalid",
	}
	err := validateGetLogsFlags(flags)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--type must be 'console' or 'system'")
}

func TestGetLogsCommand_DefaultValues(t *testing.T) {
	cmd := newGetLogsCommand()

	// Verify default flag values
	tail, _ := cmd.Flags().GetInt("tail")
	assert.Equal(t, 50, tail)

	logType, _ := cmd.Flags().GetString("type")
	assert.Equal(t, "console", logType)

	follow, _ := cmd.Flags().GetBool("follow")
	assert.Equal(t, false, follow)
}
