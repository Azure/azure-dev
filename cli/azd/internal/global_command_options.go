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

	// when true, interactive prompts should behave as if the user selected the default value.
	// if there is no default value the prompt returns an error.
	NoPrompt bool

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
