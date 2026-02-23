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
//   - Command execution
//   - Structured error reporting via AZD_ERROR_FILE
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

	var cfg runConfig
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.preExecute != nil {
		if err := cfg.preExecute(ctx, rootCmd); err != nil {
			if reportErr := ReportError(err); reportErr != nil {
				log.Printf("warning: failed to report structured error: %v", reportErr)
			}
			printError(err)
			os.Exit(1)
		}
	}

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		if reportErr := ReportError(err); reportErr != nil {
			log.Printf("warning: failed to report structured error: %v", reportErr)
		}

		printError(err)
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
	var localErr *LocalError
	if errors.As(err, &localErr) && localErr.Suggestion != "" {
		return localErr.Suggestion
	}

	var svcErr *ServiceError
	if errors.As(err, &svcErr) && svcErr.Suggestion != "" {
		return svcErr.Suggestion
	}

	return ""
}
