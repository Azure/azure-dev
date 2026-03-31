// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package executor

import (
	"errors"
	"strings"
	"testing"
)

func TestValidationError(t *testing.T) {
	err := &ValidationError{
		Field:  "scriptPath",
		Reason: "cannot be empty",
	}

	expected := "validation error for scriptPath: cannot be empty"
	if err.Error() != expected {
		t.Errorf("ValidationError.Error() = %q, want %q", err.Error(), expected)
	}
}

func TestScriptNotFoundError(t *testing.T) {
	err := &ScriptNotFoundError{
		Path: "test.sh",
	}

	expected := "script not found: test.sh"
	if err.Error() != expected {
		t.Errorf("ScriptNotFoundError.Error() = %q, want %q", err.Error(), expected)
	}
}

func TestInvalidShellError(t *testing.T) {
	err := &InvalidShellError{
		Shell: "invalid",
	}

	msg := err.Error()
	if !strings.Contains(msg, "invalid shell: invalid") {
		t.Errorf("InvalidShellError.Error() should contain 'invalid shell: invalid', got %q", msg)
	}
	if !strings.Contains(msg, "bash") {
		t.Errorf("InvalidShellError.Error() should list valid shells, got %q", msg)
	}
}

func TestExecutionError(t *testing.T) {
	tests := []struct {
		name     string
		err      *ExecutionError
		wantText string
	}{
		{
			name: "Inline script error",
			err: &ExecutionError{
				ExitCode: 1,
				Shell:    "bash",
				IsInline: true,
			},
			wantText: "inline script exited with code 1 (shell: bash)",
		},
		{
			name: "File script error",
			err: &ExecutionError{
				ExitCode: 127,
				Shell:    "pwsh",
				IsInline: false,
			},
			wantText: "script exited with code 127 (shell: pwsh)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.wantText {
				t.Errorf("ExecutionError.Error() = %q, want %q", tt.err.Error(), tt.wantText)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{name: "Valid config - empty shell", config: Config{Shell: ""}, wantErr: false},
		{name: "Valid config - bash", config: Config{Shell: "bash"}, wantErr: false},
		{name: "Valid config - pwsh", config: Config{Shell: "pwsh"}, wantErr: false},
		{name: "Invalid shell", config: Config{Shell: "invalid"}, wantErr: true},
		{name: "Valid shell with uppercase", config: Config{Shell: "BASH"}, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil {
				if _, ok := errors.AsType[*InvalidShellError](err); !ok {
					t.Errorf("Config.Validate() should return *InvalidShellError, got %T", err)
				}
			}
		})
	}
}
