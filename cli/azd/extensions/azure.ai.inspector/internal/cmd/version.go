// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaiinspector/internal/version"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newVersionCommand(outputFormat *string) *cobra.Command {
	cmd := azdext.NewVersionCommand("azure.ai.inspector", version.Version, outputFormat)
	cmd.Example = `  # Display the installed inspector extension version
  azd ai inspector version`
	return cmd
}
