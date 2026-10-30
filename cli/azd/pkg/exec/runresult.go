// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exec

import (
	"fmt"
	"os/exec"

	"github.com/azure/azure-dev/cli/azd/pkg/exec/internal/process"
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

	cause error
}

// NewExitError creates an ExitError from an os/exec exit failure.
func NewExitError(
	exitErr exec.ExitError,
	cmd string,
	stdOut string,
	stdErr string,
	outputAvailable bool,
) error {
	return newExitError(exitErr, cmd, stdOut, stdErr, outputAvailable, nil)
}

func newExitError(
	exitErr exec.ExitError,
	cmd string,
	stdOut string,
	stdErr string,
	outputAvailable bool,
	cause error,
) *ExitError {
	return &ExitError{
		ExitCode:        exitErr.ExitCode(),
		Cmd:             cmd,
		err:             exitErr,
		stdOut:          stdOut,
		stdErr:          stdErr,
		outputAvailable: outputAvailable,
		cause:           cause,
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

// Interrupted reports whether the process exit represents a Ctrl+C interrupt.
func (e *ExitError) Interrupted() bool {
	if e == nil {
		return false
	}

	return process.IsInterruptExit(&e.err, e.ExitCode)
}

// Unwrap returns additional execution failures, including context cancellation.
func (e *ExitError) Unwrap() error {
	return e.cause
}
