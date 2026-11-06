// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"azure.resources/internal/cmd"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func main() {
	azdext.Run(newRootCommand())
}

func newRootCommand() *cobra.Command {
	return cmd.NewRootCommand()
}
