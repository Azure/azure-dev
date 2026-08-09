// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
)

// Generation runs as two independent long-running resources — one for datasets,
// one for evaluators — sharing no collection. A job group therefore nests under
// the resource that produced it: a top-level `job show <id>` would have to guess
// the endpoint from an id prefix that is not a documented contract.

const (
	jobKindDataset   = "dataset"
	jobKindEvaluator = "evaluator"
)

// jobKind binds a group to one generation resource, so every command under it
// calls one endpoint rather than trying both and reporting whichever answered.
type jobKind struct {
	name   string
	list   func(context.Context, *evalContext) ([]eval_api.GenerationJob, error)
	get    func(context.Context, *evalContext, string) (*eval_api.GenerationJob, error)
	cancel func(context.Context, *evalContext, string) (*eval_api.GenerationJob, error)
	remove func(context.Context, *evalContext, string) error
}

var datasetJobs = jobKind{
	name: jobKindDataset,
	list: func(ctx context.Context, ec *evalContext) ([]eval_api.GenerationJob, error) {
		out, err := ec.evalClient.ListDataGenerationJobs(ctx, ProjectEndpointAPIVersion)
		if err != nil {
			return nil, err
		}
		return out.Data, nil
	},
	get: func(ctx context.Context, ec *evalContext, id string) (*eval_api.GenerationJob, error) {
		return ec.evalClient.GetDataGenerationJob(ctx, id, ProjectEndpointAPIVersion)
	},
	cancel: func(ctx context.Context, ec *evalContext, id string) (*eval_api.GenerationJob, error) {
		return ec.evalClient.CancelDataGenerationJob(ctx, id, ProjectEndpointAPIVersion)
	},
	remove: func(ctx context.Context, ec *evalContext, id string) error {
		return ec.evalClient.DeleteDataGenerationJob(ctx, id, ProjectEndpointAPIVersion)
	},
}

var evaluatorJobs = jobKind{
	name: jobKindEvaluator,
	list: func(ctx context.Context, ec *evalContext) ([]eval_api.GenerationJob, error) {
		out, err := ec.evalClient.ListEvaluatorGenerationJobs(ctx, ProjectEndpointAPIVersion)
		if err != nil {
			return nil, err
		}
		return out.Data, nil
	},
	get: func(ctx context.Context, ec *evalContext, id string) (*eval_api.GenerationJob, error) {
		return ec.evalClient.GetEvaluatorGenerationJob(ctx, id, ProjectEndpointAPIVersion)
	},
	cancel: func(ctx context.Context, ec *evalContext, id string) (*eval_api.GenerationJob, error) {
		return ec.evalClient.CancelEvaluatorGenerationJob(ctx, id, ProjectEndpointAPIVersion)
	},
	remove: func(ctx context.Context, ec *evalContext, id string) error {
		return ec.evalClient.DeleteEvaluatorGenerationJob(ctx, id, ProjectEndpointAPIVersion)
	},
}

func newJobCommand(kind jobKind) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: fmt.Sprintf("Inspect, cancel and delete %s generation jobs.", kind.name),
		Long: fmt.Sprintf("Inspect, cancel and delete %s generation jobs.\n\n", kind.name) +
			fmt.Sprintf("This is the resume path for `%s generate`: a job started with ", kind.name) +
			"--no-wait, or one whose client was interrupted, is reattached to here " +
			"rather than restarted.",
	}
	cmd.AddCommand(
		newJobListCommand(kind),
		newJobShowCommand(kind),
		newJobCancelCommand(kind),
		newJobDeleteCommand(kind),
	)
	return cmd
}

func newJobListCommand(kind jobKind) *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "list",
		Short: fmt.Sprintf("List the project's %s generation jobs.", kind.name),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			jobs, err := kind.list(ctx, ec)
			if err != nil {
				return messages.ListingJobs(kind.name, err)
			}

			if isJSON(cmd) {
				return emitJSONList(cmd.OutOrStdout(), jobs)
			}
			if len(jobs) == 0 {
				fmt.Fprint(cmd.OutOrStdout(), messages.NoJobs(kind.name))
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

func newJobShowCommand(kind jobKind) *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "show <job-id>",
		Short: fmt.Sprintf("Show a %s generation job.", kind.name),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			job, err := kind.get(ctx, ec, jobID)
			if err != nil {
				return jobLookupError(kind, jobID, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), job)
			}
			fmt.Fprint(cmd.OutOrStdout(), messages.JobLine(job.ID, job.Status))
			if job.Error != nil && job.Error.Message != "" {
				fmt.Fprint(cmd.OutOrStdout(), messages.JobErrorLine(job.Error.Message))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newJobCancelCommand(kind jobKind) *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "cancel <job-id>",
		Short: fmt.Sprintf("Cancel an in-flight %s generation job.", kind.name),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			canceled, err := kind.cancel(ctx, ec, jobID)
			if err != nil {
				return jobLookupError(kind, jobID, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), canceled)
			}
			fmt.Fprint(cmd.OutOrStdout(),
				messages.JobCancelled(kind.name, jobID, canceled.Status))
			return nil
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newJobDeleteCommand(kind jobKind) *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "delete <job-id>",
		Short: fmt.Sprintf("Delete a %s generation job record.", kind.name),
		Long: fmt.Sprintf("Delete a %s generation job record.\n\n", kind.name) +
			"The artifact the job produced is already registered as its own version " +
			"and is not affected.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			if err := kind.remove(ctx, ec, jobID); err != nil {
				return jobLookupError(kind, jobID, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), map[string]string{
					"id": jobID, "kind": kind.name, "status": "deleted",
				})
			}
			fmt.Fprint(cmd.OutOrStdout(), messages.JobDeleted(kind.name, jobID))
			return nil
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// jobLookupError names the sibling group, because the two job types share an id
// shape and reaching for the wrong one is the likely mistake.
func jobLookupError(kind jobKind, jobID string, err error) error {
	if eval_api.IsNotFound(err) {
		other := jobKindEvaluator
		if kind.name == jobKindEvaluator {
			other = jobKindDataset
		}
		return messages.JobNotFound(kind.name, jobID, other)
	}
	return messages.ReadingJob(kind.name, jobID, err)
}
