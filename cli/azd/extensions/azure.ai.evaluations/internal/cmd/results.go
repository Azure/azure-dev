// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
)

func newResultsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "results",
		Short: "Inspect evaluation results.",
	}
	cmd.AddCommand(newResultsShowCommand(), newResultsExportCommand(), newResultsCompareCommand())
	return cmd
}

func newResultsShowCommand() *cobra.Command {
	var (
		runID       string
		failedOnly  bool
		outFile     string
		endpointFlg string
		groupName   string
	)

	cmd := &cobra.Command{
		Use:   "show <eval-id>",
		Short: "Show per-sample results for a run.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			evalID, err := resolveEvalID(cmd, ec, args, groupName)
			if err != nil {
				return err
			}

			run, err := ec.latestOrNamedRun(cmd, evalID, runID)
			if err != nil {
				return err
			}

			if outFile != "" {
				f, err := os.Create(outFile)
				if err != nil {
					return fmt.Errorf("creating %q: %w", outFile, err)
				}
				defer f.Close()
				return emitJSON(f, run)
			}
			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), run)
			}
			return renderResults(cmd.OutOrStdout(), run, failedOnly)
		},
	}

	cmd.Flags().StringVar(&runID, "run-id", "", "Run to show. Defaults to the most recent run.")
	cmd.Flags().BoolVar(&failedOnly, "failed-only", false, "Show only criteria with failures.")
	cmd.Flags().StringVarP(&outFile, "out-file", "O", "", "Write JSON results to this path.")
	addEvalGroupFlags(cmd, &groupName)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newResultsExportCommand() *cobra.Command {
	var (
		runID       string
		format      string
		outFile     string
		endpointFlg string
		groupName   string
	)

	cmd := &cobra.Command{
		Use:   "export <eval-id>",
		Short: "Export run results as JSON or CSV.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format = strings.ToLower(format)
			if format != "json" && format != "csv" {
				return fmt.Errorf("--format must be json or csv, got %q", format)
			}

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			evalID, err := resolveEvalID(cmd, ec, args, groupName)
			if err != nil {
				return err
			}

			run, err := ec.latestOrNamedRun(cmd, evalID, runID)
			if err != nil {
				return err
			}

			var w io.Writer = cmd.OutOrStdout()
			if outFile != "" {
				f, err := os.Create(outFile)
				if err != nil {
					return fmt.Errorf("creating %q: %w", outFile, err)
				}
				defer f.Close()
				w = f
			}

			if format == "json" {
				return emitJSON(w, run)
			}
			return writeResultsCSV(w, run)
		},
	}

	cmd.Flags().StringVar(&runID, "run-id", "", "Run to export. Defaults to the most recent run.")
	cmd.Flags().StringVar(&format, "format", "json", "Output format: json or csv.")
	cmd.Flags().StringVarP(&outFile, "out-file", "O", "", "Write to this path instead of stdout.")
	addEvalGroupFlags(cmd, &groupName)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// resolveEvalID takes the eval group id from the argument, from a group named
// with --eval-group, or from the id cached in the azd environment.
//
// The cached id is the last group deployed, which is unambiguous only while a
// config declares one. --eval-group is how the others are reached without
// having to know their service ids.
func resolveEvalID(
	cmd *cobra.Command,
	ec *evalContext,
	args []string,
	groupName string,
) (string, error) {
	if len(args) > 0 && args[0] != "" {
		return args[0], nil
	}

	if flag, err := cmd.Flags().GetString("eval-id"); err == nil && flag != "" {
		return flag, nil
	}

	if groupName != "" {
		if id := ec.getEnvValue(cmd.Context(), idKey("evalgroup", groupName)); id != "" {
			return id, nil
		}
		return "", fmt.Errorf(
			"eval group %q has no id recorded in this environment; deploy it first, "+
				"or pass its id directly", groupName)
	}

	if cached := ec.getEnvValue(cmd.Context(), envKeyEvalGroupID); cached != "" {
		return cached, nil
	}
	return "", fmt.Errorf(
		"no eval group id given; pass it as an argument, name one with --eval-group, "+
			"or set %s in the azd environment",
		envKeyEvalGroupID)
}

