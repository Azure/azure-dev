// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"os"

	"azureappservice/internal/cmd"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
	ctx := azdext.NewContext()
	rootCmd := cmd.NewRootCommand()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		color.Red("Error: %v", err)
		os.Exit(1)
	}
}
