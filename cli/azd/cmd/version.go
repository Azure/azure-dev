// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
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
	flags      *versionFlags
	formatter  output.Formatter
	writer     io.Writer
	console    input.Console
	llmManager llm.Manager
}

func newVersionAction(
	flags *versionFlags,
	formatter output.Formatter,
	writer io.Writer,
	console input.Console,
	llmManager llm.Manager,
) actions.Action {
	return &versionAction{
		flags:      flags,
		formatter:  formatter,
		writer:     writer,
		console:    console,
		llmManager: llmManager,
	}
}

func (v *versionAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	switch v.formatter.Kind() {
	case output.NoneFormat:
		fmt.Fprintf(v.console.Handles().Stdout, "azd version %s\n", internal.Version)
		time.Sleep(500 * time.Millisecond)
		_, err := v.llmManager.Info(v.console.Handles().Stdout)
		if err != nil {
			return nil, fmt.Errorf("failed to get LLM info: %w", err)
		}
		fmt.Fprintf(v.console.Handles().Stdout, "\n")
	case output.JsonFormat:
		var result contracts.VersionResult
		versionSpec := internal.VersionInfo()

		result.Azd.Commit = versionSpec.Commit
		result.Azd.Version = versionSpec.Version.String()

		err := v.formatter.Format(result, v.writer, nil)
		if err != nil {
			return nil, err
		}
	}

	return nil, nil
}