// addEvalGroupFlag registers the flag that names a group from the config, so
// every command taking an eval-id can reach a group by the name its author
// used.
// addEvalGroupFlags registers the two ways to say which group a command acts
// on: --eval-group names one from the config, --eval-id gives its service id.
//
// The id is also accepted as a positional argument. The flag exists because
// `run start --eval-id` already spells it that way, and a script that learned
// it there should not have to find out that the sibling commands take only a
// positional.
func addEvalGroupFlags(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(target, "eval-group", "",
		"Name a group from the config instead of passing its id.")
	cmd.Flags().String("eval-id", "",
		"Id of the eval group. Same as passing the id as an argument.")
}

// latestOrNamedRun returns the named run, or the most recent one for the group.
func (ec *evalContext) latestOrNamedRun(
	cmd *cobra.Command,
	evalID, runID string,
) (*eval_api.OpenAIEvalRun, error) {
	ctx := cmd.Context()

	// The remembered run is per group. A single shared one belongs to whichever
	// group ran last, and asking another group for it returns 404 rather than
	// that group's own latest run.
	if runID == "" {
		runID = ec.getEnvValue(ctx, idKey("evalrun", evalID))
	}
	if runID != "" {
		run, err := ec.evalClient.GetOpenAIEvalRun(ctx, evalID, runID)
		if err == nil {
			return run, nil
		}
		// A remembered run that no longer resolves is not worth failing on:
		// fall through to whatever the group has now.
		if cmd.Flag("run-id") != nil && cmd.Flag("run-id").Changed {
			return nil, fmt.Errorf("reading run %s: %w", runID, err)
		}
	}

	list, err := ec.evalClient.ListOpenAIEvalRuns(ctx, evalID, 1)
	if err != nil {
		return nil, fmt.Errorf("listing runs for eval group %s: %w", evalID, err)
	}
	if len(list.Data) == 0 {
		return nil, fmt.Errorf("eval group %s has no runs yet", evalID)
	}
	return &list.Data[0], nil
}

func renderResults(w io.Writer, run *eval_api.OpenAIEvalRun, failedOnly bool) error {
	fmt.Fprintf(w, "Run %s  status: %s\n", run.ID, run.Status)

	if c := run.ResultCounts; c != nil {
		fmt.Fprintf(w, "Totals: %d passed, %d failed, %d errored\n\n",
			c.Passed, c.Failed, c.Errored)
	}

	if len(run.PerTestingCriteria) == 0 {
		fmt.Fprintln(w, "No per-criteria results are available yet.")
		return nil
	}

	rows := make([][]string, 0, len(run.PerTestingCriteria))
	for _, cr := range run.PerTestingCriteria {
		if failedOnly && cr.Failed == 0 {
			continue
		}
		rows = append(rows, []string{
			cr.TestingCriteria,
			strconv.Itoa(cr.Passed),
			strconv.Itoa(cr.Failed),
		})
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "No failing criteria.")
		return nil
	}
	if err := emitTable(w, []string{"CRITERION", "PASSED", "FAILED"}, rows); err != nil {
		return err
	}
	if run.ReportURL != "" {
		fmt.Fprintf(w, "\nReport: %s\n", run.ReportURL)
	}
	return nil
}

func writeResultsCSV(w io.Writer, run *eval_api.OpenAIEvalRun) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write([]string{"run_id", "status", "criterion", "passed", "failed"}); err != nil {
		return err
	}
	if len(run.PerTestingCriteria) == 0 {
		return cw.Write([]string{run.ID, run.Status, "", "", ""})
	}
	for _, cr := range run.PerTestingCriteria {
		if err := cw.Write([]string{
			run.ID, run.Status, cr.TestingCriteria,
			strconv.Itoa(cr.Passed), strconv.Itoa(cr.Failed),
		}); err != nil {
			return err
		}
	}
	return nil
}
