// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"io"
	"strings"

	"github.com/spf13/pflag"
)

// NewGlobalFlagSet creates a new flag set with all global flags defined.
// cmd.CreateGlobalFlagSet delegates here so packages that cannot import cmd
// (such as the command middleware) share the same definitions.
//
// When adding or modifying flags here, also update the reserved flag registry
// in reserved_flags.go.
func NewGlobalFlagSet() *pflag.FlagSet {
	globalFlags := pflag.NewFlagSet("global", pflag.ContinueOnError)

	globalFlags.StringP("cwd", "C", "", "Sets the current working directory.")
	globalFlags.Bool("debug", false, "Enables debugging and diagnostics logging.")
	globalFlags.Bool(
		"no-prompt",
		false,
		"Runs without prompts. Uses existing values; "+
			"fails if any required value or decision cannot be resolved automatically. "+
			"Automatically enabled when azd detects a CI/CD or AI-agent environment; "+
			"set AZD_NON_INTERACTIVE=false to opt out of that automatic enablement.")
	globalFlags.Bool(
		"non-interactive",
		false,
		"Alias for --no-prompt.")
	_ = globalFlags.MarkHidden("non-interactive")
	globalFlags.StringP(EnvironmentNameFlagName, "e", "", "The name of the environment to use.")

	// The telemetry system is responsible for reading these flags value and using it to configure the telemetry
	// system, but we still need to add it to our flag set so that when we parse the command line with Cobra we
	// don't error due to an "unknown flag".
	globalFlags.String("trace-log-file", "", "Write a diagnostics trace to a file.")
	_ = globalFlags.MarkHidden("trace-log-file")

	globalFlags.String("trace-log-url", "", "Send traces to an Open Telemetry compatible endpoint.")
	_ = globalFlags.MarkHidden("trace-log-url")

	return globalFlags
}

// StripGlobalFlags removes recognized azd flags and their values.
// It preserves the tail starting at "--" or an unrecognized or
// invalid flag, so extension arguments are not guessed.
func StripGlobalFlags(args []string) []string {
	flags := NewGlobalFlagSet()
	flags.SetOutput(io.Discard)
	flags.SetInterspersed(false)
	flags.StringP("output", "o", "", "")
	flags.BoolP("help", "h", false, "")
	flags.Bool("docs", false, "")
	result := make([]string, 0, len(args))
	for len(args) > 0 {
		if !strings.HasPrefix(args[0], "-") || args[0] == "-" {
			result = append(result, args[0])
			args = args[1:]
			continue
		}
		if err := flags.Parse(args); err != nil {
			return append(result, args...)
		}
		if flags.ArgsLenAtDash() >= 0 {
			result = append(result, "--")
			return append(result, flags.Args()...)
		}
		args = flags.Args()
	}
	return result
}
