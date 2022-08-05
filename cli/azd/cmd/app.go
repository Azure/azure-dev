// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func appCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Manage Azure application.",
	}
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))

	cmd.AddCommand(output.AddOutputParam(
		appDeployCmd(rootOptions),
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat,
	))
	cmd.AddCommand(appMonitorCmd(rootOptions))
	cmd.AddCommand(appRestoreCmd(rootOptions))
	return cmd
}
