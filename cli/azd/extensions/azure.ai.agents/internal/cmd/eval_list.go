// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"sync"
	"text/tabwriter"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type evalListFlags struct {
	limit int
}

func newEvalListCommand() *cobra.Command {
	flags := &evalListFlags{limit: 10}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List evaluations for the current project.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			logCleanup := setupDebugLogging(cmd.Flags())
			defer logCleanup()
			return runEvalList(ctx, flags)
		},
	}
	cmd.Flags().IntVar(&flags.limit, "limit", 10, "Maximum number of evals to return")
	return cmd
}

// evalRunSummary holds the fetched run info for a single eval.
type evalRunSummary struct {
	runCount      int
	lastRunStatus string
}

func runEvalList(ctx context.Context, flags *evalListFlags) error {
	resolved, err := resolveEvalContext(ctx, evalContextOptions{})
	if err != nil {
		return err
	}
	defer resolved.azdClient.Close()

	// Load the active eval ID from the azd environment.
	var activeEvalID string
	if resolved.envName != "" {
		state := loadEvalState(ctx, resolved.azdClient, resolved.envName)
		activeEvalID = state.EvalID
	}

	resp, err := resolved.evalClient.ListOpenAIEvals(ctx, flags.limit, DefaultAgentAPIVersion)
	if err != nil {
		return fmt.Errorf("failed to list evals: %w", err)
	}

	items := resp.Data

	// Fetch run summaries in parallel for each eval.
	summaries := make([]evalRunSummary, len(items))
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Add(1)
		go func(idx int, evalID string) {
			defer wg.Done()
			runs, err := resolved.evalClient.ListOpenAIEvalRuns(ctx, evalID, 10, DefaultAgentAPIVersion)
			if err != nil || runs == nil {
				return
			}
			summaries[idx].runCount = len(runs.Data)
			if len(runs.Data) > 0 {
				summaries[idx].lastRunStatus = runs.Data[0].Status
			}
		}(i, item.ResolvedID())
	}
	wg.Wait()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  \tEval ID\tName\tStatus of last run\tRuns\tCreated by\tCreated on")
	fmt.Fprintln(w, "  \t-------\t----\t------------------\t----\t----------\t----------")
	for i, item := range items {
		marker := " "
		if item.ResolvedID() == activeEvalID {
			marker = "*"
		}
		name := item.Name
		if name == "" {
			name = item.ResolvedID()
		}
		status := padColorizedStatus(summaries[i].lastRunStatus)
		createdBy := item.CreatedBy
		createdOn := formatTimestamp(item.CreatedAt)

		fmt.Fprintf(w, "%s \t%s\t%s\t%s\t%d\t%s\t%s\n",
			marker,
			item.ResolvedID(),
			name,
			status,
			summaries[i].runCount,
			createdBy,
			createdOn,
		)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if activeEvalID != "" {
		fmt.Printf("\n* = active eval in current environment\n")
	}
	fmt.Printf("(showing %d — use --limit to change)\n", len(items))
	return nil
}

// padColorizedStatus returns a fixed-width colored status string so that
// tabwriter aligns columns correctly despite ANSI escape sequences.
func padColorizedStatus(status string) string {
	const statusWidth = 10 // wide enough for "Completed", "Cancelled", etc.
	label, colorFn := statusLabelAndColor(status)
	padded := fmt.Sprintf("%-*s", statusWidth, label)
	return colorFn(padded)
}

// statusLabelAndColor maps a raw status to a display label and color function.
func statusLabelAndColor(status string) (string, func(string, ...any) string) {
	switch status {
	case "completed":
		return "Completed", color.GreenString
	case "succeeded":
		return "Succeeded", color.GreenString
	case "failed":
		return "Failed", color.RedString
	case "cancelled", "canceled":
		return "Cancelled", color.YellowString
	case "running", "in_progress":
		return "Running", color.CyanString
	case "partial":
		return "Partial", color.YellowString
	case "":
		return "No runs", color.HiBlackString
	default:
		return status, fmt.Sprintf
	}
}
