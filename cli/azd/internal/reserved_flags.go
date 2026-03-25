// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

// ReservedFlag describes a global flag that is owned by azd and must not be reused
// by extensions for a different purpose. Extensions that register a flag with the
// same short or long name will shadow the global flag, causing unpredictable behavior
// when azd tries to parse the command line before dispatching to the extension.
type ReservedFlag struct {
	// Long is the full flag name (e.g. "environment"). Always present.
	Long string
	// Short is the single-character alias (e.g. "e"). Empty when there is no short form.
	Short string
	// Description explains the flag's purpose in azd.
	Description string
}

// ReservedFlags is the canonical list of global flags that extensions must not reuse.
// It is derived from CreateGlobalFlagSet (auto_install.go), the root command's
// persistent flags, and the extension SDK's built-in flag set (extension_command.go).
//
// Keep this list in sync whenever a new global flag is added to azd.
var ReservedFlags = []ReservedFlag{
	{Long: "environment", Short: "e", Description: "The name of the environment to use."},
	{Long: "cwd", Short: "C", Description: "Sets the current working directory."},
	{Long: "debug", Short: "", Description: "Enables debugging and diagnostics logging."},
	{Long: "no-prompt", Short: "", Description: "Accepts the default value instead of prompting."},
	{Long: "output", Short: "o", Description: "The output format (json, table, none)."},
	{Long: "help", Short: "h", Description: "Help for the current command."},
	{Long: "docs", Short: "", Description: "Opens the documentation for the current command."},
	{Long: "trace-log-file", Short: "", Description: "Write a diagnostics trace to a file."},
	{Long: "trace-log-url", Short: "", Description: "Send traces to an Open Telemetry compatible endpoint."},
}

// reservedShortFlags is an index of short flag names built once at init time.
var reservedShortFlags map[string]ReservedFlag

// reservedLongFlags is an index of long flag names built once at init time.
var reservedLongFlags map[string]ReservedFlag

func init() {
	reservedShortFlags = make(map[string]ReservedFlag, len(ReservedFlags))
	reservedLongFlags = make(map[string]ReservedFlag, len(ReservedFlags))
	for _, f := range ReservedFlags {
		reservedLongFlags[f.Long] = f
		if f.Short != "" {
			reservedShortFlags[f.Short] = f
		}
	}
}

// IsReservedShortFlag returns true when the given single-character flag name
// (without the leading "-") is reserved by azd as a global flag.
func IsReservedShortFlag(short string) bool {
	_, ok := reservedShortFlags[short]
	return ok
}

// IsReservedLongFlag returns true when the given long flag name
// (without the leading "--") is reserved by azd as a global flag.
func IsReservedLongFlag(long string) bool {
	_, ok := reservedLongFlags[long]
	return ok
}

// GetReservedShortFlag returns the ReservedFlag for the given short name, if any.
func GetReservedShortFlag(short string) (ReservedFlag, bool) {
	f, ok := reservedShortFlags[short]
	return f, ok
}

// GetReservedLongFlag returns the ReservedFlag for the given long name, if any.
func GetReservedLongFlag(long string) (ReservedFlag, bool) {
	f, ok := reservedLongFlags[long]
	return f, ok
}
