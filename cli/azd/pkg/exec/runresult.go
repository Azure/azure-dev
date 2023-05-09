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
	result       RunResult
	attachOutput bool
	err          exec.ExitError
}

func NewExitError(
	result RunResult,
	exitErr exec.ExitError,
	outputAvailable bool) error {
	return &ExitError{
		result:       result,
		err:          exitErr,
		attachOutput: outputAvailable,
	}
}

func (e *ExitError) Error() string {
	if e.err.Exited() {
		if !e.attachOutput {
			return fmt.Sprintf("exit code: %d", e.result.ExitCode)
		}

		return fmt.Sprintf(
			"exit code: %d, stdout: %s, stderr: %s",
			e.result.ExitCode,
			e.result.Stdout,
			e.result.Stderr)
	}

	// for a non-exit-code error (such as a signal), just return the underlying exec.ExitError message
	return e.err.Error()
}
