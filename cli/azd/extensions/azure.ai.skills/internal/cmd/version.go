// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azureaiskills/internal/version"

	"github.com/spf13/cobra"
)

// newVersionCommand returns a `version` subcommand that prints the extension
// version, commit, and build date populated at build time via ldflags.
func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the extension version.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Version: %s\nCommit: %s\nBuild Date: %s\n", version.Version, version.Commit, version.BuildDate)
		},
	}
}
