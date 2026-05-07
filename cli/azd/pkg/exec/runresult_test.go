// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exec

import (
	osexec "os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// makeOSExitError runs a command that exits with a non-zero code and returns the resulting exec.ExitError.
// "go --help" exits with code 2 on all platforms.
func makeOSExitError(t *testing.T) osexec.ExitError {
	t.Helper()
	cmd := osexec.CommandContext(t.Context(), "go", "--help") //nolint:gosec // hardcoded test args
	err := cmd.Run()
	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return *exitErr
}

func TestNewRunResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		code   int
		stdout string
		stderr string
	}{
		{
			name:   "SuccessWithOutput",
			code:   0,
			stdout: "hello world",
			stderr: "",
		},
		{
			name:   "NonZeroExitCode",
			code:   1,
			stdout: "",
			stderr: "error occurred",
		},
		{
			name:   "AllFieldsEmpty",
			code:   0,
			stdout: "",
			stderr: "",
		},
		{
			name:   "BothStdoutAndStderr",
			code:   42,
			stdout: "some output",
			stderr: "some error",
		},
		{
			name:   "NegativeExitCode",
			code:   -1,
			stdout: "",
			stderr: "",
		},
		{
			name:   "MultilineOutput",
			code:   0,
			stdout: "line1\nline2\nline3",
			stderr: "err1\nerr2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := NewRunResult(tt.code, tt.stdout, tt.stderr)
			require.Equal(t, tt.code, result.ExitCode)
			require.Equal(t, tt.stdout, result.Stdout)
			require.Equal(t, tt.stderr, result.Stderr)
		})
	}
}

func TestNewExitError(t *testing.T) {
	t.Parallel()

	osExitErr := makeOSExitError(t)

	tests := []struct {
		name            string
		cmd             string
		stdOut          string
		stdErr          string
		outputAvailable bool
	}{
		{
			name:            "WithOutputAvailable",
			cmd:             "mycli",
			stdOut:          "standard output",
			stdErr:          "error output",
			outputAvailable: true,
		},
		{
			name:            "WithoutOutputAvailable",
			cmd:             "mycli",
			stdOut:          "",
			stdErr:          "",
			outputAvailable: false,
		},
		{
			name:            "OnlyStdout",
			cmd:             "anothercli",
			stdOut:          "some output",
			stdErr:          "",
			outputAvailable: true,
		},
		{
			name:            "OnlyStderr",
			cmd:             "anothercli",
			stdOut:          "",
			stdErr:          "some error",
			outputAvailable: true,
		},
		{
			name:            "EmptyCmd",
			cmd:             "",
			stdOut:          "out",
			stdErr:          "err",
			outputAvailable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := NewExitError(osExitErr, tt.cmd, tt.stdOut, tt.stdErr, tt.outputAvailable)
			require.Error(t, err)

			var typedErr *ExitError
			require.ErrorAs(t, err, &typedErr)
			require.Equal(t, tt.cmd, typedErr.Cmd)
			require.Equal(t, osExitErr.ExitCode(), typedErr.ExitCode)
		})
	}
}

func TestExitError_Error(t *testing.T) {
	t.Parallel()

	osExitErr := makeOSExitError(t)

	tests := []struct {
		name            string
		stdOut          string
		stdErr          string
		outputAvailable bool
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:            "OutputAvailableIncludesStdoutStderr",
			stdOut:          "my stdout",
			stdErr:          "my stderr",
			outputAvailable: true,
			wantContains:    []string{"exit code:", "stdout: my stdout", "stderr: my stderr"},
		},
		{
			name:            "OutputNotAvailableExcludesStdoutStderr",
			stdOut:          "my stdout",
			stdErr:          "my stderr",
			outputAvailable: false,
			wantContains:    []string{"exit code:"},
			wantNotContains: []string{"stdout:", "stderr:"},
		},
		{
			name:            "EmptyOutputFields",
			stdOut:          "",
			stdErr:          "",
			outputAvailable: true,
			wantContains:    []string{"exit code:", "stdout: ", "stderr: "},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := NewExitError(osExitErr, "testcmd", tt.stdOut, tt.stdErr, tt.outputAvailable)
			errMsg := err.Error()

			for _, want := range tt.wantContains {
				require.Contains(t, errMsg, want)
			}
			for _, notWant := range tt.wantNotContains {
				require.NotContains(t, errMsg, notWant)
			}
		})
	}
}

func TestExitError_ErrorContainsExitCode(t *testing.T) {
	t.Parallel()

	osExitErr := makeOSExitError(t)
	err := NewExitError(osExitErr, "go", "out", "err", true)

	// go --help exits with code 2
	require.ErrorContains(t, err, "exit code: 2")
	require.ErrorContains(t, err, "stdout: out")
	require.ErrorContains(t, err, "stderr: err")
}

func TestExitError_SatisfiesErrorInterface(t *testing.T) {
	t.Parallel()

	osExitErr := makeOSExitError(t)
	err := NewExitError(osExitErr, "go", "", "", false)

	// NewExitError returns the error interface; confirm non-nil and type-assertable.
	require.Error(t, err)

	var typedErr *ExitError
	require.ErrorAs(t, err, &typedErr)

	// The error string is non-empty even without output.
	require.NotEmpty(t, typedErr.Error())
}
