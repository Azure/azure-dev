// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scripting

import (
	"context"
	"errors"
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

func TestValidationError_Unwrap(t *testing.T) {
	cause := errors.New("cause")
	err := &ValidationError{
		Field:  "scriptPath",
		Reason: "cannot access",
		Err:    cause,
	}

	assert.ErrorIs(t, err, cause)
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

func TestExecutionError_Interrupted(t *testing.T) {
	assert.False(t, (&ExecutionError{ExitCode: 1}).Interrupted())
	assert.True(t, (&ExecutionError{ExitCode: 130}).Interrupted())
	assert.False(t, (*ExecutionError)(nil).Interrupted())
}

func TestExecutionError_UnkeyedLiteralCompatibility(t *testing.T) {
	err := &ExecutionError{1, "", false}
	assert.Equal(t, 1, err.ExitCode)
}

func TestExecutionError_UnwrapsCancellation(t *testing.T) {
	cleanupErr := errors.New("cleanup failed")
	executionErr := &ExecutionError{
		ExitCode: 1,
	}
	err := wrapExecutionError(
		executionErr,
		errors.Join(context.Canceled, cleanupErr),
	)

	var typedErr *ExecutionError
	assert.ErrorAs(t, err, &typedErr)
	assert.ErrorIs(t, err, context.Canceled)
	assert.ErrorIs(t, err, cleanupErr)
}

func TestExecutionError_CauseWrapperPreservesInterrupted(t *testing.T) {
	err := wrapExecutionError(
		&ExecutionError{ExitCode: 130},
		context.Canceled,
	)

	interrupted, ok := err.(interface {
		Interrupted() bool
	})
	assert.True(t, ok)
	assert.True(t, interrupted.Interrupted())
}
