// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func authActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("auth", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "auth",
			Short: "Authenticate with Azure.",
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupConfig,
		},
	})

	group.Add("token", &actions.ActionDescriptorOptions{
		Command:        newAuthTokenCmd(),
		FlagsResolver:  newAuthTokenFlags,
		ActionResolver: newAuthTokenAction,
		OutputFormats:  []output.Format{output.JsonFormat},
		DefaultFormat:  output.NoneFormat,
	})

	group.Add("login", &actions.ActionDescriptorOptions{
		Command:        newLoginCmd("auth"),
		FlagsResolver:  newAuthLoginFlags,
		ActionResolver: newAuthLoginAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	group.Add("logout", &actions.ActionDescriptorOptions{
		Command:        newLogoutCmd("auth"),
		ActionResolver: newLogoutAction,
	})

	// Register the new `local-imds` command
	group.Add("local-imds", &actions.ActionDescriptorOptions{
		Command:        newLocalIMDSCmd("auth"),
		ActionResolver: newLocalIMDSAction,
	})

	return group
}
