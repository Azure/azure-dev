// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
// TODO: Remove
// Trivial change to test pipeline
package main

import (
	"context"
	"os"

	"azure.ai.finetune/internal/cmd"
	"github.com/fatih/color"
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

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		color.Red("Error: %v", err)
		os.Exit(1)
	}
}
