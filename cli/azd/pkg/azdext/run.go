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

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// RunOption configures the behavior of [Run].
type RunOption func(*runConfig)

type runConfig struct {
	preExecute func(ctx context.Context, cmd *cobra.Command) error
}

// WithPreExecute registers a hook that runs after context creation but before
// command execution. If the hook returns a non-nil error, Run prints it and
// exits. This is useful for extensions that need special setup such as
// dual-mode host detection or working-directory changes.
func WithPreExecute(fn func(ctx context.Context, cmd *cobra.Command) error) RunOption {
	return func(c *runConfig) { c.preExecute = fn }
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

	// Validate that extension-defined flags do not collide with azd reserved global flags.
	// This check runs before execution so extension developers see the error immediately.
	if err := ValidateNoReservedFlagConflicts(rootCmd); err != nil {
		if reportErr := ReportError(ctx, err); reportErr != nil {
			log.Printf("warning: failed to report structured error: %v", reportErr)
			printError(err)
		}
		os.Exit(1)
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

// ErrorSuggestion extracts the Suggestion field from structured extension or host gRPC errors.
// Returns an empty string if the error has no suggestion.
func ErrorSuggestion(err error) string {
	if localErr, ok := errors.AsType[*LocalError](err); ok && localErr.Suggestion != "" {
		return localErr.Suggestion
	}

	if svcErr, ok := errors.AsType[*ServiceError](err); ok && svcErr.Suggestion != "" {
		return svcErr.Suggestion
	}

	if actionable := ActionableErrorDetailFromError(err); actionable != nil && actionable.GetSuggestion() != "" {
		return actionable.GetSuggestion()
	}

	return ""
}

// ErrorMessage extracts the user-friendly message from a [LocalError], [ServiceError],
// or a host gRPC error carrying an [ActionableErrorDetail]. Returns "" otherwise.
func ErrorMessage(err error) string {
	if localErr, ok := errors.AsType[*LocalError](err); ok && localErr.Message != "" {
		return localErr.Message
	}

	if svcErr, ok := errors.AsType[*ServiceError](err); ok && svcErr.Message != "" {
		return svcErr.Message
	}

	// Host-originated actionable errors carry the user-facing message in status.Message
	// (ActionableErrorDetail intentionally does not duplicate it). Only return when an
	// ActionableErrorDetail is present so we don't claim every random gRPC error is structured.
	if st, ok := GRPCStatusFromError(err); ok {
		if ActionableErrorDetailFromStatus(st) != nil && st.Message() != "" {
			return st.Message()
		}
	}

	return ""
}

// ErrorLinks extracts the Links field from structured extension or host gRPC errors.
// Returns nil if the error has no links.
func ErrorLinks(err error) []errorhandler.ErrorLink {
	if localErr, ok := errors.AsType[*LocalError](err); ok && len(localErr.Links) > 0 {
		return localErr.Links
	}

	if svcErr, ok := errors.AsType[*ServiceError](err); ok && len(svcErr.Links) > 0 {
		return svcErr.Links
	}

	if actionable := ActionableErrorDetailFromError(err); actionable != nil {
		return UnwrapErrorLinks(actionable.GetLinks())
	}

	return nil
}

// IsStructuredError reports whether err is an azd extension local or service error.
func IsStructuredError(err error) bool {
	_, localErr := errors.AsType[*LocalError](err)
	if localErr {
		return true
	}

	_, svcErr := errors.AsType[*ServiceError](err)
	return svcErr
}
