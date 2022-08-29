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

func versionCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		commands.ActionFunc(versionAction),
		commands.BuildOptions{
			GlobalOptions: rootOptions,
			Use:           "version",
			Short:         "Print the version number of Azure Developer CLI.",
			Long:          "",
		},
	)

	return output.AddOutputParam(
		cmd,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat,
	)
}

func versionAction(ctx context.Context, cmd *cobra.Command, _ []string, _ *azdcontext.AzdContext) error {
	formatter := output.GetFormatter(ctx)
	writer := output.GetWriter(ctx)

	switch formatter.Kind() {
	case output.NoneFormat:
		fmt.Printf("azd version %s\n", internal.Version)
	case output.JsonFormat:
		versionSpec := internal.GetVersionSpec()
		err := formatter.Format(versionSpec, writer, nil)
		if err != nil {
			return err
		}
	}

	return nil
}
