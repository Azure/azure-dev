// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestRunningOnCodespacesBrowser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		stdout   string
		runErr   error
		expected bool
	}{
		{
			name:     "browser_environment_detected",
			stdout:   "The --status argument is not yet supported in browsers",
			expected: true,
		},
		{
			name:     "desktop_environment",
			stdout:   "Version: 1.85.0\nCommit: abc123",
			expected: false,
		},
		{
			name:     "empty_output",
			stdout:   "",
			expected: false,
		},
		{
			name:     "command_fails",
			runErr:   errors.New("code: command not found"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mockContext := mocks.NewMockContext(context.Background())

			if tt.runErr != nil {
				mockContext.CommandRunner.When(func(args exec.RunArgs, commandName string) bool {
					return args.Cmd == "code"
				}).SetError(tt.runErr)
			} else {
				mockContext.CommandRunner.When(func(args exec.RunArgs, commandName string) bool {
					return args.Cmd == "code"
				}).Respond(exec.RunResult{
					Stdout: tt.stdout,
				})
			}

			result := runningOnCodespacesBrowser(t.Context(), mockContext.CommandRunner)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestParseUseDeviceCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		flagPtr     *string
		expected    bool
		expectError bool
	}{{
		name:     "flag_true",
		flagPtr:  new("true"),
		expected: true,
	},
		{
			name:     "flag_false",
			flagPtr:  new("false"),
			expected: false,
		},
		{
			name:        "flag_invalid",
			flagPtr:     new("notabool"),
			expectError: true,
		},
		{
			name:     "flag_not_set",
			flagPtr:  nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mockContext := mocks.NewMockContext(context.Background())

			// Mock code --status to return non-browser env
			mockContext.CommandRunner.When(func(args exec.RunArgs, commandName string) bool {
				return args.Cmd == "code"
			}).Respond(exec.RunResult{
				Stdout: "Version: 1.85.0",
			})

			flag := boolPtr{ptr: tt.flagPtr}
			result, err := parseUseDeviceCode(t.Context(), flag, mockContext.CommandRunner)

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), "unexpected boolean input")
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}
