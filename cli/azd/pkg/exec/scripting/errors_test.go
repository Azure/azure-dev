// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scripting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{
		Field:  "scriptPath",
		Reason: "cannot be empty",
	}
	assert.Equal(t,
		"validation error for scriptPath: cannot be empty",
		err.Error(),
	)
}

func TestScriptNotFoundError_Error(t *testing.T) {
	err := &ScriptNotFoundError{Path: "missing.sh"}
	assert.Equal(t, "script not found: missing.sh", err.Error())
}

func TestInvalidShellError_Error(t *testing.T) {
	err := &InvalidShellError{Shell: "python"}
	got := err.Error()
	assert.Contains(t, got, "python")
	assert.Contains(t, got, "invalid shell")
}

func TestExecutionError_Error_NoShell(t *testing.T) {
	err := &ExecutionError{ExitCode: 1, Shell: "", IsInline: false}
	assert.Equal(t, "command exited with code 1", err.Error())
}

func TestExecutionError_Error_Inline(t *testing.T) {
	err := &ExecutionError{
		ExitCode: 2, Shell: "bash", IsInline: true,
	}
	got := err.Error()
	assert.Contains(t, got, "inline script")
	assert.Contains(t, got, "2")
	assert.Contains(t, got, "bash")
}

func TestExecutionError_Error_Script(t *testing.T) {
	err := &ExecutionError{
		ExitCode: 42, Shell: "pwsh", IsInline: false,
	}
	got := err.Error()
	assert.Contains(t, got, "script exited with code 42")
	assert.Contains(t, got, "pwsh")
	assert.NotContains(t, got, "inline")
}
