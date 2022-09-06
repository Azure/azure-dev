package exec

import "fmt"

type RunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

func (rr RunResult) String() string {
	return fmt.Sprintf("exit code: %d, stdout: %s, stderr: %s", rr.ExitCode, rr.Stdout, rr.Stderr)
}

func NewRunResult(code int, stdout, stderr string) RunResult {
	return RunResult{
		ExitCode: code,
		Stdout:   stdout,
		Stderr:   stderr,
	}
}
