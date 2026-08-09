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

// jobSelector binds a command to one of the two generation collections.
//
// Required here, unlike on `generate`: an id alone does not say which
// collection to call, and the two share an id shape, so guessing would mean
// trying both and reporting whichever answered.
type jobSelector struct {
	dataset   bool
	evaluator bool
}

func (s *jobSelector) bind(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&s.dataset, "dataset", false, "Act on dataset generation jobs.")
	cmd.Flags().BoolVar(&s.evaluator, "evaluator", false, "Act on evaluator generation jobs.")
	cmd.MarkFlagsMutuallyExclusive("dataset", "evaluator")
	cmd.MarkFlagsOneRequired("dataset", "evaluator")
}

func (s *jobSelector) kind() jobKind {
	if s.evaluator {
		return evaluatorJobs
	}
	return datasetJobs
}

func newJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Inspect, cancel and delete generation jobs.",
		Long: "Inspect, cancel and delete generation jobs.\n\n" +
			"This is the resume path for `generate`: a job started with --no-wait, " +
			"or one whose client was interrupted, is reattached to here rather than " +
			"restarted.\n\n" +
			"Pass --dataset or --evaluator to say which generation to act on. " +
			"The two are separate service collections, so it is required.",
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
	sel := &jobSelector{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the project's generation jobs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := sel.kind()
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

	sel.bind(cmd)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newJobShowCommand() *cobra.Command {
	var endpointFlg string
	sel := &jobSelector{}

	cmd := &cobra.Command{
		Use:   "show <job-id>",
		Short: "Show a generation job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]
			kind := sel.kind()

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

	sel.bind(cmd)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newJobCancelCommand() *cobra.Command {
	var endpointFlg string
	sel := &jobSelector{}

	cmd := &cobra.Command{
		Use:   "cancel <job-id>",
		Short: "Cancel an in-flight generation job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]
			kind := sel.kind()

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

	sel.bind(cmd)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newJobDeleteCommand() *cobra.Command {
	var endpointFlg string
	sel := &jobSelector{}

	cmd := &cobra.Command{
		Use:   "delete <job-id>",
		Short: "Delete a generation job record.",
		Long: "Delete a generation job record.\n\n" +
			"The artifact the job produced is already registered as its own version " +
			"and is not affected.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]
			kind := sel.kind()

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

	sel.bind(cmd)
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
