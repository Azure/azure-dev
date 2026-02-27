// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/update"
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
	flags               *versionFlags
	formatter           output.Formatter
	writer              io.Writer
	console             input.Console
	alphaFeatureManager *alpha.FeatureManager
}

func newVersionAction(
	flags *versionFlags,
	formatter output.Formatter,
	writer io.Writer,
	console input.Console,
	alphaFeatureManager *alpha.FeatureManager,
) actions.Action {
	return &versionAction{
		flags:               flags,
		formatter:           formatter,
		writer:              writer,
		console:             console,
		alphaFeatureManager: alphaFeatureManager,
	}
}

func (v *versionAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	switch v.formatter.Kind() {
	case output.NoneFormat:
		channelSuffix := v.channelSuffix()
		fmt.Fprintf(v.console.Handles().Stdout, "azd version %s%s\n", internal.Version, channelSuffix)
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

// channelSuffix returns a display suffix like " (stable)" or " (daily)".
// Based on the running binary's version string, not the configured channel.
// Only shown when the update alpha feature is enabled.
func (v *versionAction) channelSuffix() string {
	if !v.alphaFeatureManager.IsEnabled(update.FeatureUpdate) {
		return ""
	}

	// Detect from the binary itself: if the version contains "daily.", it's a daily build.
	if _, err := update.ParseDailyBuildNumber(internal.Version); err == nil {
		return " (daily)"
	}

	return " (stable)"
}
