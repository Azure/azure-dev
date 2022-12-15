// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type versionFlags struct {
	global *internal.GlobalCommandOptions
}

func (v *versionFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	v.global = global
}

func newVersionFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *versionFlags {
	flags := &versionFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

type versionAction struct {
	flags     *versionFlags
	formatter output.Formatter
	writer    io.Writer
	console   input.Console
}

func newVersionAction(
	flags *versionFlags,
	formatter output.Formatter,
	writer io.Writer,
	console input.Console,
) actions.Action {
	return &versionAction{
		flags:     flags,
		formatter: formatter,
		writer:    writer,
		console:   console,
	}
}

func (v *versionAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	switch v.formatter.Kind() {
	case output.NoneFormat:
		fmt.Fprintf(v.console.Handles().Stdout, "azd version %s\n", internal.Version)
	case output.JsonFormat:
		versionSpec := internal.GetVersionSpec()
		err := v.formatter.Format(versionSpec, v.writer, nil)
		if err != nil {
			return nil, err
		}
	}

	return nil, nil
}
