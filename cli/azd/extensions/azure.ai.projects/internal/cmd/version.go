// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azure.ai.projects/internal/version"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newVersionCommand(outputFormat *string) *cobra.Command {
	cmd := azdext.NewVersionCommand(
		"azure.ai.projects",
		version.Version,
		outputFormat,
	)
	cmd.Example = `  # Display the installed project extension version
  azd ai project version`
	return cmd
}
