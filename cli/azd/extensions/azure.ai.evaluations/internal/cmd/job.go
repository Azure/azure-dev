// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
)

// Generation runs as two independent long-running resources — one for datasets,
// one for evaluators — and a job id does not say which it came from. Rather
// than make the caller remember, every command here tries both.

const (
	jobKindDataset   = "dataset"
	jobKindEvaluator = "evaluator"
)

func newJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Inspect and cancel generation jobs.",
		Long: "Inspect and cancel generation jobs.\n\n" +
			"This is the resume path for `dataset generate` and `evaluator generate`: " +
			"a job started with --no-wait, or one whose client was interrupted, is " +
			"reattached to here rather than restarted.",
	}
	cmd.AddCommand(
		newJobListCommand(),
		newJobShowCommand(),
		newJobCancelCommand(),
	)
	return cmd
}

func newJobListCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the project's generation jobs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			datasets, err := ec.evalClient.ListDataGenerationJobs(ctx, ProjectEndpointAPIVersion)
			if err != nil {
				return fmt.Errorf("listing dataset generation jobs: %w", err)
			}
			evaluators, err := ec.evalClient.ListEvaluatorGenerationJobs(ctx, ProjectEndpointAPIVersion)
			if err != nil {
				return fmt.Errorf("listing evaluator generation jobs: %w", err)
			}

			type jobRow struct {
				ID     string `json:"id"`
				Kind   string `json:"kind"`
				Status string `json:"status"`
			}
			rows := make([]jobRow, 0, len(datasets.Data)+len(evaluators.Data))
			for _, j := range datasets.Data {
				rows = append(rows, jobRow{ID: j.ID, Kind: jobKindDataset, Status: j.Status})
			}
			for _, j := range evaluators.Data {
				rows = append(rows, jobRow{ID: j.ID, Kind: jobKindEvaluator, Status: j.Status})
			}

			if isJSON(cmd) {
				return emitJSONList(cmd.OutOrStdout(), rows)
			}
			if len(rows) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No generation jobs found.")
				return nil
			}
			table := make([][]string, 0, len(rows))
			for _, r := range rows {
				table = append(table, []string{r.ID, r.Kind, r.Status})
			}
			return emitTable(cmd.OutOrStdout(), []string{"JOB ID", "KIND", "STATUS"}, table)
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newJobShowCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "show <job-id>",
		Short: "Show a generation job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			job, _, err := findGenerationJob(ctx, ec, jobID)
			if err != nil {
				return err
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), job)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", job.ID, job.Status)
			if job.Error != nil && job.Error.Message != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "error: %s\n", job.Error.Message)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newJobCancelCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "cancel <job-id>",
		Short: "Cancel an in-flight generation job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			_, kind, err := findGenerationJob(ctx, ec, jobID)
			if err != nil {
				return err
			}

			var canceled *eval_api.GenerationJob
			if kind == jobKindDataset {
				canceled, err = ec.evalClient.CancelDataGenerationJob(
					ctx, jobID, ProjectEndpointAPIVersion)
			} else {
				canceled, err = ec.evalClient.CancelEvaluatorGenerationJob(
					ctx, jobID, ProjectEndpointAPIVersion)
			}
			if err != nil {
				return fmt.Errorf("cancelling job %s: %w", jobID, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), canceled)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Cancelled %s generation job %s (%s)\n",
				kind, jobID, canceled.Status)
			return nil
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// findGenerationJob resolves an id against both job types and reports which one
// answered, so that a caller never has to know which command started it.
func findGenerationJob(
	ctx context.Context,
	ec *evalContext,
	jobID string,
) (*eval_api.GenerationJob, string, error) {
	if job, err := ec.evalClient.GetDataGenerationJob(
		ctx, jobID, ProjectEndpointAPIVersion,
	); err == nil {
		return job, jobKindDataset, nil
	}
	if job, err := ec.evalClient.GetEvaluatorGenerationJob(
		ctx, jobID, ProjectEndpointAPIVersion,
	); err == nil {
		return job, jobKindEvaluator, nil
	}
	return nil, "", fmt.Errorf(
		"no generation job %s in this project; "+
			"`azd ai eval job list` shows the ones there are", jobID)
}
