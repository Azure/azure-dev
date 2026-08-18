// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	azcorelog "github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/spf13/pflag"
)

// setupDebugLogging silences the standard logger unless debug mode is on.
//
// The data-plane clients trace every request through log.Printf, which Go
// writes to stderr by default. Without this the CLI interleaves raw HTTP traces
// with its own output on every command.
//
// The returned function puts the logger back and closes the file. Callers run
// for the length of the process and let the OS close it, so it is returned for
// tests and for any caller that wants to stop logging early.
func setupDebugLogging(flags *pflag.FlagSet) func() {
	if !isDebug(flags) {
		log.SetOutput(io.Discard)
		azcorelog.SetListener(nil)
		return func() {}
	}

	// Written outside the working directory: that is the user's repository, the
	// scaffolded .gitignore does not cover this name, and a routine `git add -A`
	// committed one.
	//
	// The name is picked by CreateTemp rather than built from the date alone.
	// The temp directory is shared on Linux, and at a predictable path another
	// user can leave a file of their own -- readable, to collect HTTP traces
	// that carry request headers, or a symbolic link, to have them written to a
	// file of their choosing. CreateTemp finds an unused name and creates it
	// 0600 in one step, so neither is reachable. The date stays in the name
	// because it is what makes a directory of these readable, and the full path
	// is echoed below.
	logFile, err := os.CreateTemp("", fmt.Sprintf("azd-ai-eval-%s-*.log", time.Now().Format("2006-01-02")))

	var w io.Writer
	var closeFile func()
	if err != nil {
		w = os.Stderr
		closeFile = func() {}
	} else {
		w = logFile
		closeFile = func() { _ = logFile.Close() }
		// A log nobody can find is not a log. Debugging was asked for
		// explicitly, so naming the file costs nothing.
		fmt.Fprintf(os.Stderr, "Debug log: %s\n", filepath.ToSlash(logFile.Name()))
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
