// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// RunOption configures the behavior of [Run].
type RunOption func(*runConfig)

type runConfig struct {
	preExecute   func(ctx context.Context, cmd *cobra.Command) error
	exitCodeFunc func(err error) (int, bool)
}

// WithPreExecute registers a hook that runs after context creation but before
// command execution. If the hook returns a non-nil error, Run prints it and
// exits. This is useful for extensions that need special setup such as
// dual-mode host detection or working-directory changes.
func WithPreExecute(fn func(ctx context.Context, cmd *cobra.Command) error) RunOption {
	return func(c *runConfig) { c.preExecute = fn }
}

// WithExitCode registers a function that extracts an exit code from an error.
// If the function returns (code, true), Run exits with that code instead of
// the default 1. This is useful for extensions that propagate child process
// exit codes (e.g., a script runner that should exit with the script's code).
//
// The returned exit code must be non-zero; returning (0, true) is treated as
// unmatched (falls through to the default exit(1)) because exit code 0 would
// mask a real error.
func WithExitCode(fn func(err error) (int, bool)) RunOption {
	return func(c *runConfig) { c.exitCodeFunc = fn }
}

// Run is the standard entry point for azd extensions. It handles all lifecycle
// boilerplate that every extension needs:
//   - FORCE_COLOR environment variable → color.NoColor
//   - cobra SilenceErrors (Run controls error output)
//   - Context creation with tracing propagation
//   - gRPC access token injection via [WithAccessToken]
//   - Command execution
//   - Structured error reporting via gRPC ReportError
//   - Error + suggestion display
//   - os.Exit on failure
//
// A typical extension main.go becomes:
//
//	func main() {
//	    azdext.Run(cmd.NewRootCommand())
//	}
func Run(rootCmd *cobra.Command, opts ...RunOption) {
	if v, ok := os.LookupEnv("FORCE_COLOR"); ok && v == "1" {
		color.NoColor = false
	}

	rootCmd.SilenceErrors = true

	ctx := NewContext()
	ctx = WithAccessToken(ctx)

	var cfg runConfig
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.preExecute != nil {
		if err := cfg.preExecute(ctx, rootCmd); err != nil {
			if reportErr := ReportError(ctx, err); reportErr != nil {
				log.Printf("warning: failed to report structured error: %v", reportErr)
				printError(err)
			}

			os.Exit(1)
		}
	}

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		if reportErr := ReportError(ctx, err); reportErr != nil {
			log.Printf("warning: failed to report structured error: %v", reportErr)
			printError(err)
		}

		if cfg.exitCodeFunc != nil {
			if code, ok := cfg.exitCodeFunc(err); ok && code != 0 {
				os.Exit(code)
			}
		}

		os.Exit(1)
	}
}

func printError(err error) {
	redStderr := color.New(color.FgRed)
	redStderr.EnableColor()
	redStderr.Fprintf(os.Stderr, "Error: %v\n", err)

	if s := ErrorSuggestion(err); s != "" {
		if !strings.HasPrefix(s, "Suggestion: ") {
			s = "Suggestion: " + s
		}
		fmt.Fprintln(os.Stderr, s)
	}
}

// ErrorSuggestion extracts the Suggestion field from a [LocalError] or [ServiceError].
// Returns an empty string if the error has no suggestion.
func ErrorSuggestion(err error) string {
	if localErr, ok := errors.AsType[*LocalError](err); ok && localErr.Suggestion != "" {
		return localErr.Suggestion
	}

	if svcErr, ok := errors.AsType[*ServiceError](err); ok && svcErr.Suggestion != "" {
		return svcErr.Suggestion
	}

	return ""
}

// ErrorMessage extracts the user-friendly Message field from a [LocalError] or [ServiceError].
// Returns an empty string if the error is not an extension error type.
func ErrorMessage(err error) string {
	if localErr, ok := errors.AsType[*LocalError](err); ok && localErr.Message != "" {
		return localErr.Message
	}

	if svcErr, ok := errors.AsType[*ServiceError](err); ok && svcErr.Message != "" {
		return svcErr.Message
	}

	return ""
}
