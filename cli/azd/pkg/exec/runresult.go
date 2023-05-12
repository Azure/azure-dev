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
	stdOut string,
	stdErr string,
	outputAvailable bool) error {
	return &ExitError{
		err:             exitErr,
		stdOut:          stdOut,
		stdErr:          stdErr,
		outputAvailable: outputAvailable,
	}
}

func (e *ExitError) Error() string {
	if e.err.Exited() {
		if !e.outputAvailable {
			return fmt.Sprintf("exit code: %d", e.err.ExitCode())
		}

		return fmt.Sprintf(
			"exit code: %d, stdout: %s, stderr: %s",
			e.err.ExitCode(),
			e.stdOut,
			e.stdErr)
	}

	// for a non-exit-code error (such as a signal), just return the underlying exec.ExitError message
	return e.err.Error()
}
