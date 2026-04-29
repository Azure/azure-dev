// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
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
	// Execute the root command. The SDK's NewExtensionRootCommand handles
	// --cwd / AZD_CWD by changing the working directory in PersistentPreRunE,
	// so no manual chdir is required here.
	ctx := context.Background()
	rootCmd := cmd.NewRootCommand()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		// Check if this is our custom UserFriendlyError type
		var userFriendlyErr *internal.UserFriendlyError
		if errors.As(err, &userFriendlyErr) {
			// Display the error message in red
			color.Red("Error: %v", userFriendlyErr.Error())

			// If we have user details, display them in normal text color
			if userFriendlyErr.GetUserDetails() != "" {
				fmt.Println()
				fmt.Println(userFriendlyErr.GetUserDetails())
			}
		} else {
			// Default error handling for regular errors
			color.Red("Error: %v", err)
		}
		os.Exit(1)
	}
}
