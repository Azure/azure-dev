// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"azure.ai.rle/internal/monitor"
	"azure.ai.rle/internal/rollouts"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

var runRolloutMonitor = monitor.Run

func newMonitorCommand() *cobra.Command {
	var rolloutID string
	var noBrowser bool
	var outputDir string
	cmd := &cobra.Command{
		Use:   "monitor --rollout-id <id>",
		Short: "Open a local dashboard for a saved rollout",
		Long: `Open a read-only browser dashboard for a locally saved rollout.

Read .output/<rollout-id>/summary.json and rollout.json, or use --output-dir
to select the artifact root used during execution. No sign-in, Foundry project
setting, local source folder, or running sandbox is required. This command does
not fetch remote results, search a separate cache, or execute another rollout.
If the rollout directory does not exist, the command warns and exits without
opening a browser. Incomplete or corrupt artifacts still return an error.

The monitor stays running until Ctrl+C. Use --no-browser to open the printed
link manually and enter the local access code.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireRolloutMonitorEnabled(); err != nil {
				return err
			}
			rolloutID = strings.TrimSpace(rolloutID)
			if rolloutID == "" {
				return &azdext.LocalError{
					Message:    "--rollout-id is required but was not provided.",
					Code:       "rle_monitor_rollout_id_required",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Provide --rollout-id <id>, using the ID printed by azd ai rle rollout.",
				}
			}
			if err := rollouts.ValidateID(rolloutID); err != nil {
				return invalidMonitorIDError(err)
			}
			if err := validateMonitorOutput(cmd); err != nil {
				return err
			}
			directory, err := resolveRolloutOutputDir(outputDir)
			if err != nil {
				return err
			}
			reader := &rollouts.ArtifactReader{OutputDir: directory}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			err = runRolloutMonitor(ctx, reader, rolloutID, noBrowser, cmd.OutOrStdout(), cmd.ErrOrStderr())
			if errors.Is(err, rollouts.ErrArtifactDirectoryNotFound) {
				_, writeErr := fmt.Fprintf(cmd.ErrOrStderr(),
					"Warning: no output directory exists for rollout %s at %s.\n"+
						"No dashboard was opened. Older rollouts may not have exported artifacts; "+
						"check --output-dir if they were saved elsewhere.\n",
					rolloutID, filepath.Join(directory, rolloutID))
				return writeErr
			}
			return err
		},
	}
	cmd.Flags().StringVar(&rolloutID, "rollout-id", "", "ID of a rollout in the artifact directory (required).")
	cmd.Flags().StringVar(&outputDir, "output-dir", defaultRolloutOutputDir, "Artifact root used by rollout --output-dir.")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the dashboard link without opening a browser.")
	return cmd
}

func resolveRolloutOutputDir(directory string) (string, error) {
	if strings.TrimSpace(directory) == "" {
		return "", &azdext.LocalError{
			Message: "--output-dir requires a non-empty directory.", Code: "rle_rollout_output_required",
			Category: azdext.LocalErrorCategoryUser, Suggestion: "Use .output or the artifact root used during execution.",
		}
	}
	path, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve rollout artifact directory: %w", err)
	}
	return path, nil
}

func rolloutMonitorEnabled() bool {
	return rleEnableAllEnabled()
}

func requireRolloutMonitorEnabled() error {
	if rolloutMonitorEnabled() {
		return nil
	}
	return &azdext.LocalError{
		Message:    "The rollout monitor is available only in RLE development mode.",
		Code:       "rle_monitor_disabled",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Set AZD_AI_RLE_ENABLE_ALL=true to enable local rollout monitoring.",
	}
}

func invalidMonitorIDError(err error) error {
	return &azdext.LocalError{
		Message:    err.Error(),
		Code:       "rle_invalid_rollout_id",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Use the 32-character rollout ID printed by azd ai rle rollout.",
	}
}

func validateMonitorOutput(cmd *cobra.Command) error {
	flag := cmd.Flag("output")
	if flag != nil && flag.Changed {
		return &azdext.LocalError{
			Message:    "--output cannot be used with the rollout monitor.",
			Code:       "rle_monitor_conflicting_arguments",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Remove --output. The monitor prints its local browser link and access code.",
		}
	}
	return nil
}
