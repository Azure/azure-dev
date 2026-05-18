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

	azcorelog "github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/spf13/pflag"
)

// setupDebugLogging configures debug logging for the extension.
//
// When debug mode is disabled, the standard Go logger writes to io.Discard and
// the Azure SDK logger is silenced. When enabled, both write into
// `azd-ai-skills-<date>.log` in the current working directory (or stderr if
// the file cannot be opened).
//
// The Azure SDK pipeline is configured with `Logging.IncludeBody: false` for
// every skill request — see `skill_api/client.go` and §11 of the design spec
// for the rationale (request bodies carry user-authored description /
// instructions, and there is no sanitizer in place yet).
//
// Returns a cleanup function that should be deferred by the caller. The
// extension currently discards the cleanup because log writes are unbuffered
// and the OS closes the file on exit; the cleanup is provided so callers that
// care can deterministically close the file.
func setupDebugLogging(flags *pflag.FlagSet) func() {
	if !isDebug(flags) {
		log.SetOutput(io.Discard)
		azcorelog.SetListener(nil)
		return func() {}
	}

	currentDate := time.Now().Format("2006-01-02")
	logFileName := fmt.Sprintf("azd-ai-skills-%s.log", currentDate)

	//nolint:gosec // log file name is generated locally from date and not user-controlled
	logFile, err := os.OpenFile(logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)

	var w io.Writer
	var closeFile func()
	if err != nil {
		w = os.Stderr
		closeFile = func() {}
	} else {
		w = logFile
		closeFile = func() { _ = logFile.Close() }
	}

	log.SetOutput(w)
	azcorelog.SetListener(func(event azcorelog.Event, msg string) {
		// Body logging is disabled in the pipeline, so msg never contains
		// request/response bodies. Even so, never log raw skill content.
		fmt.Fprintf(w, "[%s] %s: %s\n", time.Now().Format(time.RFC3339), event, msg)
	})

	return func() {
		log.SetOutput(io.Discard)
		azcorelog.SetListener(nil)
		closeFile()
	}
}

// isDebug reports whether --debug is set on the command line or
// AZD_EXT_DEBUG is enabled in the environment.
func isDebug(flags *pflag.FlagSet) bool {
	if flags != nil {
		if v, err := flags.GetBool("debug"); err == nil && v {
			return true
		}
	}
	v, _ := strconv.ParseBool(os.Getenv("AZD_EXT_DEBUG"))
	return v
}
