// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azureaidataset/internal/pkg/gen_api"

	"github.com/spf13/cobra"
)

// The job group nests under `dataset` rather than sitting at the root, because
// generation runs as two independent long-running resources — one for datasets,
// one for evaluators — sharing no collection. The evaluator half lives in
// `azure.ai.evaluations`; a shared top-level `job show <id>` would have to guess
// which endpoint to call from an id prefix that is not a documented contract.

func newJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Inspect, cancel and delete dataset generation jobs.",
		Long: "Inspect, cancel and delete dataset generation jobs.\n\n" +
			"This is the resume path for `dataset generate`: a job started with " +
			"--no-wait, or one whose client was interrupted, is reattached to here " +
			"rather than restarted.",
	}
	cmd.AddCommand(
		newJobListCommand(),
		newJobShowCommand(),
		newJobCancelCommand(),
		newJobDeleteCommand(),
	)
	return cmd
}

func newJobListCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the project's dataset generation jobs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			dc, err := newDatasetContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer dc.Close()

			out, err := dc.genClient.ListDataGenerationJobs(ctx, ProjectEndpointAPIVersion)
			if err != nil {
				return fmt.Errorf("listing dataset generation jobs: %w", err)
			}
			jobs := out.Data

			if isJSON(cmd) {
				return emitJSONList(cmd.OutOrStdout(), jobs)
			}
			if len(jobs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No dataset generation jobs found.")
				return nil
			}
			table := make([][]string, 0, len(jobs))
			for _, j := range jobs {
				table = append(table, []string{j.ID, j.Status})
			}
			return emitTable(cmd.OutOrStdout(), []string{"JOB ID", "STATUS"}, table)
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newJobShowCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "show <job-id>",
		Short: "Show a dataset generation job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]

			ctx := cmd.Context()
			dc, err := newDatasetContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer dc.Close()

			job, err := dc.genClient.GetDataGenerationJob(ctx, jobID, ProjectEndpointAPIVersion)
			if err != nil {
				return jobLookupError(jobID, err)
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
		Short: "Cancel an in-flight dataset generation job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]

			ctx := cmd.Context()
			dc, err := newDatasetContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer dc.Close()

			canceled, err := dc.genClient.CancelDataGenerationJob(ctx, jobID, ProjectEndpointAPIVersion)
			if err != nil {
				return jobLookupError(jobID, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), canceled)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Cancelled dataset generation job %s (%s)\n",
				jobID, canceled.Status)
			return nil
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newJobDeleteCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "delete <job-id>",
		Short: "Delete a dataset generation job record.",
		Long: "Delete a dataset generation job record.\n\n" +
			"The dataset the job produced is already registered as its own version " +
			"and is not affected.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]

			ctx := cmd.Context()
			dc, err := newDatasetContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer dc.Close()

			if err := dc.genClient.DeleteDataGenerationJob(ctx, jobID, ProjectEndpointAPIVersion); err != nil {
				return jobLookupError(jobID, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), map[string]string{
					"id": jobID, "kind": "dataset", "status": "deleted",
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted dataset generation job %s\n", jobID)
			return nil
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// jobLookupError names the evaluator group, because the two job types share an
// id shape and reaching for the wrong one is the likely mistake.
func jobLookupError(jobID string, err error) error {
	if gen_api.IsNotFound(err) {
		return fmt.Errorf(
			"no dataset generation job %q in this project; if it generated an "+
				"evaluator, use `azd ai eval evaluator job` instead", jobID)
	}
	return fmt.Errorf("reading dataset generation job %s: %w", jobID, err)
}
