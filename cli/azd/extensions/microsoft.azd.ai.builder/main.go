// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.ai.builder/internal/cmd"
	"github.com/fatih/color"
	"github.com/spf13/pflag"
)

func init() {
	forceColorVal, has := os.LookupEnv("FORCE_COLOR")
	if has && forceColorVal == "1" {
		color.NoColor = false
	}
}

func main() {
	// Execute the root command
	ctx := context.Background()
	rootCmd := cmd.NewRootCommand()

	debugEnabled := isDebugEnabled()
	configureLogger(debugEnabled)

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		color.Red("Error: %v", err)
		os.Exit(1)
	}
}

func isDebugEnabled() bool {
	debugEnabled := false
	pflag.BoolVar(&debugEnabled, "debug", false, "Enable debug logging")
	pflag.Parse()

	return debugEnabled
}

// ConfigureLogger sets up logging based on the debug flag.
// - If debug is true, logs go to a daily file in "logs/" next to the executable.
// - If debug is false, logs are discarded.
// - Standard log functions (log.Print, log.Println, etc.) will use this setup.
func configureLogger(debug bool) {
	var output io.Writer = io.Discard // Default: discard logs

	if debug {
		// Determine the directory of the running executable
		exePath, err := os.Executable()
		if err != nil {
			fmt.Println("Failed to get executable path:", err)
			return
		}
		exeDir := filepath.Dir(exePath)

		// Ensure the logs directory exists
		logDir := filepath.Join(exeDir, "logs")
		if err := os.MkdirAll(logDir, 0755); err != nil {
			fmt.Println("Failed to create log directory:", err)
			return
		}

		// Generate log file name per day
		logFile := filepath.Join(logDir, fmt.Sprintf("app-%s.log", time.Now().Format("2006-01-02")))
		fmt.Println("Logging to:", logFile)

		// Open the log file for appending
		file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Println("Failed to open log file:", err)
			return
		}

		// Send logs to both file and console
		output = io.MultiWriter(file, os.Stdout)
	}

	// Configure the global logger
	log.SetOutput(output)
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetPrefix("[APP] ")
	log.Println("Logger initialized")
}
