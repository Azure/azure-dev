// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"path/filepath"

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
				path = project.EvalConfigPath(evalDir)
			}
			cfg, err := project.LoadEvalConfig(path)
			if err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}

			eval, err := cfg.Eval(firstArg(args))
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
			datasetPath := ""
			if decl, ok := cfg.DatasetDeclaration(eval.Dataset); ok && decl.Source != "" {
				datasetPath = filepath.Join(filepath.Dir(path), decl.Source)
			}

			reconciler := &evalReconciler{ec: ec}
			id, err := reconciler.EnsureEval(ctx, *eval, datasetPath, false)
			if err != nil {
				return err
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), map[string]string{
					"id": id, "name": eval.Name,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s Created eval: %s (%s)\n", doneMark, eval.Name, id)
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
				return fmt.Errorf("listing evals: %w", err)
			}

			if isJSON(cmd) {
				return emitJSONList(cmd.OutOrStdout(), list.Data)
			}
			if len(list.Data) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No evals found.")
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
			if err != nil {
				if eval_api.IsNotFound(err) {
					return fmt.Errorf(
						"no eval %q in this project; "+
							"`azd ai eval list` shows the ones there are", evalID)
				}
				return fmt.Errorf("reading eval %q: %w", evalID, err)
			}
			return emitJSON(cmd.OutOrStdout(), group)
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
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

			if err := ec.evalClient.DeleteOpenAIEval(ctx, evalID); err != nil {
				if eval_api.IsNotFound(err) {
					return fmt.Errorf("no eval %q in this project", evalID)
				}
				return fmt.Errorf("deleting eval %q: %w", evalID, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), map[string]string{
					"id": evalID, "status": "deleted",
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted eval %s\n", evalID)
			return nil
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}
