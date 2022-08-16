// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os/user"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/pkg/errors"
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

func uploadCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "upload",
		Short:  "Upload telemetry",
		Long:   "Upload telemetry",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !rootOptions.EnableTelemetry {
				return nil
			}

			user, err := user.Current()
			if err != nil {
				return errors.Errorf("could not determine current user: %w", err)
			}

			telemetryDir := filepath.Join(user.HomeDir, ".azd", "telemetry")
			storageQueue, err := telemetry.NewStorageQueue(telemetryDir, "trn")
			if err != nil {
				return errors.Errorf("could not initialize storage %w", err)
			}

			uploader := telemetry.NewUploader(storageQueue, "d3b9c006-3680-4300-9862-35fce9ac66c7", nil)
			uploader.Upload()

			return nil
		},
	}
	return cmd
}
