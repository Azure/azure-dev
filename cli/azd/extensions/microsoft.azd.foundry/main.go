// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"os"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.foundry/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
)

func init() {
	forceColorVal, has := os.LookupEnv("FORCE_COLOR")
	if has && forceColorVal == "1" {
		color.NoColor = false
	}

	if err := azdext.SetupDailyLogger(); err != nil {
		color.Red("Error setting up daily logger: %w", err)
		os.Exit(1)
	}
}

func main() {
	// Execute the root command
	ctx := context.Background()
	rootCmd := cmd.NewRootCommand()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		color.Red("Extension Error: %v", err)
		os.Exit(1)
	}

	os.Exit(0)
}
