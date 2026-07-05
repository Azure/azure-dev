// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strconv"
	"time"

	azcorelog "github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/spf13/pflag"
)

var connectionStringJSONRegex = regexp.MustCompile(`("[\w]*(?:CONNECTION_STRING|ConnectionString)":\s*)"[^"]*"`)

// setupDebugLogging configures debug logging for the extension.
// By default Go's standard log package writes to stderr, which causes internal
// messages (e.g. from the command runner and GitHub CLI wrapper) to appear as
// noisy user-facing output. This function silences those logs unless debug mode
// is enabled, and additionally configures the Azure SDK logger when debugging.
// Returns a cleanup function that should be deferred by the caller.
func setupDebugLogging(flags *pflag.FlagSet) func() {
	if !isDebug(flags) {
		log.SetOutput(io.Discard)
		azcorelog.SetListener(nil)
		return func() {}
	}

	logFile, err := os.CreateTemp("", "azd-ai-agents-*.log")
	if err == nil {
		logFile.Close()
		logFile, err = os.OpenFile(logFile.Name(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	}

	var w io.Writer
	var closeFile func()
	if err != nil {
		w = os.Stderr
		closeFile = func() {}
	} else {
		w = logFile
		closeFile = func() { logFile.Close() } //nolint:gosec // best-effort cleanup of debug log file
	}

	log.SetOutput(w)
	azcorelog.SetListener(func(event azcorelog.Event, msg string) {
		msg = connectionStringJSONRegex.ReplaceAllString(msg, `${1}"REDACTED"`)
		fmt.Fprintf(w, "[%s] %s: %s\n", time.Now().Format(time.RFC3339), event, msg)
	})

	return func() {
		log.SetOutput(io.Discard)
		azcorelog.SetListener(nil)
		closeFile()
	}
}

// isDebug checks if debug mode is enabled via --debug flag or AZD_EXT_DEBUG environment variable
func isDebug(flags *pflag.FlagSet) bool {
	if debugFlag, err := flags.GetBool("debug"); err == nil && debugFlag {
		return true
	}

	debugEnv := os.Getenv("AZD_EXT_DEBUG")
	if debugEnv == "" {
		return false
	}

	debug, err := strconv.ParseBool(debugEnv)
	return err == nil && debug
}
