// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
)

// The eval group is read and delete only. Creation belongs to `azd up`, which
// owns reconciliation: a second creation path would drift from the declared
// config, and reconciliation could not then tell whether to adopt an eval it
// found or replace it.

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
