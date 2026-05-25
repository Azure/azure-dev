// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// RegisterCommands attaches the connection CRUD subcommands (list, show, create,
// update, delete) to the provided parent command. The parent is expected to
// already declare the persistent "-p / --project-endpoint" flag.
func RegisterCommands(parent *cobra.Command, extCtx *azdext.ExtensionContext) {
	parent.AddCommand(newConnectionListCommand(extCtx))
	parent.AddCommand(newConnectionShowCommand(extCtx))
	parent.AddCommand(newConnectionCreateCommand(extCtx))
	parent.AddCommand(newConnectionUpdateCommand(extCtx))
	parent.AddCommand(newConnectionDeleteCommand(extCtx))
}
