// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

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
	// collect finishes a succeeded job: it writes the artifact and hands back
	// the catalog entry for it. This is the half `generate --no-wait` cannot do,
	// because it returns before the job has produced anything.
	collect func(
		ctx context.Context, ec *evalContext, job *eval_api.GenerationJob,
		baseDir, outputDir string, out io.Writer,
	) (*project.ArtifactRef, error)
	// outputDir is where this kind's artifact lands when the caller did not name
	// a directory of their own.
	outputDir string
	// addToCatalog records the collected artifact in the configuration, which is
	// the half that makes the artifact usable rather than just present on disk.
	addToCatalog func(*cobra.Command, string, *project.ArtifactRef) error
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
	collect: func(
		ctx context.Context, ec *evalContext, job *eval_api.GenerationJob,
		baseDir, outputDir string, out io.Writer,
	) (*project.ArtifactRef, error) {
		// No declared name: reattaching has only the job, so the service's own
		// name is what the file is called.
		return ec.collectDataset(ctx, job, "", baseDir, outputDir, out)
	},
	outputDir:    project.DefaultDatasetsDir,
	addToCatalog: addDatasetToCatalog,
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
	collect: func(
		_ context.Context, ec *evalContext, job *eval_api.GenerationJob,
		baseDir, outputDir string, out io.Writer,
	) (*project.ArtifactRef, error) {
		return ec.collectRubric(job, "", baseDir, outputDir, out)
	},
	outputDir:    project.DefaultEvaluatorsDir,
	addToCatalog: addEvaluatorToCatalog,
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
	// path locates the configuration an artifact is collected beside. Only
	// `show` collects, so only `show` registers it.
	path string
	// outputDir is where `show` writes the artifact. `generate` refuses this
	// flag alongside --no-wait because it returns before there is anything to
	// write, and points the caller here -- so here has to accept it.
	outputDir string
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

	shown, total, trimmed := trimForDisplay(a.cmd, jobs)
	if isJSON(a.cmd) {
		return emitJSONPage(a.cmd.OutOrStdout(), shown, &total, "")
	}
	if len(jobs) == 0 {
		fmt.Fprint(a.cmd.OutOrStdout(), messages.NoJobs(kind.name))
		return nil
	}
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
		Short: "Show a generation job, and collect its artifact once it has finished.",
		Long: "Show a generation job, and collect its artifact once it has finished.\n\n" +
			"`generate --no-wait` returns before the job has produced anything, so " +
			"the download and the catalog entry are left for this command. A job " +
			"still running is reported and nothing is written; a job that has " +
			"succeeded is completed here, and running it again is harmless.",
		Args: requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&jobShowAction{cmd: cmd, flags: flags, jobID: args[0]}).Run()
		},
	}

	flags.bind(cmd)
	// The artifact lands beside the configuration, so this command resolves it
	// the same way every other one does.
	addEvalPathFlag(cmd, &flags.path)
	cmd.Flags().StringVar(&flags.outputDir, "output-dir", "",
		"Directory the collected artifact is written to.")
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

	out := a.cmd.OutOrStdout()
	if !isJSON(a.cmd) {
		fmt.Fprint(out, messages.JobLine(job.ID, job.Status))
		if job.Error != nil && job.Error.Message != "" {
			fmt.Fprint(out, messages.JobErrorLine(job.Error.Message))
		}
	}

	// The half `generate --no-wait` could not do. Only a job that has finished
	// has anything to collect; one still running is reported above and left
	// alone, so this stays safe to run repeatedly while waiting.
	//
	// Collection narrates what it wrote, and under -o json that narration lands
	// on stdout ahead of the document and stops it parsing. The document says
	// the same things in fields, so the prose is dropped rather than moved.
	ref, collectErr := a.collect(ctx, ec, kind, job, humanOut(a.cmd, out))

	if isJSON(a.cmd) {
		// The job is still the document, with the artifact added when there was
		// one: a caller polling this has to be able to read both from one read.
		doc := map[string]any{"job": job}
		if ref != nil {
			doc[kind.name] = ref
		}
		if err := emitJSON(out, doc); err != nil {
			return err
		}
	}
	return collectErr
}

// collect finishes a job that has succeeded, and reports nothing for one that
// has not.
//
// Idempotent by construction: the artifact is written atomically over whatever
// was there, and the catalog entry is only added when the file does not already
// declare it, so a caller polling `job show` does not accumulate anything.
func (a *jobShowAction) collect(
	ctx context.Context,
	ec *evalContext,
	kind jobKind,
	job *eval_api.GenerationJob,
	out io.Writer,
) (*project.ArtifactRef, error) {
	if kind.collect == nil || !job.Succeeded() {
		return nil, nil
	}

	evalDir, err := ec.evalDir(ctx, a.flags.path)
	if err != nil {
		return nil, err
	}
	baseDir := project.EvalDirOf(evalDir)

	// An unset flag leaves the artifact in the kind's own directory, which is
	// where `generate` without --output-dir would have put it.
	outputDir := a.flags.outputDir
	if outputDir == "" {
		outputDir = kind.outputDir
	}

	ref, err := kind.collect(ctx, ec, job, baseDir, outputDir, out)
	if err != nil {
		return nil, err
	}
	if err := kind.addToCatalog(a.cmd, evalDir, ref); err != nil {
		return ref, err
	}
	return ref, nil
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
