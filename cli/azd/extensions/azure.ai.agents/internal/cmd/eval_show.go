// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// eval_show.go implements the "eval show" command, which displays eval
// definitions, run history, and per-criteria result breakdowns.

package cmd

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opt_eval"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// evalShowFlags holds CLI flags for the eval show command.
type evalShowFlags struct {
	envName   string // explicit environment name (from -e flag)
	evalRunID string // specific eval run to show
	limit     int    // maximum number of runs to display
	output    string // export results to JSON file
}

func newEvalShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &evalShowFlags{limit: 20}
	cmd := &cobra.Command{
		Use:   "show [eval-id]",
		Short: "Show an eval definition, run history, or run details.",
		Long: `Show an eval definition, run history, or run details.

If eval-id is omitted, the most recent eval from the current environment is used.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			logCleanup := setupDebugLogging(cmd.Flags())
			defer logCleanup()

			var evalID string
			if len(args) > 0 {
				evalID = args[0]
			}
			flags.envName = extCtx.Environment
			return runEvalShow(ctx, evalID, flags)
		},
	}
	cmd.Flags().StringVar(&flags.evalRunID, "eval-run-id", "", "Show details for a specific eval run")
	cmd.Flags().IntVar(&flags.limit, "limit", 20, "Maximum number of runs to show")
	cmd.Flags().StringVarP(&flags.output, "out-file", "O", "", "Export full run results to a JSON file")
	return cmd
}

func runEvalShow(ctx context.Context, evalID string, flags *evalShowFlags) error {
	resolved, err := resolveEvalContext(ctx, evalContextOptions{envName: flags.envName})
	if err != nil {
		return err
	}
	defer resolved.azdClient.Close()

	// Fall back to the eval ID stored in the azd environment.
	if evalID == "" && resolved.envName != "" {
		state := opt_eval.LoadEvalState(ctx, resolved.azdClient, resolved.envName)
		evalID = state.EvalID
	}
	if evalID == "" {
		return fmt.Errorf("no eval-id provided and none found in the current environment; run 'azd ai agent eval generate' first or pass an eval-id")
	}

	if flags.evalRunID != "" {
		run, err := resolved.evalClient.GetOpenAIEvalRun(ctx, evalID, flags.evalRunID)
		if err != nil {
			return fmt.Errorf("failed to get eval run: %w", err)
		}
		if flags.output != "" {
			return eval_api.WriteJSONFile(flags.output, run)
		}
		return printEvalRunSummary(evalID, run)
	}

	evalObj, err := resolved.evalClient.GetOpenAIEval(ctx, evalID)
	if err != nil {
		return fmt.Errorf("failed to get eval: %w", err)
	}
	runs, err := resolved.evalClient.ListOpenAIEvalRuns(ctx, evalID, flags.limit)
	if err != nil {
		return fmt.Errorf("failed to list eval runs: %w", err)
	}
	if flags.output != "" {
		return eval_api.WriteJSONFile(flags.output, map[string]any{
			"eval": evalObj,
			"runs": runs.Data,
		})
	}
	return printEvalSummary(evalObj, runs.Data, flags.limit)
}

func printEvalSummary(evalObj *eval_api.OpenAIEval, runs []eval_api.OpenAIEvalRun, limit int) error {
	fmt.Printf("Eval:       %s\n", evalObj.ResolvedID())
	if evalObj.Name != "" {
		fmt.Printf("Name:       %s\n", evalObj.Name)
	}
	if agent := evalObj.Metadata["azd_agent"]; agent != "" {
		fmt.Printf("Agent:      %s\n", agent)
	}
	fmt.Printf("Created:    %s\n", eval_api.FormatTimestamp(evalObj.CreatedAt))
	if evalObj.CreatedBy != "" {
		fmt.Printf("Created by: %s\n", evalObj.CreatedBy)
	}
	fmt.Printf("Runs:       %d\n\n", len(runs))
	fmt.Println("Recent runs:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  Run ID\tStatus\tPassed\tFailed\tCreated")
	fmt.Fprintln(w, "  ------\t------\t------\t------\t-------")
	for _, run := range runs {
		passed, failed := "", ""
		if run.ResultCounts != nil {
			passed = fmt.Sprintf("%d/%d", run.ResultCounts.Passed, run.ResultCounts.Total)
			failed = fmt.Sprintf("%d", run.ResultCounts.Failed)
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n",
			run.ID,
			colorizeStatus(run.Status),
			passed,
			failed,
			eval_api.FormatTimestamp(run.CreatedAt),
		)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Printf("\n(showing %d runs — use --limit to change)\n", min(limit, len(runs)))
	return nil
}

func printEvalRunSummary(evalID string, run *eval_api.OpenAIEvalRun) error {
	fmt.Printf("Eval:       %s\n", evalID)
	fmt.Printf("Run:        %s\n", run.ID)
	if run.Name != "" {
		fmt.Printf("Name:       %s\n", run.Name)
	}
	fmt.Printf("Status:     %s\n", colorizeStatus(run.Status))
	fmt.Printf("Created:    %s\n", eval_api.FormatTimestamp(run.CreatedAt))
	if run.CreatedBy != "" {
		fmt.Printf("Created by: %s\n", run.CreatedBy)
	}

	// Agent target info from data source.
	if run.DataSource != nil && run.DataSource.Target != nil {
		agent := run.DataSource.Target.Name
		if run.DataSource.Target.Version != nil {
			agent += " v" + *run.DataSource.Target.Version
		}
		fmt.Printf("Agent:      %s\n", agent)
	}

	// Result counts.
	if rc := run.ResultCounts; rc != nil {
		fmt.Printf("\nResults:    %d total, %s passed, %s failed, %s errored\n",
			rc.Total,
			color.GreenString("%d", rc.Passed),
			color.RedString("%d", rc.Failed),
			color.YellowString("%d", rc.Errored),
		)
	}

	// Per-criteria breakdown.
	if len(run.PerTestingCriteria) > 0 {
		fmt.Println("\nPer-criteria results:")
		for _, c := range run.PerTestingCriteria {
			fmt.Printf("  %s: %s passed, %s failed, %s errored\n",
				c.TestingCriteria,
				color.GreenString("%d", c.Passed),
				color.RedString("%d", c.Failed),
				color.YellowString("%d", c.Errored),
			)
		}
	}

	if run.ReportURL != "" {
		fmt.Printf("\nReport:     %s\n", run.ReportURL)
	}
	return nil
}
