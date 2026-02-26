// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/propagation"
)

// ExtensionContext holds parsed global state available to extension commands.
type ExtensionContext struct {
	Debug        bool
	NoPrompt     bool
	Cwd          string
	Environment  string
	OutputFormat string

	ctx context.Context
}

// Context returns the prepared context with tracing and access token metadata.
func (ec *ExtensionContext) Context() context.Context {
	if ec.ctx != nil {
		return ec.ctx
	}
	return context.Background()
}

// ExtensionCommandOptions configures the extension root command.
type ExtensionCommandOptions struct {
	// Name is the extension name (used in command Use field)
	Name string
	// Version is the extension version
	Version string
	// Use overrides the default Use string (defaults to Name)
	Use string
	// Short is a short description
	Short string
	// Long is a long description
	Long string
}

// NewExtensionRootCommand creates a root cobra.Command pre-configured for azd extensions.
// It automatically:
//   - Registers azd's global flags (--debug, --no-prompt, --cwd, -e/--environment, --output)
//   - Reads AZD_* environment variables set by the azd framework
//   - Sets up OpenTelemetry trace context from TRACEPARENT/TRACESTATE env vars
//   - Calls WithAccessToken() on the command context
//
// The returned command has PersistentPreRunE configured to set up the ExtensionContext.
func NewExtensionRootCommand(opts ExtensionCommandOptions) (*cobra.Command, *ExtensionContext) {
	extCtx := &ExtensionContext{}

	use := opts.Use
	if use == "" {
		use = opts.Name
	}

	cmd := &cobra.Command{
		Use:     use,
		Short:   opts.Short,
		Long:    opts.Long,
		Version: opts.Version,
	}

	// Register persistent flags
	flags := cmd.PersistentFlags()
	flags.BoolVar(&extCtx.Debug, "debug", false, "Enables debug and diagnostics logging")
	flags.BoolVar(&extCtx.NoPrompt, "no-prompt", false, "Accepts the default value instead of prompting")
	flags.StringVarP(&extCtx.Cwd, "cwd", "C", "", "Sets the current working directory")
	flags.StringVarP(&extCtx.Environment, "environment", "e", "", "The name of the environment to use")
	flags.StringVarP(&extCtx.OutputFormat, "output", "o", "default", "The output format")

	// Hidden trace flags
	var traceLogFile, traceLogURL string
	flags.StringVar(&traceLogFile, "trace-log-file", "", "Write raw OpenTelemetry trace data to a file")
	flags.StringVar(&traceLogURL, "trace-log-url", "", "Send raw OpenTelemetry trace data to a URL")
	_ = flags.MarkHidden("trace-log-file")
	_ = flags.MarkHidden("trace-log-url")

	cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// Env-var fallback for flags not explicitly set
		if !cmd.Flags().Changed("debug") {
			if v := os.Getenv("AZD_DEBUG"); v != "" {
				if b, err := strconv.ParseBool(v); err == nil {
					extCtx.Debug = b
				}
			}
		}

		if !cmd.Flags().Changed("no-prompt") {
			if v := os.Getenv("AZD_NO_PROMPT"); v != "" {
				if b, err := strconv.ParseBool(v); err == nil {
					extCtx.NoPrompt = b
				}
			}
		}

		if !cmd.Flags().Changed("cwd") {
			if v := os.Getenv("AZD_CWD"); v != "" {
				extCtx.Cwd = v
			}
		}

		if !cmd.Flags().Changed("environment") {
			if v := os.Getenv("AZD_ENVIRONMENT"); v != "" {
				extCtx.Environment = v
			}
		}

		// Change working directory if specified.
		// This mirrors azd's own --cwd flag behavior. The value comes from the
		// trusted --cwd flag or AZD_CWD env var set by the azd framework.
		if extCtx.Cwd != "" {
			absPath, err := filepath.Abs(extCtx.Cwd)
			if err != nil {
				return fmt.Errorf("invalid working directory %q: %w", extCtx.Cwd, err)
			}
			if err := os.Chdir(absPath); err != nil {
				return err
			}
		}

		// Extract OTel trace context from environment
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		if parent := os.Getenv(TraceparentEnv); parent != "" {
			tc := propagation.TraceContext{}
			ctx = tc.Extract(ctx, propagation.MapCarrier{
				TraceparentKey: parent,
				TracestateKey:  os.Getenv(TracestateEnv),
			})
		}

		// Inject gRPC access token
		ctx = WithAccessToken(ctx)

		extCtx.ctx = ctx
		cmd.SetContext(ctx)

		return nil
	}

	return cmd, extCtx
}
