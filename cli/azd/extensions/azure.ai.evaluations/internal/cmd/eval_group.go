// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/spf13/cobra"
)

// Creation normally belongs to `azd up`, which owns reconciliation. `create`
// is the same path for a single eval outside a project, and takes the
// configuration rather than a wall of flags so there is never a second
// definition to maintain.

// evalCreateFlags carries what `eval create` was asked for.
type evalCreateFlags struct {
	fromFile string
	evalDir  string
	endpoint string
}

// evalCreateAction creates one declared eval without deploying the rest.
type evalCreateAction struct {
	cmd   *cobra.Command
	flags *evalCreateFlags
	name  string
}

// newEvalCreateCommand creates one declared eval without deploying the rest.
func newEvalCreateCommand() *cobra.Command {
	flags := &evalCreateFlags{}

	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create one eval declared in the configuration.",
		Long: "Create one eval declared in the configuration.\n\n" +
			"`azd up` reconciles every eval in the file. This creates a single one, " +
			"for a project that is not deployed as a whole — or, with --from-file, " +
			"for no project at all.\n\n" +
			"The name is optional while the configuration declares exactly one eval.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&evalCreateAction{cmd: cmd, flags: flags, name: firstArg(args)}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.fromFile, "from-file", "",
		"Read the configuration from this path instead of the eval directory.")
	cmd.Flags().StringVar(&flags.evalDir, "path", "",
		"Directory holding the evaluation configuration. Defaults to the directory "+
			"`init` scaffolded, otherwise ./evals.")
	cmd.Flags().StringVar(&flags.endpoint, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *evalCreateAction) Run() error {
	ctx := a.cmd.Context()

	path := a.flags.fromFile
	if path == "" {
		dir, err := resolveEvalDir(ctx, a.flags.evalDir)
		if err != nil {
			return err
		}
		if path, err = project.ResolveEvalConfigPath(dir); err != nil {
			return err
		}
	}
	cfg, err := project.LoadEvalConfig(path)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	eval, err := cfg.Eval(chooseEval(a.cmd, cfg, a.name))
	if err != nil {
		return err
	}

	ec, err := newEvalContext(ctx, a.flags.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	// Local sources resolve against the file, not the working directory,
	// so the columns are read from where the declaration points.
	baseDir := filepath.Dir(path)
	datasetPath := ""
	if decl, ok := cfg.DatasetDeclaration(eval.Dataset); ok {
		datasetPath = project.ResolveSource(baseDir, decl.File)
	}

	reconciler := &evalReconciler{ec: ec}
	// Every eval the file declares, not only the one being created: an
	// eval another declaration already owns must not be adopted here.
	reconciler.ReserveDeclared(ctx, cfg.Evals)
	out := a.cmd.OutOrStdout()

	// Before anything is pushed. Publishing is not free -- a dataset
	// version is immutable and the number climbs on every attempt -- so
	// a declaration the evaluators cannot satisfy is refused first.
	if err := checkEvaluatorRequirements(eval, ec.evaluatorSchemas(ctx)); err != nil {
		return err
	}
	// Reported per artifact, because "publishes nothing when nothing
	// changed" is the contract a reader is checking here and a single
	// closing line cannot show it. Silent under -o json.
	say := func(kind, name, version string, changed bool) {
		if isJSON(a.cmd) {
			return
		}
		if changed {
			fmt.Fprintln(out, messages.PublishedVersion(kind, name, version))
		} else {
			fmt.Fprintln(out, messages.UnchangedAtVersion(kind, name, version))
		}
	}

	// The eval names its dataset and evaluators, and the service resolves
	// those names when the eval is created, so they have to be published
	// first. `azd up` reconciles the whole file; this reconciles only what
	// this eval refers to, which is also what makes a rubric edit reach
	// the service without a full deploy.
	if decl, ok := cfg.DatasetDeclaration(eval.Dataset); ok {
		version, changed, err := reconciler.EnsureDataset(ctx, *decl, datasetPath)
		if err != nil {
			return messages.DatasetProblem(decl.Name, err)
		}
		say("dataset", decl.Name, version, changed)
	}
	for _, ref := range eval.Evaluators {
		decl, ok := cfg.EvaluatorDeclaration(ref.Evaluator)
		// A built-in, or one already registered, has nothing local to publish.
		if !ok || !decl.CarriesItsRubric() {
			continue
		}
		// A rubric written out in the configuration has no file to read.
		local := ""
		if decl.Source != "" {
			local = project.ResolveSource(baseDir, decl.Source)
		}
		version, changed, err := reconciler.EnsureEvaluator(ctx, *decl, local)
		if err != nil {
			return messages.EvaluatorProblem(decl.Name, err)
		}
		say("evaluator", decl.Name, version, changed)
	}

	id, created, err := reconciler.EnsureEval(ctx, *eval, datasetPath)
	if err != nil {
		return err
	}

	if isJSON(a.cmd) {
		return emitJSON(a.cmd.OutOrStdout(), map[string]string{
			"id": id, "name": eval.Name,
		})
	}
	if created {
		fmt.Fprint(out, messages.EvalCreated(eval.Name, id))
	} else {
		fmt.Fprint(out, messages.EvalUnchanged(eval.Name, id))
	}
	return nil
}

// evalListFlags carries what `eval list` was asked for.
type evalListFlags struct {
	limit      int
	nameFilter string
	pageToken  string
	all        bool
	endpoint   string
}

// evalListAction lists the project's evals.
type evalListAction struct {
	cmd   *cobra.Command
	flags *evalListFlags
}

func newEvalListCommand() *cobra.Command {
	flags := &evalListFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the project's evals, a page at a time.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&evalListAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.nameFilter, "name", "",
		"Only evals whose name contains this, compared without case. Searches every page.")
	addPagingFlags(cmd, &flags.limit, &flags.pageToken, &flags.all, defaultPageSize)
	cmd.Flags().StringVar(&flags.endpoint, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *evalListAction) Run() error {
	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.flags.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	pageSize := pageSizeOr(a.flags.limit, a.flags.all, defaultPageSize)

	// A filter that only searched the page in hand would answer "no
	// such eval" for one sitting on the next, which is the opposite of
	// what it is for. The service filters nothing, so finding a name
	// costs the full walk; reading a page does not.
	var page *eval_api.OpenAIEvalList
	if a.flags.nameFilter != "" || a.flags.all {
		page, err = ec.evalClient.ListOpenAIEvals(ctx, pageSize)
	} else {
		page, err = ec.evalClient.ListOpenAIEvalsPage(ctx, pageSize, a.flags.pageToken)
	}
	if err != nil {
		return messages.ListingEvals(err)
	}

	// Filtered before either view renders, so `-o json` and the table
	// answer the same question.
	total := len(page.Data)
	matched := filterEvalsByName(page.Data, a.flags.nameFilter)

	if isJSON(a.cmd) {
		return emitJSONList(a.cmd.OutOrStdout(), matched)
	}
	out := a.cmd.OutOrStdout()
	if len(matched) == 0 {
		if a.flags.nameFilter != "" && total > 0 {
			fmt.Fprint(out, messages.NoEvalsMatching(a.flags.nameFilter, total))
		} else {
			fmt.Fprint(out, messages.NoEvals())
		}
		return nil
	}
	rows := make([][]string, 0, len(matched))
	for _, e := range matched {
		rows = append(rows, []string{e.ID, e.Name})
	}
	if err := emitTable(out, []string{"EVAL ID", "NAME"}, rows); err != nil {
		return err
	}
	if page.HasMore && page.LastID != "" {
		fmt.Fprint(out, messages.MoreEvalsToList(page.LastID))
	}
	return nil
}

// filterEvalsByName keeps the evals whose name contains the filter. An empty
// filter keeps everything, so the caller does not branch.
func filterEvalsByName(evals []eval_api.OpenAIEval, name string) []eval_api.OpenAIEval {
	if name == "" {
		return evals
	}
	needle := strings.ToLower(name)
	out := make([]eval_api.OpenAIEval, 0, len(evals))
	for _, e := range evals {
		if strings.Contains(strings.ToLower(e.Name), needle) {
			out = append(out, e)
		}
	}
	return out
}

// evalShowAction reports one eval definition.
type evalShowAction struct {
	cmd      *cobra.Command
	endpoint string
	evalID   string
}

func newEvalShowCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "show <eval>",
		Short: "Show an eval definition.",
		Args:  requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&evalShowAction{cmd: cmd, endpoint: endpointFlg, evalID: args[0]}).Run()
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *evalShowAction) Run() error {
	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	group, err := ec.evalClient.GetOpenAIEval(ctx, a.evalID)
	if err != nil && eval_api.IsNotFound(err) {
		// The argument reads as an id. `list` reports names, and this
		// refused the very name it points the reader at, so a name is
		// resolved before giving up.
		if resolved := ec.evalIDNamed(ctx, a.evalID); resolved != "" {
			group, err = ec.evalClient.GetOpenAIEval(ctx, resolved)
		}
	}
	if err != nil {
		if eval_api.IsNotFound(err) {
			return messages.EvalNotFound(a.evalID)
		}
		return messages.ReadingEval(a.evalID, err)
	}
	if isJSON(a.cmd) {
		return emitJSON(a.cmd.OutOrStdout(), group)
	}
	detail := []field{
		{"Id", group.ID},
		{"Name", group.Name},
		// CreatedAt is `any` because the service sends epoch seconds here
		// and RFC3339 elsewhere; fmt.Sprint on the former prints a float
		// in scientific notation.
		{"Created", timestampString(group.CreatedAt)},
		{"Created By", group.CreatedBy},
	}
	// Without this the command answers "does this id exist", which is
	// not what a definition is, nor what its own help promises.
	if graders := evalGraders(group); graders != "" {
		detail = append(detail, field{"Evaluators", graders})
	}
	return emitDetail(a.cmd.OutOrStdout(), detail)
}

