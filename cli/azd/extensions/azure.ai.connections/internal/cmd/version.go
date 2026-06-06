// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azure.ai.connections/internal/version"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newVersionCommand(outputFormat *string) *cobra.Command {
	return azdext.NewVersionCommand("azure.ai.connections", version.Version, outputFormat)
}
