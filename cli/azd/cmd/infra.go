// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/spf13/cobra"
)

func infraCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "infra",
		Short: "Manage Azure resources.",
	}
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))
	cmd.AddCommand(BuildCmd(rootOptions, infraCreateCmdDesign, initInfraCreateAction, nil))
	cmd.AddCommand(BuildCmd(rootOptions, infraDeleteCmdDesign, initInfraDeleteAction, nil))
	return cmd
}
