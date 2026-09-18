// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/helpformat"
	"github.com/spf13/cobra"
)

const aiNamespaceHelpAnnotation = "ai-help.namespace"

func installAINamespaceHelp(cmd *cobra.Command) {
	footer := ""
	if cmd.CommandPath() == "azd ai" {
		cmd.Long = "Build and manage AI applications with azd extensions.\n\n" +
			"Available commands reflect the extensions installed on this machine. " +
			"Each extension provides its own commands, prerequisites, and configuration."
		cmd.Example = `  # Find available AI extensions
  azd extension list

  # Show installed extensions
  azd extension list --installed`
		footer = `Environments & Configuration:
  An azd environment is a named set of deployment values for a project.
  Use 'azd env new <name>' to create one and 'azd env select <name>' to select it.
  Values managed by 'azd env set <key> <value>' are persisted in .azure/<environment>/.env.
  These deployment values are distinct from environment variables inside a running service.

  Extensions differ in how they resolve project endpoints and other context.
  See the selected extension's --help for its supported flags and configuration sources.`
	} else {
		cmd.Example = fmt.Sprintf("  # Explore commands in this namespace\n  %s --help", cmd.CommandPath())
	}
	helpformat.Install(cmd, "", footer)
}
