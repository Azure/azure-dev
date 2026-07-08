// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

var (
	// Version is populated at build time.
	Version = "dev"
	// Commit is populated at build time.
	Commit = "none"
	// BuildDate is populated at build time.
	BuildDate = "unknown"
)

func newVersionCommand(outputFormat *string) *cobra.Command {
	return azdext.NewVersionCommand("azure.ai.rle", Version, outputFormat)
}
