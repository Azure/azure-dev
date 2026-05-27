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

// setupDebugLogging routes the standard logger and the Azure SDK logger to a
// per-day file when --debug is set, and to io.Discard otherwise. The SDK
// pipeline runs with IncludeBody=false so request/response bodies (which can
// carry user-authored description / instructions) never reach the log.
func setupDebugLogging(flags *pflag.FlagSet) func() {
	if !isDebug(flags) {
		log.SetOutput(io.Discard)
		azcorelog.SetListener(nil)
		return func() {}
	}

	logFileName := fmt.Sprintf("azd-ai-skills-%s.log", time.Now().Format("2006-01-02"))

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
		fmt.Fprintf(w, "[%s] %s: %s\n", time.Now().Format(time.RFC3339), event, msg)
	})

	return func() {
		log.SetOutput(io.Discard)
		azcorelog.SetListener(nil)
		closeFile()
	}
}

func isDebug(flags *pflag.FlagSet) bool {
	if flags != nil {
		if v, err := flags.GetBool("debug"); err == nil && v {
			return true
		}
	}
	v, _ := strconv.ParseBool(os.Getenv("AZD_EXT_DEBUG"))
	return v
}
