// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// eval_list.go implements the "eval list" command, which lists recent
// evaluations for the current Foundry project with run counts and status.

package cmd

import (
	"context"
	"fmt"
	"os"
	"sync"
	"text/tabwriter"

	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opteval"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// evalListFlags holds CLI flags for the eval list command.
type evalListFlags struct {
	limit int // maximum number of evals to return
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
		state := opteval.LoadEvalState(ctx, resolved.azdClient, resolved.envName)
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
		createdOn := eval_api.FormatTimestamp(item.CreatedAt)

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
