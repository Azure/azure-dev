// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// optimize_status.go implements the "optimize status" command, which checks
// or watches the status of an optimization job.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/optimize_api"

	azdext "github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// optimizeStatusFlags holds CLI flags for the optimize status command.
type optimizeStatusFlags struct {
	envName      string // explicit environment name (from -e flag)
	output       string // output format (json or table)
	watch        bool   // poll until job completes
	pollInterval int    // polling interval in seconds
	optimizeConnectionFlags
}

func newOptimizeStatusCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
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
			flags.envName = extCtx.Environment
			flags.output = extCtx.OutputFormat
			operationID := ""
			if len(args) > 0 {
				operationID = args[0]
			} else {
				operationID = loadOptimizeJobIDForAgent(ctx, "", flags.envName)
				if operationID == "" {
					return fmt.Errorf("operation ID is required: provide it as an argument, or run 'azd ai agent optimize' first")
				}
				if flags.output != "json" {
					fmt.Fprintf(cmd.OutOrStdout(), "  Using last job: %s\n\n", operationID)
				}
			}
			return runOptimizeStatus(cmd, flags, operationID)
		},
	}

	cmd.Flags().BoolVar(&flags.watch, "watch", false, "Poll until job completes")
	cmd.Flags().IntVar(&flags.pollInterval, "poll-interval", 10, "Polling interval in seconds")
	flags.optimizeConnectionFlags.register(cmd)

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "table"},
		Default:       "table",
	})

	return cmd
}

func runOptimizeStatus(cmd *cobra.Command, flags *optimizeStatusFlags, operationID string) error {
	endpoint, err := flags.resolve(cmd.Context(), flags.envName)
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

	if flags.output == "json" && !flags.watch {
		return printOptimizeStatusJSON(out, status)
	}

	if flags.output != "json" {
		printOptimizeJobSummary(out, status)
	}

	hasProject := isInAzdProject(cmd.Context())

	if flags.watch && !optimize_api.IsTerminal(status.Status) {
		finalStatus, err := pollOptimizeJob(cmd, client, flags.pollInterval, operationID, flags.envName)
		if err != nil {
			return err
		}
		if flags.output == "json" {
			return printOptimizeStatusJSON(out, finalStatus)
		}
		printOptimizeResults(cmd.Context(), out, finalStatus, hasProject, flags.envName)
	} else if len(status.Candidates()) > 0 {
		if flags.output == "json" {
			return printOptimizeStatusJSON(out, status)
		}
		printOptimizeResults(cmd.Context(), out, status, hasProject, flags.envName)
	}

	if status.Error != nil {
		return fmt.Errorf("optimization job failed: %s", status.Error.Message)
	}

	return nil
}

// printOptimizeStatusJSON writes the job status as indented JSON.
func printOptimizeStatusJSON(out io.Writer, status *optimize_api.OptimizeJobStatus) error {
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal job status to JSON: %w", err)
	}
	fmt.Fprintln(out, string(data))
	return nil
}

// printOptimizeJobSummary prints a brief summary of an optimization job's state.
func printOptimizeJobSummary(out io.Writer, status *optimize_api.OptimizeJobStatus) {
	fmt.Fprintf(out, "  Job ID:  %s\n", color.CyanString(status.ID))
	fmt.Fprintf(out, "  Status:  %s\n", formatOptimizeStatus(status.Status))
	if agentName := status.AgentName(); agentName != "" {
		fmt.Fprintf(out, "  Agent:   %s\n", agentName)
	}
	if status.AllTargetAttributesFailed {
		fmt.Fprintf(out, "  Strategy: %s\n", color.YellowString("failed (baseline only — no candidates generated)"))
	}
	if status.Progress != nil {
		fmt.Fprintf(out, "  Candidates Completed: %d\n", status.Progress.CandidatesCompleted)
	}
	if best := status.BestCandidate(); best != nil {
		fmt.Fprintf(out, "  Best:    %.2f\n", best.AvgScore)
	}
	if status.CreatedAt != 0 {
		fmt.Fprintf(out, "  Created: %s\n", eval_api.FormatTimestamp(status.CreatedAt))
	}
	if status.UpdatedAt != 0 {
		fmt.Fprintf(out, "  Updated: %s\n", eval_api.FormatTimestamp(status.UpdatedAt))
	}
	if status.CreatedAt != 0 && status.UpdatedAt != 0 && status.UpdatedAt > status.CreatedAt {
		duration := time.Duration(status.UpdatedAt-status.CreatedAt) * time.Second
		fmt.Fprintf(out, "  Duration: %s\n", duration)
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
