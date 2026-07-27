// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
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
			mockContext := mocks.NewMockContext(t.Context())

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
			mockContext := mocks.NewMockContext(t.Context())

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

func Test_NewAuthLoginAction_Constructor(t *testing.T) {
	t.Parallel()
	formatter := &output.JsonFormatter{}
	console := mockinput.NewMockConsole()
	annotations := CmdAnnotations{"key": "value"}
	a := newAuthLoginAction(
		formatter, io.Discard, nil, nil,
		&authLoginFlags{}, console, annotations, nil,
	)
	la := a.(*loginAction)
	require.NotNil(t, la.flags)
	require.Equal(t, annotations, la.annotations)
}

func Test_AlphaFeatureManager_WithConfig(t *testing.T) {
	t.Parallel()
	cfg := config.NewEmptyConfig()
	fm := alpha.NewFeaturesManagerWithConfig(cfg)
	require.NotNil(t, fm)
}

func Test_ProjectConfig_Basic(t *testing.T) {
	t.Parallel()
	cfg := &project.ProjectConfig{Name: "test"}
	require.Equal(t, "test", cfg.Name)
}

func Test_NewAuthLoginAction(t *testing.T) {
	t.Parallel()
	action := newAuthLoginAction(
		&output.JsonFormatter{},
		&bytes.Buffer{},
		nil, // authManager
		nil, // accountSubManager
		&authLoginFlags{},
		mockinput.NewMockConsole(),
		CmdAnnotations{},
		nil, // commandRunner
	)
	require.NotNil(t, action)
}

func Test_NewAuthLoginFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newAuthLoginFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_StringPtr_SetAndString(t *testing.T) {
	t.Parallel()
	var sp stringPtr

	// Before set, String() returns ""
	assert.Equal(t, "", sp.String())
	assert.Equal(t, "string", sp.Type())

	// After set
	err := sp.Set("hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", sp.String())

	// Set empty string
	err = sp.Set("")
	require.NoError(t, err)
	assert.Equal(t, "", sp.String())
}

func Test_BoolPtr_SetAndString(t *testing.T) {
	t.Parallel()
	var bp boolPtr

	// Before set returns "false"
	assert.Equal(t, "false", bp.String())
	assert.Equal(t, "", bp.Type())

	// After set
	err := bp.Set("true")
	require.NoError(t, err)
	assert.Equal(t, "true", bp.String())
}
