// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/spf13/cobra"
)

func isDeploymentPreview(command *cobra.Command) bool {
	if command == nil || command.Name() != "deploy" ||
		(command.Parent() != nil && command.Parent().Parent() != nil) {
		return false
	}
	preview, _ := command.Flags().GetBool("preview")
	return preview
}

func deploymentHooksEnabled(descriptor *actions.ActionDescriptor) bool {
	preview, _ := descriptor.Options.Command.Flags().GetBool("preview")
	return !preview
}
