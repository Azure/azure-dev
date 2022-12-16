// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func infraActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("infra", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: "Manage Azure resources.",
		},
	})

	group.Add("create", &actions.ActionDescriptorOptions{
		Command:        newInfraCreateCmd(),
		FlagsResolver:  newInfraCreateFlags,
		ActionResolver: newInfraCreateAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	group.Add("delete", &actions.ActionDescriptorOptions{
		Command:        newInfraDeleteCmd(),
		FlagsResolver:  newInfraDeleteFlags,
		ActionResolver: newInfraDeleteAction,
	})

	return group
}
