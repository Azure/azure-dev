// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/spf13/pflag"
)

// setupDebugLogging configures debug logging for the extension.
//
// Go's standard log package writes to stderr by default, which would surface
// the inspector's verbose proxy/SSE traffic logs as noisy user-facing output.
// In normal use we redirect log output to io.Discard; with --debug (or
// AZD_EXT_DEBUG=true) we redirect it to a per-day log file in the current
// working directory. The returned function closes any opened log file.
func setupDebugLogging(flags *pflag.FlagSet) func() error {
	if !isDebug(flags) {
		log.SetOutput(io.Discard)
		return nil
	}

	currentDate := time.Now().Format("2006-01-02")
	logFileName := fmt.Sprintf("azd-ai-inspector-%s.log", currentDate)

	//nolint:gosec // log file name is generated locally from date and not user-controlled
	logFile, err := os.OpenFile(logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		log.SetOutput(os.Stderr)
		return nil
	}

	log.SetOutput(logFile)
	return func() error {
		log.SetOutput(io.Discard)
		return logFile.Close()
	}
}

// isDebug checks if debug mode is enabled via --debug flag or
// AZD_EXT_DEBUG environment variable.
func isDebug(flags *pflag.FlagSet) bool {
	if debugFlag, err := flags.GetBool("debug"); err == nil && debugFlag {
		return true
	}

	debug, _ := strconv.ParseBool(os.Getenv("AZD_EXT_DEBUG"))
	return debug
}
