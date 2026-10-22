// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"azure.ai.rle/internal/monitor"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

var runHeliosJobMonitor = func(
	ctx context.Context, source monitor.JobSource, runID string, noBrowser bool, out, errOut io.Writer,
) error {
	return monitor.RunHeliosJob(ctx, source, runID, noBrowser, out, errOut)
}

func newHeliosMonitorCommand() *cobra.Command {
	var runID string
	var noBrowser bool
	cmd := &cobra.Command{
		Use:   "helios-monitor --run-id <id>",
		Short: "Open the Helios Monitor Dashboard for an RLE telemetry run",
		Long: `Open the existing RLE browser dashboard with live data read from Helios.

The command reads metrics and traces directly from the Foundry project's Helios
experiment-tracking service into a bounded in-memory cache. It does not create
or require a local run mirror. Set FOUNDRY_PROJECT_ENDPOINT to the Foundry
project that owns the run.

The monitor stays running until Ctrl+C. Use --no-browser to open the printed
link manually.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runID = strings.TrimSpace(runID)
			if err := validateHeliosRunID(runID); err != nil {
				return err
			}
			if err := validateMonitorOutput(cmd); err != nil {
				return err
			}
			projectEndpoint, err := resolveJobsProjectEndpoint()
			if err != nil {
				return err
			}
			client, err := createRleClient(projectEndpoint)
			if err != nil {
				return err
			}
			client.credential = newCachedTokenCredential(client.credential)
			source, err := newHeliosJobSource(client, runID)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return runHeliosJobMonitor(
				ctx, source, runID, noBrowser, cmd.OutOrStdout(), cmd.ErrOrStderr(),
			)
		},
	}
	cmd.Flags().StringVar(&runID, "run-id", "", "Helios run ID to monitor.")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the dashboard link without opening a browser.")
	return cmd
}

func validateHeliosRunID(runID string) error {
	switch {
	case runID == "":
		return &azdext.LocalError{
			Message: "--run-id is required.",
			Code:    "rle_helios_run_id_required", Category: azdext.LocalErrorCategoryUser,
			Suggestion: "Provide the Helios run ID used when the RLE telemetry was exported.",
		}
	case len(runID) > 256:
		return invalidHeliosRunIDError("Helios run ID must not exceed 256 characters.")
	case strings.ContainsAny(runID, "/\\"):
		return invalidHeliosRunIDError("Helios run ID must not contain path separators.")
	}
	for _, character := range runID {
		if character < 0x20 || character == 0x7f {
			return invalidHeliosRunIDError("Helios run ID must not contain control characters.")
		}
	}
	return nil
}

func invalidHeliosRunIDError(message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     "rle_invalid_helios_run_id",
		Category: azdext.LocalErrorCategoryUser,
		Suggestion: fmt.Sprintf(
			"Use the run ID shown in the Helios project configured by %s.",
			foundryProjectEndpointEnvVar,
		),
	}
}
