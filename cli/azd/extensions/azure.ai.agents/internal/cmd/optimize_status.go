// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// optimize_status.go implements the "optimize status" command, which checks
// or watches the status of an optimization job.

package cmd

import (
	"fmt"
	"io"

	"azureaiagent/internal/pkg/agents/optimize_api"

	azdext "github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// optimizeStatusFlags holds CLI flags for the optimize status command.
type optimizeStatusFlags struct {
	watch        bool // poll until job completes
	pollInterval int  // polling interval in seconds
	optimizeConnectionFlags
}

func newOptimizeStatusCommand() *cobra.Command {
	flags := &optimizeStatusFlags{}

	cmd := &cobra.Command{
		Use:   "status [operation-id]",
		Short: "Check the status of an optimization job.",
		Long: `Check the status of an optimization job by its operation ID.

If no operation ID is provided, uses the last optimization job from this project.
Use --watch to poll until the job completes.`,
		Example: `  # Check last job status (auto-resolved)
  azd ai agent optimize status

  # Check specific job status
  azd ai agent optimize status opt_abc123

  # Watch until complete
  azd ai agent optimize status opt_abc123 --watch`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			operationID := ""
			if len(args) > 0 {
				operationID = args[0]
			} else {
				operationID = loadLastOptimizeJobID(ctx)
				if operationID == "" {
					return fmt.Errorf("operation ID is required: provide it as an argument, or run 'azd ai agent optimize' first")
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  Using last job: %s\n\n", operationID)
			}
			return runOptimizeStatus(cmd, flags, operationID)
		},
	}

	cmd.Flags().BoolVar(&flags.watch, "watch", false, "Poll until job completes")
	cmd.Flags().IntVar(&flags.pollInterval, "poll-interval", 5, "Polling interval in seconds")
	flags.optimizeConnectionFlags.register(cmd)

	return cmd
}

func runOptimizeStatus(cmd *cobra.Command, flags *optimizeStatusFlags, operationID string) error {
	endpoint, err := flags.resolve(cmd.Context())
	if err != nil {
		return err
	}

	credential, err := newAgentCredential()
	if err != nil {
		return err
	}

	client := optimize_api.NewOptimizeClient(endpoint, credential)
	out := cmd.OutOrStdout()

	status, err := client.GetOptimizeStatus(cmd.Context(), operationID)
	if err != nil {
		return fmt.Errorf("failed to get job status: %w\n\nCheck that the operation ID %q is correct", err, operationID)
	}

	printOptimizeJobSummary(out, status)

	hasProject := isInAzdProject(cmd.Context())

	if flags.watch && !optimize_api.IsTerminal(status.Status) {
		finalStatus, err := pollOptimizeJob(cmd, client, flags.pollInterval, operationID)
		if err != nil {
			return err
		}
		printOptimizeResults(cmd.Context(), out, finalStatus, hasProject)
	} else if len(status.Candidates) > 0 {
		printOptimizeResults(cmd.Context(), out, status, hasProject)
	}

	if status.Error != nil {
		return fmt.Errorf("optimization job failed: %s", status.Error.Message)
	}

	return nil
}

// printOptimizeJobSummary prints a brief summary of an optimization job's state.
func printOptimizeJobSummary(out io.Writer, status *optimize_api.OptimizeJobStatus) {
	fmt.Fprintf(out, "  Job ID:  %s\n", color.CyanString(status.OperationID))
	fmt.Fprintf(out, "  Status:  %s\n", formatOptimizeStatus(status.Status))
	if status.Agent != nil && status.Agent.AgentName != "" {
		fmt.Fprintf(out, "  Agent:   %s\n", status.Agent.AgentName)
	}
	if status.AllTargetAttributesFailed {
		fmt.Fprintf(out, "  Strategy: %s\n", color.YellowString("failed (baseline only — no candidates generated)"))
	} else if status.Progress != nil && status.Progress.CurrentTargetAttribute != "" {
		fmt.Fprintf(out, "  Strategy: %s\n", status.Progress.CurrentTargetAttribute)
	}
	if status.Best != nil {
		fmt.Fprintf(out, "  Best:    %.2f\n", status.Best.AvgScore)
	}
	if status.CreatedAt != "" {
		fmt.Fprintf(out, "  Created: %s\n", status.CreatedAt)
	}
	if status.Error != nil {
		fmt.Fprintf(out, "  Error:   %s\n", color.RedString(status.Error.Message))
	}
	if len(status.Warnings) > 0 {
		for _, w := range status.Warnings {
			fmt.Fprintf(out, "  Warning: %s\n", color.YellowString(w))
		}
	}
	fmt.Fprintln(out)
}
