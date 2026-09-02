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

// Data generation is the one collection on its own API version, so the job
// commands have to ask for it the same way generate does. Evaluator generation
// is on the project endpoint version, which is why only these four differ.
var datasetJobs = jobKind{
	name: jobKindDataset,
	list: func(ctx context.Context, ec *evalContext) ([]eval_api.GenerationJob, error) {
		out, err := ec.evalClient.ListDataGenerationJobs(ctx, DataGenerationAPIVersion)
		if err != nil {
			return nil, err
		}
		return out.Data, nil
	},
	get: func(ctx context.Context, ec *evalContext, id string) (*eval_api.GenerationJob, error) {
		return ec.evalClient.GetDataGenerationJob(ctx, id, DataGenerationAPIVersion)
	},
	cancel: func(ctx context.Context, ec *evalContext, id string) (*eval_api.GenerationJob, error) {
		return ec.evalClient.CancelDataGenerationJob(ctx, id, DataGenerationAPIVersion)
	},
	remove: func(ctx context.Context, ec *evalContext, id string) error {
		return ec.evalClient.DeleteDataGenerationJob(ctx, id, DataGenerationAPIVersion)
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
	// "Required." leads, because the same two flag names are optional filters
	// one command over on `generate`, and the help is the only thing that says
	// which meaning applies here.
	cmd.Flags().BoolVar(&s.dataset, "dataset", false,
		"Required (or --evaluator). Act on dataset generation jobs.")
	cmd.Flags().BoolVar(&s.evaluator, "evaluator", false,
		"Required (or --dataset). Act on evaluator generation jobs.")
	cmd.MarkFlagsMutuallyExclusive("dataset", "evaluator")
	cmd.MarkFlagsOneRequired("dataset", "evaluator")
}

// kind resolves the selector. Total rather than defaulting: cobra enforces
// that one flag is set, and if that enforcement is ever dropped a silent
// default would query the wrong collection and report "not found".
func (s *jobSelector) kind() (jobKind, error) {
	switch {
	case s.dataset && !s.evaluator:
		return datasetJobs, nil
	case s.evaluator && !s.dataset:
		return evaluatorJobs, nil
	default:
		return jobKind{}, messages.JobKindRequired()
	}
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

// jobFlags are the flags every job command takes: which of the two generation
// collections to act on, and where to reach it.
type jobFlags struct {
	sel      jobSelector
	endpoint string
}

// bind registers them together, so a command cannot declare one and forget
// the other.
func (f *jobFlags) bind(cmd *cobra.Command) {
	f.sel.bind(cmd)
	cmd.Flags().StringVar(&f.endpoint, "project-endpoint", "", "Foundry project endpoint.")
}

// jobListFlags carries what `job list` was asked for.
type jobListFlags struct {
	jobFlags
	displayLimit int
	showAll      bool
}

// jobListAction lists the project's generation jobs.
type jobListAction struct {
	cmd   *cobra.Command
	flags *jobListFlags
}

func newJobListCommand() *cobra.Command {
	flags := &jobListFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the project's generation jobs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&jobListAction{cmd: cmd, flags: flags}).Run()
		},
	}

	flags.bind(cmd)
	addDisplayPagingFlags(cmd, &flags.displayLimit, &flags.showAll, defaultPageSize)
	return cmd
}

func (a *jobListAction) Run() error {
	kind, err := a.flags.sel.kind()
	if err != nil {
		return err
	}
	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.flags.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	jobs, err := kind.list(ctx, ec)
	if err != nil {
		return messages.ListingJobs(kind.name, err)
	}

	if isJSON(a.cmd) {
		return emitJSONList(a.cmd.OutOrStdout(), jobs)
	}
	if len(jobs) == 0 {
		fmt.Fprint(a.cmd.OutOrStdout(), messages.NoJobs(kind.name))
		return nil
	}
	shown, total, trimmed := trimForDisplay(a.cmd, jobs)
	table := make([][]string, 0, len(shown))
	for _, j := range shown {
		table = append(table, []string{j.ID, j.Status})
	}
	if err := emitTable(a.cmd.OutOrStdout(), []string{"JOB ID", "STATUS"}, table); err != nil {
		return err
	}
	if trimmed {
		fmt.Fprint(a.cmd.OutOrStdout(), messages.ShowingSomeOf(len(table), total))
	}
	return nil
}