// evalGraders lists the evaluators the eval grades with, preferring the
// reference a caller would recognize over the criterion label.
//
// data_source_config is deliberately not shown beside it: every eval this
// extension creates carries type "custom", which describes the item schema
// rather than where the rows come from, so a "Source" row would read as an
// answer while always saying the same thing.
func evalGraders(group *eval_api.OpenAIEval) string {
	if group == nil {
		return ""
	}
	names := make([]string, 0, len(group.TestingCriteria))
	for _, c := range group.TestingCriteria {
		name := c.EvaluatorName
		if name == "" {
			name = c.Name
		}
		if name == "" {
			continue
		}
		if c.EvaluatorVersion != "" {
			name += " (" + c.EvaluatorVersion + ")"
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// evalDeleteFlags carries what `eval delete` was asked for.
type evalDeleteFlags struct {
	force    bool
	endpoint string
}

// evalDeleteAction removes an eval and every run under it.
type evalDeleteAction struct {
	cmd    *cobra.Command
	flags  *evalDeleteFlags
	evalID string
}

func newEvalDeleteCommand() *cobra.Command {
	flags := &evalDeleteFlags{}

	cmd := &cobra.Command{
		Use:   "delete <eval>",
		Short: "Delete an eval and everything under it.",
		Long: "Delete an eval and everything under it.\n\n" +
			"An eval owns its runs, so deleting one discards their results too.\n\n" +
			"Asks before removing it. With --no-prompt, or with JSON output, " +
			"--force is required.",
		Args: requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&evalDeleteAction{cmd: cmd, flags: flags, evalID: args[0]}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.endpoint, "project-endpoint", "", "Foundry project endpoint.")
	registerForceFlag(cmd, &flags.force)
	return cmd
}

func (a *evalDeleteAction) Run() error {
	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.flags.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	// Asked on what the author typed, before the name is resolved to an
	// id: the question is about the runs they are discarding, and that
	// answer does not change with which id it turns out to be.
	subject := fmt.Sprintf("eval %s and every run under it", a.evalID)
	goAhead, err := confirmDelete(a.cmd, ec, subject, a.flags.force)
	if err != nil {
		return err
	}
	if !goAhead {
		return deleteDeclined(a.cmd, subject)
	}

	// Local, because the name below resolves to an id and the reporting
	// past that point is about whichever one was actually deleted.
	evalID := a.evalID
	err = ec.evalClient.DeleteOpenAIEval(ctx, evalID)
	if err != nil && eval_api.IsNotFound(err) {
		// `list` reports names, so a name is what a reader has to hand.
		// An eval is immutable, though, so editing a declaration leaves
		// another under the same name, and this deletes the runs under
		// whichever it picks: with more than one it asks rather than guesses.
		ids, listErr := ec.evalIDsNamed(ctx, evalID)
		if listErr != nil {
			// Reporting the eval gone on a listing we could not
			// read would be a delete silently doing nothing.
			return listErr
		}
		switch len(ids) {
		case 0:
		case 1:
			evalID = ids[0]
			err = ec.evalClient.DeleteOpenAIEval(ctx, evalID)
		default:
			return messages.AmbiguousEvalName(evalID, ids)
		}
	}
	if err != nil {
		if eval_api.IsNotFound(err) {
			return messages.EvalGone(evalID)
		}
		return messages.DeletingEval(evalID, err)
	}

	if isJSON(a.cmd) {
		return emitJSON(a.cmd.OutOrStdout(), map[string]string{
			"id": evalID, "status": "deleted",
		})
	}
	fmt.Fprint(a.cmd.OutOrStdout(), messages.EvalDeleted(evalID))
	return nil
}
