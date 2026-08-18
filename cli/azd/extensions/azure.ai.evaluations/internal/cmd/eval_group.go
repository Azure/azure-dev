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

// newEvalCreateCommand creates one declared eval without deploying the rest.
func newEvalCreateCommand() *cobra.Command {
	var (
		fromFile    string
		evalDir     string
		endpointFlg string
	)

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
			ctx := cmd.Context()

			path := fromFile
			if path == "" {
				path = project.ResolveEvalConfigPath(evalDir)
			}
			cfg, err := project.LoadEvalConfig(path)
			if err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}

			eval, err := cfg.Eval(chooseEval(cmd, cfg, firstArg(args)))
			if err != nil {
				return err
			}

			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			// Local sources resolve against the file, not the working directory,
			// so the columns are read from where the declaration points.
			baseDir := filepath.Dir(path)
			datasetPath := ""
			if decl, ok := cfg.DatasetDeclaration(eval.Dataset); ok {
				datasetPath = project.ResolveSource(baseDir, decl.Source)
			}

			reconciler := &evalReconciler{ec: ec}
			out := cmd.OutOrStdout()

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
				if isJSON(cmd) {
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
				if !ok || decl.Source == "" {
					continue
				}
				local := project.ResolveSource(baseDir, decl.Source)
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

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), map[string]string{
					"id": id, "name": eval.Name,
				})
			}
			if created {
				fmt.Fprint(out, messages.EvalCreated(eval.Name, id))
			} else {
				fmt.Fprint(out, messages.EvalUnchanged(eval.Name, id))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&fromFile, "from-file", "",
		"Read the configuration from this path instead of the eval directory.")
	cmd.Flags().StringVar(&evalDir, "path", project.DefaultEvalDir,
		"Directory holding the evaluation configuration.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newEvalListCommand() *cobra.Command {
	var (
		limit       int
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the project's evals.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			list, err := ec.evalClient.ListOpenAIEvals(ctx, limit)
			if err != nil {
				return messages.ListingEvals(err)
			}

			if isJSON(cmd) {
				return emitJSONList(cmd.OutOrStdout(), list.Data)
			}
			if len(list.Data) == 0 {
				fmt.Fprint(cmd.OutOrStdout(), messages.NoEvals())
				return nil
			}
			rows := make([][]string, 0, len(list.Data))
			for _, e := range list.Data {
				rows = append(rows, []string{e.ID, e.Name})
			}
			return emitTable(cmd.OutOrStdout(), []string{"EVAL ID", "NAME"}, rows)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 0, "Cap the number of evals returned.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newEvalShowCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "show <eval>",
		Short: "Show an eval definition.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			evalID := args[0]

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			group, err := ec.evalClient.GetOpenAIEval(ctx, evalID)
			if err != nil && eval_api.IsNotFound(err) {
				// The argument reads as an id. `list` reports names, and this
				// refused the very name it points the reader at, so a name is
				// resolved before giving up.
				if resolved := ec.evalIDNamed(ctx, evalID); resolved != "" {
					group, err = ec.evalClient.GetOpenAIEval(ctx, resolved)
				}
			}
			if err != nil {
				if eval_api.IsNotFound(err) {
					return messages.EvalNotFound(evalID)
				}
				return messages.ReadingEval(evalID, err)
			}
			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), group)
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
			// Without these the command answers "does this id exist", which is
			// not what a definition is, nor what its own help promises.
			if source := evalSourceType(group); source != "" {
				detail = append(detail, field{"Source", source})
			}
			if graders := evalGraders(group); graders != "" {
				detail = append(detail, field{"Evaluators", graders})
			}
			return emitDetail(cmd.OutOrStdout(), detail)
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// evalSourceType reports where the eval's rows come from, as the service
// records it. Empty when the service sent no data source config.
func evalSourceType(group *eval_api.OpenAIEval) string {
	if group == nil || group.DataSourceConfig == nil {
		return ""
	}
	kind, _ := group.DataSourceConfig["type"].(string)
	return kind
}

// evalGraders lists the evaluators the eval grades with, preferring the
// reference a caller would recognize over the criterion label.
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

func newEvalDeleteCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "delete <eval>",
		Short: "Delete an eval and everything under it.",
		Long: "Delete an eval and everything under it.\n\n" +
			"An eval owns its runs, so deleting one discards their results too.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			evalID := args[0]

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

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

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), map[string]string{
					"id": evalID, "status": "deleted",
				})
			}
			fmt.Fprint(cmd.OutOrStdout(), messages.EvalDeleted(evalID))
			return nil
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}
