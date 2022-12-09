// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/spf13/cobra"
)

const TelemetryCommandFlag = "telemetry"
const TelemetryUploadCommandFlag = "upload"

func telemetryCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:    TelemetryCommandFlag,
		Short:  "Manage telemetry",
		Long:   "Manage telemetry",
		Hidden: true,
		Annotations: map[string]string{
			actions.AnnotationName: "telemetry",
		},
	}
	cmd.AddCommand(uploadCmd(rootOptions))
	return cmd
}

func uploadCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:    TelemetryUploadCommandFlag,
		Short:  "Upload telemetry",
		Long:   "Upload telemetry",
		Hidden: true,
		Annotations: map[string]string{
			actions.AnnotationName: "telemetry-upload",
		},
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
