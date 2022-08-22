// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/spf13/cobra"
)

func telemetryCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "telemetry",
		Short:  "Telemetry command",
		Long:   "Telemetry command",
		Hidden: true,
	}
	cmd.AddCommand(uploadCmd(rootOptions))
	return cmd
}

const UploadCommandPath = "telemetry upload"

func uploadCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "upload",
		Short:  "Upload telemetry",
		Long:   "Upload telemetry",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			telemetrySystem := telemetry.GetTelemetrySystem()

			if telemetrySystem == nil {
				return nil
			}

			return telemetrySystem.RunBackgroundUpload(cmd.Context(), rootOptions.EnableDebugLogging)
		},
	}
	return cmd
}
