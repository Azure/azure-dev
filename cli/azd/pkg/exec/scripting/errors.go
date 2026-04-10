// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scripting

import "fmt"

// ValidationError indicates that input validation failed.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error for %s: %s", e.Field, e.Reason)
}

// ScriptNotFoundError indicates that a script file could not be found.
type ScriptNotFoundError struct {
	Path string
}

func (e *ScriptNotFoundError) Error() string {
	return fmt.Sprintf("script not found: %s", e.Path)
}

// InvalidShellError indicates that an invalid shell was specified.
type InvalidShellError struct {
	Shell string
}

func (e *InvalidShellError) Error() string {
	return fmt.Sprintf(
		"invalid shell: %s (valid: bash, sh, zsh, pwsh, powershell, cmd)", e.Shell)
}

// ExecutionError indicates that script execution failed with a specific exit code.
type ExecutionError struct {
	ExitCode int
	Shell    string
	IsInline bool
}

func (e *ExecutionError) Error() string {
	if e.Shell == "" {
		return fmt.Sprintf("command exited with code %d", e.ExitCode)
	}
	if e.IsInline {
		return fmt.Sprintf(
			"inline script exited with code %d (shell: %s)", e.ExitCode, e.Shell)
	}
	return fmt.Sprintf(
		"script exited with code %d (shell: %s)", e.ExitCode, e.Shell)
}