// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/spf13/cobra"
)

func versionCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	return commands.Build(
		commands.ActionFunc(
			func(context.Context, *cobra.Command, []string, *environment.AzdContext) error {
				fmt.Printf("azd version %s\n", internal.Version)
				return nil
			},
		),
		rootOptions,
		"version",
		"Print the version number of azd",
		"",
	)
}
