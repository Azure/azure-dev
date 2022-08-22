// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func versionCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		commands.ActionFunc(versionAction),
		rootOptions,
		"version",
		"Print the version number of Azure Developer CLI.",
		"",
	)

	return output.AddOutputParam(
		cmd,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat,
	)
}

func versionAction(_ context.Context, cmd *cobra.Command, _ []string, _ *azdcontext.AzdContext) error {
	formatter, err := output.GetFormatter(cmd)
	if err != nil {
		return err
	}

	switch formatter.Kind() {
	case output.NoneFormat:
		fmt.Printf("azd version %s\n", internal.Version)
	case output.JsonFormat:
		versionSpec := internal.GetVersionSpec()
		err = formatter.Format(versionSpec, cmd.OutOrStdout(), nil)
		if err != nil {
			return err
		}
	}

	return nil
}
