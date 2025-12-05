// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"os"

	"azureaiagent/internal/cmd"

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

	// TODO: Rebase and uncomment after #6321 is merged
	// Hydrate context with traceparent from environment if present
	// if traceparent := os.Getenv("TRACEPARENT"); traceparent != "" {
	// 	ctx = azdext.ContextFromTraceParent(ctx, traceparent)
	// }

	rootCmd := cmd.NewRootCommand()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		color.Red("Error: %v", err)
		os.Exit(1)
	}
}
