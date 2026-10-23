// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
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
var runAPIJobMonitor = monitor.RunRemoteJob

func newMonitorCommand() *cobra.Command {
	var rolloutID string
	var jobID string
	var noBrowser bool
	var outputDir string
	cmd := &cobra.Command{
		Use:   "monitor (--rollout-id <id> | --job-id <id>)",
		Short: "Open a local dashboard for a saved rollout or a training job's rollouts",
		Long: `Open a read-only browser dashboard for saved rollouts.

With --rollout-id, read .output/<rollout-id>/summary.json and rollout.json, or
use --output-dir to select the artifact root used during execution. No sign-in,
Foundry project setting, local source folder, or running sandbox is required.
If the rollout directory does not exist, the command warns and exits without
opening a browser. Incomplete or corrupt artifacts still return an error.

With --job-id, always read config, metrics, logs and rollouts directly from the
RLE service into memory: no local run files are required, and graphs are
fetched only when opened. Opening any rollout also shows its
persisted progress events, with more pages available on demand.
Job status is read directly from the fine-tuning API
through the Foundry project URL, independently of RLE job registration.
Sign-in and a Foundry project are required.

The monitor stays running until Ctrl+C. Use --no-browser to open the printed
link manually and enter the local access code.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rolloutID = strings.TrimSpace(rolloutID)
			jobID = strings.TrimSpace(jobID)
			if rolloutID != "" && jobID != "" {
				return &azdext.LocalError{
					Message:    "--rollout-id and --job-id cannot be used together.",
					Code:       "rle_monitor_conflicting_arguments",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Pass --job-id to browse a training run, or --rollout-id to open one saved rollout.",
				}
			}
			if rolloutID == "" && jobID == "" {
				return &azdext.LocalError{
					Message:    "--rollout-id or --job-id is required but neither was provided.",
					Code:       "rle_monitor_rollout_id_required",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Provide --rollout-id <id> from azd ai rle rollout, or --job-id <id> from azd ai rle train.",
				}
			}
			if err := validateMonitorOutput(cmd); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			if jobID != "" {
				return runMonitorForJob(ctx, cmd, jobID, noBrowser)
			}
			if err := rollouts.ValidateID(rolloutID); err != nil {
				return invalidMonitorIDError(err)
			}
			directory, err := resolveRolloutOutputDir(outputDir)
			if err != nil {
				return err
			}
			reader := &rollouts.ArtifactReader{OutputDir: directory}
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
	cmd.Flags().StringVar(&rolloutID, "rollout-id", "", "ID of a rollout in the artifact directory.")
	cmd.Flags().StringVar(&jobID, "job-id", "", "ID of a training job whose recorded rollouts to browse.")
	cmd.Flags().StringVar(&outputDir, "output-dir", defaultRolloutOutputDir, "Artifact root used by rollout --output-dir.")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the dashboard link without opening a browser.")
	return cmd
}

func runMonitorForJob(
	ctx context.Context,
	cmd *cobra.Command,
	jobID string,
	noBrowser bool,
) error {
	if err := rejectRealMonitorFlags(cmd, "output-dir"); err != nil {
		return err
	}
	if err := validateRleJobID(jobID); err != nil {
		return err
	}
	projectEndpoint, err := resolveJobsProjectEndpoint()
	if err != nil {
		return err
	}
	return runRealJobMonitor(ctx, cmd, projectEndpoint, jobID, noBrowser)
}

func validateRleJobID(jobID string) error {
	if rleJobIDPattern.MatchString(jobID) {
		return nil
	}
	return &azdext.LocalError{
		Message: "job_id must start with ftjob- followed by 24 or 32 lowercase hexadecimal characters.",
		Code:    "rle_invalid_job_id", Category: azdext.LocalErrorCategoryUser,
		Suggestion: "Use the job ID returned by the real fine-tuning service.",
	}
}

func rejectRealMonitorFlags(cmd *cobra.Command, flags ...string) error {
	for _, name := range flags {
		if flag := cmd.Flag(name); flag != nil && flag.Changed {
			return &azdext.LocalError{
				Message: fmt.Sprintf("--%s cannot be used with real-service API monitoring.", name),
				Code:    "rle_monitor_conflicting_arguments", Category: azdext.LocalErrorCategoryUser,
				Suggestion: "Remove the flag. Real-service monitoring always runs and does not stream or mirror files.",
			}
		}
	}
	return nil
}

func runRealJobMonitor(
	ctx context.Context, cmd *cobra.Command, projectEndpoint string, jobID string, noBrowser bool,
) error {
	rle, err := createRleClient(projectEndpoint)
	if err != nil {
		return err
	}
	rle.credential = newCachedTokenCredential(rle.credential)
	source := &rleJobSource{rle: rle, jobID: jobID}
	return runAPIJobMonitor(ctx, source, jobID, noBrowser, cmd.OutOrStdout(), cmd.ErrOrStderr())
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
			Message:    "--output cannot be used with the monitor.",
			Code:       "rle_monitor_conflicting_arguments",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Remove --output. The monitor prints its local browser link and access code.",
		}
	}
	return nil
}
