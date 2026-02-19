// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exec

import (
	"fmt"
	"os/exec"
)

// RunResult is the result of running a command.
type RunResult struct {
	// The exit code of the command.
	ExitCode int
	// The stdout output captured from running the command.
	Stdout string
	// The stderr output captured from running the command.
	Stderr string
}

func NewRunResult(code int, stdout, stderr string) RunResult {
	return RunResult{
		ExitCode: code,
		Stdout:   stdout,
		Stderr:   stderr,
	}
}

// ExitError is the error returned when a command unsuccessfully exits.
type ExitError struct {
	// The path or name of the command being invoked.
	Cmd string
	// The exit code of the command.
	ExitCode int

	stdOut string
	stdErr string

	outputAvailable bool

	// The underlying exec.ExitError.
	err exec.ExitError
}

func NewExitError(
	exitErr exec.ExitError,
	cmd string,
	stdOut string,
	stdErr string,
	outputAvailable bool) error {
	return &ExitError{
		ExitCode:        exitErr.ExitCode(),
		Cmd:             cmd,
		err:             exitErr,
		stdOut:          stdOut,
		stdErr:          stdErr,
		outputAvailable: outputAvailable,
	}
}

// Error augments the underlying exec.ExitError's Error with the stdout and stderr output of the command, if available.
func (e *ExitError) Error() string {
	var errorPrefix string

	// Handle the case where the underlying error represents an exit code error. In this case we'd rather use "exit code"
	// and not "exit status" as the error message, to make it easier to find in logs.
	if e.err.Exited() {
		errorPrefix = fmt.Sprintf("exit code: %d", e.err.ExitCode())
	} else {
		errorPrefix = e.err.Error()
	}

	if !e.outputAvailable {
		return errorPrefix
	}

	return fmt.Sprintf("%s, stdout: %s, stderr: %s", errorPrefix, e.stdOut, e.stdErr)
}

// StderrOutput returns the stderr output captured from the command.
func (e *ExitError) StderrOutput() string {
	return e.stdErr
}

// NewTestExitError creates an ExitError suitable for unit tests
// where constructing an os/exec.ExitError is impractical.
func NewTestExitError(cmd string, exitCode int, stderr string) *ExitError {
	return &ExitError{
		Cmd:             cmd,
		ExitCode:        exitCode,
		stdErr:          stderr,
		outputAvailable: true,
	}
}
