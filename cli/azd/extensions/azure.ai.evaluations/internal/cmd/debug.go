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

// setupDebugLogging silences the standard logger unless debug mode is on.
//
// The data-plane clients trace every request through log.Printf, which Go
// writes to stderr by default. Without this the CLI interleaves raw HTTP traces
// with its own output on every command. Returns a cleanup function the caller
// should defer.
func setupDebugLogging(flags *pflag.FlagSet) func() {
	if !isDebug(flags) {
		log.SetOutput(io.Discard)
		azcorelog.SetListener(nil)
		return func() {}
	}

	logFileName := fmt.Sprintf("azd-ai-eval-%s.log", time.Now().Format("2006-01-02"))

	//nolint:gosec // the name is generated locally from the date, not user input
	logFile, err := os.OpenFile(logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)

	var w io.Writer
	var closeFile func()
	if err != nil {
		w = os.Stderr
		closeFile = func() {}
	} else {
		w = logFile
		closeFile = func() { logFile.Close() } //nolint:gosec // best-effort cleanup
	}

	log.SetOutput(w)
	azcorelog.SetListener(func(event azcorelog.Event, msg string) {
		fmt.Fprintf(w, "[%s] %s: %s\n", time.Now().Format(time.RFC3339), event, msg)
	})

	return func() {
		log.SetOutput(io.Discard)
		azcorelog.SetListener(nil)
		closeFile()
	}
}

// isDebug reports whether --debug or AZD_EXT_DEBUG is set.
func isDebug(flags *pflag.FlagSet) bool {
	if debugFlag, err := flags.GetBool("debug"); err == nil && debugFlag {
		return true
	}
	debug, _ := strconv.ParseBool(os.Getenv("AZD_EXT_DEBUG"))
	return debug
}
