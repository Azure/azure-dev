// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// optimize_list.go implements the "optimize list" command, which lists
// recent optimization jobs with status, agent, and score.

package cmd

import (
	"fmt"
	"io"
	"strings"

	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// optimizeListFlags holds CLI flags for the optimize list command.
type optimizeListFlags struct {
	limit  int    // maximum number of results
	status string // filter by job status
	optimizeConnectionFlags
}

func newOptimizeListCommand() *cobra.Command {
	flags := &optimizeListFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent optimization runs.",
		Long: `List recent optimization and evaluation runs.

Use --status to filter by job status and --limit to control page size.`,
		Example: `  # List all recent runs
  azd ai agent optimize list

  # List only completed runs
  azd ai agent optimize list --status completed

  # Show last 5 runs
  azd ai agent optimize list --limit 5`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOptimizeList(cmd, flags)
		},
	}

	cmd.Flags().IntVar(&flags.limit, "limit", 20, "Maximum number of results")
	cmd.Flags().StringVar(&flags.status, "status", "", "Filter by status (pending/running/completed/failed/cancelled)")
	flags.optimizeConnectionFlags.register(cmd)

	return cmd
}

func runOptimizeList(cmd *cobra.Command, flags *optimizeListFlags) error {
	// Validate --status flag before making API call
	if flags.status != "" {
		valid := map[string]bool{"pending": true, "running": true, "completed": true, "failed": true, "cancelled": true}
		if !valid[flags.status] {
			return fmt.Errorf("invalid --status %q: must be one of pending, running, completed, failed, cancelled", flags.status)
		}
	}

	endpoint, err := flags.resolve(cmd.Context())
	if err != nil {
		return err
	}

	credential, err := newAgentCredential()
	if err != nil {
		return err
	}

	client := optimize_api.NewOptimizeClient(endpoint, credential)

	listResp, err := client.ListOptimizeJobs(cmd.Context(), flags.limit, flags.status)
	if err != nil {
		return fmt.Errorf("failed to list optimization jobs: %w\n\nCheck that the endpoint %q is reachable", err, endpoint)
	}

	out := cmd.OutOrStdout()

	if len(listResp.Data) == 0 {
		fmt.Fprintln(out, "  No optimization jobs found.")
		if flags.status != "" {
			fmt.Fprintf(out, "\n  Try removing the --status filter or run a new job with:\n")
			fmt.Fprintf(out, "    azd ai agent optimize --config spec.yaml\n")
		}
		return nil
	}

	printOptimizeListTable(out, listResp.Data)
	return nil
}

func printOptimizeListTable(out io.Writer, jobs []optimize_api.OptimizeJobStatus) {
	bold := color.New(color.Bold)

	_, _ = bold.Fprintf(out, "  %-38s %-12s %-14s %7s   %s\n", "ID", "Status", "Agent", "Score", "Created")
	fmt.Fprintf(out, "  %-38s %-12s %-14s %7s   %s\n",
		strings.Repeat("─", 38), strings.Repeat("─", 12),
		strings.Repeat("─", 14), strings.Repeat("─", 7), strings.Repeat("─", 19))

	for _, job := range jobs {
		scoreStr := "—"
		if job.Best != nil {
			scoreStr = fmt.Sprintf("%.2f", job.Best.AvgScore)
		}

		agentName := "—"
		if job.Agent != nil && job.Agent.AgentName != "" {
			agentName = job.Agent.AgentName
		}

		created := job.CreatedAt
		if created == "" {
			created = "—"
		}

		fmt.Fprintf(out, "  %-38s %-12s %-14s %7s   %s\n",
			job.OperationID,
			formatOptimizeStatus(job.Status),
			truncateString(agentName, 14),
			scoreStr,
			truncateString(created, 19),
		)
	}
	fmt.Fprintln(out)
}
