// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

var (
	// Populated at build time
	Version   = "dev" // Default value for development builds
	Commit    = "none"
	BuildDate = "unknown"
)

func newVersionCommand(outputFormat *string) *cobra.Command {
	return azdext.NewVersionCommand("azure.ai.projects", Version, outputFormat)
}
