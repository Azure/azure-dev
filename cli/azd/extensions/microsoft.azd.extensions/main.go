// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"os"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/cmd"
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

	cwd, err := rootCmd.PersistentFlags().GetString("cwd")
	if err != nil {
		color.Red("Error: %v", err)
		os.Exit(1)
	}

	if cwd != "." {
		if err := os.Chdir(cwd); err != nil {
			color.Red("Error: failed to change directory to %s: %v", cwd, err)
			os.Exit(1)
		}
	}

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		color.Red("Error: %v", err)
		os.Exit(1)
	}
}
