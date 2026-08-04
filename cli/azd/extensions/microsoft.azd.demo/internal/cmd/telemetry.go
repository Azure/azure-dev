// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"log"

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
				log.Printf("demo telemetry: failed to create azd client: %v", err)
				color.Yellow("Telemetry is unavailable; continuing without reporting.")
				return nil
			}

			defer azdClient.Close()

			resp, err := azdClient.Telemetry().ReportUsage(ctx, &azdext.ReportUsageRequest{
				EventName: "demo.telemetry.reported",
				Attributes: map[string]string{
					"demo.mode":    "sample",
					"demo.outcome": "completed",
				},
			})
			if err != nil {
				log.Printf("demo telemetry: failed to report usage: %v", err)
				color.Yellow("Telemetry could not be reported; continuing.")
				return nil
			}

			if resp == nil {
				log.Print("demo telemetry: host returned an empty response")
				color.Yellow("Telemetry returned no response; continuing.")
				return nil
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
