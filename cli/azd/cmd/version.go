// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type versionFlags struct {
	outputFormat string
	global       *internal.GlobalCommandOptions
}

func (v *versionFlags) Setup(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	output.AddOutputFlag(local, &v.outputFormat, []output.Format{output.JsonFormat, output.NoneFormat}, output.NoneFormat)
	v.global = global
}

func versionCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *versionFlags) {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version number of Azure Developer CLI.",
	}

	flags := &versionFlags{}
	flags.Setup(cmd.Flags(), global)

	return cmd, flags
}

type versionAction struct {
	flags     versionFlags
	formatter output.Formatter
	writer    io.Writer
}

func newVersionAction(flags versionFlags, formatter output.Formatter, writer io.Writer) *versionAction {
	return &versionAction{
		flags:     flags,
		formatter: formatter,
		writer:    writer,
	}
}

func (v *versionAction) Run(ctx context.Context) error {
	switch v.formatter.Kind() {
	case output.NoneFormat:
		fmt.Printf("azd version %s\n", internal.Version)
	case output.JsonFormat:
		versionSpec := internal.GetVersionSpec()
		err := v.formatter.Format(versionSpec, v.writer, nil)
		if err != nil {
			return err
		}
	}

	return nil
}
