// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureappservice/internal/version"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Display the version of the extension.",
		RunE: func(cmd *cobra.Command, args []string) error {
			color.Cyan("Azure App Service Extension")
			color.White("Version: %s", version.Version)
			return nil
		},
	}
}
