// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azureaiagent/internal/version"

	"github.com/spf13/cobra"
)

func newVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Prints the version of the application",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Version: %s\nCommit: %s\nBuild Date: %s\n", version.Version, version.Commit, version.BuildDate)
		},
	}

	cmd.Flags().Bool("microsoft-foundry-skill", false, "Identify a Microsoft Foundry Skill invocation")
	_ = cmd.Flags().MarkHidden("microsoft-foundry-skill")

	return cmd
}
