// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// optimize_list.go implements the "optimize list" command, which lists
// recent optimization jobs with status, agent, and score.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// optimizeListFlags holds CLI flags for the optimize list command.
type optimizeListFlags struct {
	envName string // explicit environment name (from -e flag)
	output  string
	limit   int    // maximum number of results
	status  string // filter by job status
	optimizeConnectionFlags
}

func newOptimizeListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &optimizeListFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent optimization runs.",
		Long: `List recent optimization runs.

Use --status to filter by job status and --limit to control page size.`,
		Example: `  # List all recent runs
  azd ai agent optimize list

  # List only successful runs
  azd ai agent optimize list --status succeeded

  # Show last 5 runs
  azd ai agent optimize list --limit 5`,
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.envName = extCtx.Environment
			flags.output = extCtx.OutputFormat
			return runOptimizeList(cmd, flags)
		},
	}

	cmd.Flags().IntVar(&flags.limit, "limit", 20, "Maximum number of results")
	cmd.Flags().StringVar(&flags.status, "status", "",
		"Filter by status (queued/in_progress/succeeded/failed/cancelled/skipped)")
	flags.optimizeConnectionFlags.register(cmd)
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})

	return cmd
}

func runOptimizeList(cmd *cobra.Command, flags *optimizeListFlags) error {
	if flags.limit < 1 || flags.limit > 100 {
		return fmt.Errorf("--limit must be between 1 and 100")
	}
	// Validate --status flag before making API call
	if flags.status != "" {
		valid := map[string]bool{
			"queued": true, "in_progress": true, "succeeded": true, "failed": true, "cancelled": true, "skipped": true,
		}
		if !valid[flags.status] {
			return fmt.Errorf("invalid --status %q: "+
				"must be one of queued, in_progress, succeeded, failed, cancelled, skipped",
				flags.status)
		}
	}

	endpoint, err := flags.resolve(cmd.Context(), flags.envName)
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
		return fmt.Errorf("failed to list optimization jobs: %w\n\nCheck that the project endpoint is reachable", err)
	}

	out := cmd.OutOrStdout()
	if flags.output == "json" {
		return json.NewEncoder(out).Encode(listResp)
	}

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

	_, _ = bold.Fprintf(out, "  %-38s %-12s %-14s   %s\n", "ID", "Status", "Agent", "Created")
	fmt.Fprintf(out, "  %-38s %-12s %-14s   %s\n",
		strings.Repeat("─", 38), strings.Repeat("─", 12),
		strings.Repeat("─", 14), strings.Repeat("─", 19))

	for _, job := range jobs {
		agentName := "—"
		if name := job.AgentName(); name != "" {
			agentName = name
		}

		created := "—"
		if job.CreatedAt != 0 {
			created = formatOptimizeTimestamp(job.CreatedAt)
		}

		fmt.Fprintf(out, "  %-38s %-12s %-14s   %s\n",
			job.ID,
			formatOptimizeStatus(job.Status),
			truncateString(agentName, 14),
			truncateString(created, 19),
		)
	}
	fmt.Fprintln(out)
}
