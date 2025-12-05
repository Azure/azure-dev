// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"azureaiagent/internal/pkg/azure"

	azcorelog "github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/spf13/pflag"
)

// setupDebugLogging configures the Azure SDK logger if debug mode is enabled.
func setupDebugLogging(flags *pflag.FlagSet) {
	if isDebug(flags) {
		currentDate := time.Now().Format("2006-01-02")
		logFileName := fmt.Sprintf("azd-ai-agents-%s.log", currentDate)

		logFile, err := os.OpenFile(logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			logFile = os.Stderr
		}
		azcorelog.SetListener(func(event azcorelog.Event, msg string) {
			msg = azure.ConnectionStringJSONRegex.ReplaceAllString(msg, `${1}"REDACTED"`)
			fmt.Fprintf(logFile, "[%s] %s: %s\n", time.Now().Format(time.RFC3339), event, msg)
		})
	}
}

// isDebug checks if debug mode is enabled via --debug flag or AZD_EXT_DEBUG environment variable
func isDebug(flags *pflag.FlagSet) bool {
	if debugFlag, err := flags.GetBool("debug"); err == nil && debugFlag {
		return true
	}

	debug, _ := strconv.ParseBool(os.Getenv("AZD_EXT_DEBUG"))
	return debug
}
