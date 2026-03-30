// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

type GlobalCommandOptions struct {
	// Cwd allows the user to override the current working directory, temporarily.
	// The root command will take care of cd'ing into that folder before your command
	// and cd'ing back to the original folder after the commands complete (to make testing
	// easier)
	Cwd string

	// EnableDebugLogging indicates you should turn on verbose/debug logging in your command any
	// launched tools. It's enabled with `--debug`, for any command.
	EnableDebugLogging bool

	// NoPrompt controls non-interactive mode. When true, interactive prompts should behave as
	// if the user selected the default value. If there is no default value the prompt returns
	// an error.
	//
	// Can be enabled via:
	//   - --no-prompt flag
	//   - --non-interactive flag (alias for --no-prompt)
	//   - AZD_NON_INTERACTIVE=true environment variable
	//   - Automatic agent detection (lowest priority)
	NoPrompt bool

	// EnvironmentName holds the value of `-e/--environment` parsed from the command line
	// before Cobra command tree construction. For extension commands (which use
	// DisableFlagParsing), this is the only reliable way to know what `-e` value
	// the user specified. It is empty when the user did not pass `-e` or when the
	// value was not a valid environment name (extensions may reuse `-e` for other
	// purposes such as URLs).
	EnvironmentName string

	// FailOnPrompt when true, any interactive prompt fails immediately with an
	// actionable error, even if a default value exists. This is stricter than
	// NoPrompt which silently uses defaults.
	FailOnPrompt bool

	// EnableTelemetry indicates if telemetry should be sent.
	// The rootCmd will disable this based if the environment variable
	// AZURE_DEV_COLLECT_TELEMETRY is set to 'no'.
	// Defaults to true.
	EnableTelemetry bool

	// Generates platform-agnostic help for use on static documentation sites
	// like learn.microsoft.com. This is set directly when calling NewRootCmd
	// and not bound to any command flags.
	GenerateStaticHelp bool
}