// jobShowAction reads one generation job.
type jobShowAction struct {
	cmd   *cobra.Command
	flags *jobFlags
	jobID string
}

func newJobShowCommand() *cobra.Command {
	flags := &jobFlags{}

	cmd := &cobra.Command{
		Use:   "show <job-id>",
		Short: "Show a generation job.",
		Args:  requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&jobShowAction{cmd: cmd, flags: flags, jobID: args[0]}).Run()
		},
	}

	flags.bind(cmd)
	return cmd
}

func (a *jobShowAction) Run() error {
	kind, err := a.flags.sel.kind()
	if err != nil {
		return err
	}

	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.flags.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	job, err := kind.get(ctx, ec, a.jobID)
	if err != nil {
		return jobLookupError("reading", kind, a.jobID, err)
	}

	if isJSON(a.cmd) {
		return emitJSON(a.cmd.OutOrStdout(), job)
	}
	fmt.Fprint(a.cmd.OutOrStdout(), messages.JobLine(job.ID, job.Status))
	if job.Error != nil && job.Error.Message != "" {
		fmt.Fprint(a.cmd.OutOrStdout(), messages.JobErrorLine(job.Error.Message))
	}
	return nil
}

// jobCancelAction cancels an in-flight generation job.
type jobCancelAction struct {
	cmd   *cobra.Command
	flags *jobFlags
	jobID string
}

func newJobCancelCommand() *cobra.Command {
	flags := &jobFlags{}

	cmd := &cobra.Command{
		Use:   "cancel <job-id>",
		Short: "Cancel an in-flight generation job.",
		Args:  requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&jobCancelAction{cmd: cmd, flags: flags, jobID: args[0]}).Run()
		},
	}

	flags.bind(cmd)
	return cmd
}

func (a *jobCancelAction) Run() error {
	kind, err := a.flags.sel.kind()
	if err != nil {
		return err
	}

	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.flags.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	canceled, err := kind.cancel(ctx, ec, a.jobID)
	if err != nil {
		return jobLookupError("cancelling", kind, a.jobID, err)
	}

	if isJSON(a.cmd) {
		return emitJSON(a.cmd.OutOrStdout(), canceled)
	}
	fmt.Fprint(a.cmd.OutOrStdout(), messages.JobCancelled(kind.name, a.jobID, canceled.Status))
	return nil
}

// jobDeleteAction removes one generation job record.
type jobDeleteAction struct {
	cmd   *cobra.Command
	flags *jobFlags
	jobID string
}

func newJobDeleteCommand() *cobra.Command {
	flags := &jobFlags{}

	cmd := &cobra.Command{
		Use:   "delete <job-id>",
		Short: "Delete a generation job record.",
		Long: "Delete a generation job record.\n\n" +
			"The artifact the job produced is already registered as its own version " +
			"and is not affected.",
		Args: requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&jobDeleteAction{cmd: cmd, flags: flags, jobID: args[0]}).Run()
		},
	}

	flags.bind(cmd)
	return cmd
}

func (a *jobDeleteAction) Run() error {
	kind, err := a.flags.sel.kind()
	if err != nil {
		return err
	}

	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.flags.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	if err := kind.remove(ctx, ec, a.jobID); err != nil {
		return jobLookupError("deleting", kind, a.jobID, err)
	}

	if isJSON(a.cmd) {
		return emitJSON(a.cmd.OutOrStdout(), map[string]string{
			"id": a.jobID, "kind": kind.name, "status": "deleted",
		})
	}
	fmt.Fprint(a.cmd.OutOrStdout(), messages.JobDeleted(kind.name, a.jobID))
	return nil
}

// jobLookupError names the sibling group, because the two job types share an id
// shape and reaching for the wrong one is the likely mistake.
//
// action is what the caller was doing, so a failed delete does not report that
// a read failed.
func jobLookupError(action string, kind jobKind, jobID string, err error) error {
	if eval_api.IsNotFound(err) {
		other := jobKindEvaluator
		if kind.name == jobKindEvaluator {
			other = jobKindDataset
		}
		return messages.JobNotFound(kind.name, jobID, other)
	}
	return messages.JobActionFailed(action, kind.name, jobID, err)
}
