// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// optimize_cancel.go implements the "optimize cancel" command, which cancels
// a running optimization job by its operation ID.

package cmd

import (
	"fmt"

	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// optimizeCancelFlags holds connection settings for the cancel command.
type optimizeCancelFlags struct {
	optimizeConnectionFlags
}

func newOptimizeCancelCommand() *cobra.Command {
	flags := &optimizeCancelFlags{}

	cmd := &cobra.Command{
		Use:   "cancel <operation-id>",
		Short: "Cancel a running optimization job.",
		Long: `Cancel a running optimization or evaluation job by its operation ID.

Only jobs in a non-terminal state (pending, running) can be cancelled.`,
		Example: `  # Cancel a running job
  azd ai agent optimize cancel opt_abc123`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOptimizeCancel(cmd, flags, args[0])
		},
	}

	flags.optimizeConnectionFlags.register(cmd)

	return cmd
}

func runOptimizeCancel(cmd *cobra.Command, flags *optimizeCancelFlags, operationID string) error {
	endpoint, err := flags.resolve(cmd.Context())
	if err != nil {
		return err
	}

	credential, err := newAgentCredential()
	if err != nil {
		return err
	}

	client := optimize_api.NewOptimizeClient(endpoint, credential)

	cancelResp, err := client.CancelOptimize(cmd.Context(), operationID)
	if err != nil {
		return fmt.Errorf("failed to cancel job: %w\n\nCheck that the operation ID %q is correct and the job is still running", err, operationID)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  %s Job %s has been cancelled (status: %s).\n",
		color.YellowString("⚠"), operationID, cancelResp.Status)
	fmt.Fprintf(out, "\n  Check status with:\n    azd ai agent optimize status %s\n", operationID)

	return nil
}
