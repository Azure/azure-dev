package commands

import (
	"context"
)

type GlobalCommandOptions struct {
	EnvironmentName string

	// Cwd allows the user to override the current working directory, temporarily.
	// The root command will take care of cd'ing into that folder before your command
	// and cd'ing back to the original folder after the commands complete (to make testing
	// easier)
	Cwd string

	// EnableDebugLogging indicates you should turn on verbose/debug logging in your command any
	// launched tools. It's enabled with `--debug`, for any command.
	EnableDebugLogging bool

	// when true, interactive prompts should behave as if the user selected the default value.
	// if there is no default value the prompt returns an error.
	NoPrompt bool

	// EnableTelemetry indicates if telemetry should be sent.
	// The rootCmd will disable this based if the environment variable
	// AZURE_DEV_COLLECT_TELEMETRY is set to 'no'.
	// Defaults to true.
	EnableTelemetry bool
}

type contextKey string

const (
	optionsContextKey contextKey = "options"
)

func WithGlobalCommandOptions(ctx context.Context, options *GlobalCommandOptions) context.Context {
	return context.WithValue(ctx, optionsContextKey, options)
}

func GlobalCommandOptionsFromContext(ctx context.Context) *GlobalCommandOptions {
	options, ok := ctx.Value(optionsContextKey).(*GlobalCommandOptions)
	if !ok {
		panic("GlobalCommandOptions were not found in the context")
	}
	return options
}
