// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
)

// addRunSubcommands attaches the atomic run operations.
//
// `azd ai eval run` stays the composite that creates the group if needed and
// starts a run; these expose the individual operations so every one is
// reachable without the config file.
func addRunSubcommands(cmd *cobra.Command) {
	cmd.AddCommand(
		newRunListCommand(),
		newRunShowCommand(),
		newRunCancelCommand(),
	)
}

func newRunListCommand() *cobra.Command {
	var (
		endpointFlg string
		groupName   string
	)

	cmd := &cobra.Command{
		Use:   "list [eval-id]",
		Short: "List runs for an eval group.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			evalID, err := resolveEvalID(cmd, ec, args, groupName)
			if err != nil {
				return err
			}

			list, err := ec.evalClient.ListOpenAIEvalRuns(ctx, evalID, 0)
			if err != nil {
				return fmt.Errorf("listing runs for %q: %w", evalID, err)
			}
			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), list)
			}
			if list == nil || len(list.Data) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Eval group %s has no runs yet.\n", evalID)
				return nil
			}

			rows := make([][]string, 0, len(list.Data))
			for _, run := range list.Data {
				rows = append(rows, []string{run.ID, run.Name, run.Status, summarizeCounts(run.ResultCounts)})
			}
			return emitTable(cmd.OutOrStdout(),
				[]string{"RUN ID", "NAME", "STATUS", "RESULTS"}, rows)
		},
	}
	addEvalGroupFlag(cmd, &groupName)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newRunShowCommand() *cobra.Command {
	var (
		runID       string
		endpointFlg string
		groupName   string
	)

	cmd := &cobra.Command{
		Use:   "show [eval-id]",
		Short: "Show a single run.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			evalID, err := resolveEvalID(cmd, ec, args, groupName)
			if err != nil {
				return err
			}

			run, err := ec.latestOrNamedRun(cmd, evalID, runID)
			if err != nil {
				return err
			}
			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), run)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Run %s\n", run.ID)
			fmt.Fprintf(out, "  name    : %s\n", run.Name)
			fmt.Fprintf(out, "  status  : %s\n", run.Status)
			if counts := summarizeCounts(run.ResultCounts); counts != "" {
				fmt.Fprintf(out, "  results : %s\n", counts)
			}
			if run.ReportURL != "" {
				fmt.Fprintf(out, "  report  : %s\n", run.ReportURL)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&runID, "run-id", "", "Run to show. Defaults to the most recent run.")
	addEvalGroupFlag(cmd, &groupName)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newRunCancelCommand() *cobra.Command {
	var (
		runID       string
		endpointFlg string
		groupName   string
	)

	cmd := &cobra.Command{
		Use:   "cancel [eval-id]",
		Short: "Cancel an in-flight run.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			evalID, err := resolveEvalID(cmd, ec, args, groupName)
			if err != nil {
				return err
			}

			target, err := ec.latestOrNamedRun(cmd, evalID, runID)
			if err != nil {
				return err
			}
			// Cancelling a run that already finished is a no-op worth naming,
			// since the service reports success either way.
			if terminalRunStates[target.Status] {
				return fmt.Errorf("run %s already finished with status %q",
					target.ID, target.Status)
			}

			canceled, err := ec.evalClient.CancelOpenAIEvalRun(ctx, evalID, target.ID)
			if err != nil {
				return fmt.Errorf("cancelling run %s: %w", target.ID, err)
			}
			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), canceled)
			}
			status := canceled.Status
			if status == "" {
				status = "cancelling"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Run %s is now %s\n", target.ID, status)
			return nil
		},
	}
	cmd.Flags().StringVar(&runID, "run-id", "", "Run to cancel. Defaults to the most recent run.")
	addEvalGroupFlag(cmd, &groupName)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func summarizeCounts(counts *eval_api.EvalRunResultCounts) string {
	if counts == nil {
		return ""
	}
	return fmt.Sprintf("%d passed, %d failed, %d errored",
		counts.Passed, counts.Failed, counts.Errored)
}
