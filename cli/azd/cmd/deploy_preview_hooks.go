// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "github.com/azure/azure-dev/cli/azd/cmd/actions"

func deploymentHooksEnabled(descriptor *actions.ActionDescriptor) bool {
	preview, _ := descriptor.Options.Command.Flags().GetBool("preview")
	return !preview
}
