// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/commands/pipeline"
	"github.com/spf13/cobra"
)

func pipelineCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Manage GitHub Actions pipelines.",
		Long: `Manage GitHub Actions pipelines.

The Azure Developer CLI template includes a GitHub Actions pipeline configuration file (in the *.github/workflows* folder) that deploys your application whenever code is pushed to the main branch.

For more information, go to https://aka.ms/azure-dev/pipeline.`,
	}
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))
	cmd.AddCommand(pipelineConfigCmd(rootOptions))
	return cmd
}

func pipelineConfigCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		pipeline.NewConfigAction(rootOptions),
		rootOptions,
		"config",
		"Create and configure your deployment pipeline by using GitHub Actions.",
		`Create and configure your deployment pipeline by using GitHub Actions.

For more information, go to https://aka.ms/azure-dev/pipeline.`,
	)
	return cmd
}
