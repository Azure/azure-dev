// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func newTelemetryCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "telemetry",
		Short: "Report a sample usage event through the telemetry service.",
		Long: "Reports a sample usage event so you can see how an extension " +
			"records its own telemetry through the azd host pipeline.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}

			defer azdClient.Close()

			started := time.Now()

			// Every value here is a fixed, low-cardinality string. Never
			// send resource names, paths, prompts, or anything a user
			// typed: those are customer content and must not reach the
			// telemetry pipeline. Durations are bucketed for the same
			// reason, so the dimension stays cheap to aggregate.
			resp, err := azdClient.Telemetry().ReportUsage(ctx, &azdext.ReportUsageRequest{
				EventName: "demo.telemetry.reported",
				Attributes: map[string]string{
					"demo.mode":     "sample",
					"demo.duration": durationBucket(time.Since(started)),
				},
			})
			if err != nil {
				return fmt.Errorf("failed to report usage event: %w", err)
			}

			if resp.Accepted {
				color.Green("Usage event recorded as an ext.usage span.")
				return nil
			}

			// Accepted is false rather than an error when the host keeps
			// nothing: the extension was not installed from the official
			// registry, or the per-invocation event budget is spent. Run
			// azd with --debug to see which one it was.
			color.Yellow("Usage event was not recorded. Run with --debug for the reason.")

			return nil
		},
	}
}

// durationBucket maps an elapsed time onto a small fixed set of values.
// An exact duration would be unbounded cardinality, which makes the data
// expensive to store and useless to aggregate.
func durationBucket(elapsed time.Duration) string {
	switch {
	case elapsed < time.Second:
		return "under_1s"
	case elapsed < 10*time.Second:
		return "under_10s"
	default:
		return "over_10s"
	}
}
