// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
)

// newRunOutputCommand groups the per-sample views of a run.
//
// `run show` is the summary - how many passed. These are the rows: which ones
// failed, and why.
func newRunOutputCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "output",
		Short: "Inspect the per-sample results of a run.",
	}
	cmd.AddCommand(
		newRunOutputListCommand(),
		newRunOutputShowCommand(),
		newRunOutputExportCommand(),
	)
	return cmd
}

func newRunOutputListCommand() *cobra.Command {
	var (
		failedOnly  bool
		outFile     string
		endpointFlg string
		groupName   string
	)

	cmd := &cobra.Command{
		Use:   "list [run]",
		Short: "List the per-sample results of a run.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			evalID, err := resolveEvalID(cmd, ec, nil, groupName)
			if err != nil {
				return err
			}

			runID := firstArg(args)
			run, err := ec.latestOrNamedRun(cmd, evalID, runID, runID != "")
			if err != nil {
				return err
			}

			// The run carries totals and a per-criterion breakdown. The output
			// items are the rows themselves, which is what "which one failed,
			// and why" needs. A run that never produced any still renders its
			// totals rather than failing.
			items, err := ec.evalClient.ListOutputItems(ctx, evalID, run.ID, 0)
			if err != nil {
				return messages.ReadingRunResults(run.ID, err)
			}
			rows := items.Data
			if failedOnly {
				kept := make([]eval_api.OutputItem, 0, len(rows))
				for _, it := range rows {
					if it.Failed() {
						kept = append(kept, it)
					}
				}
				rows = kept
			}

			payload := map[string]any{"run": run, "output_items": rows}
			if outFile != "" {
				f, err := os.Create(outFile)
				if err != nil {
					return messages.Creating(outFile, err)
				}
				defer f.Close()
				return emitJSON(f, payload)
			}
			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), payload)
			}
			return renderResults(cmd.OutOrStdout(), run, rows, failedOnly)
		},
	}

	cmd.Flags().BoolVar(&failedOnly, "failed-only", false, "Show only the rows that failed.")
	cmd.Flags().StringVar(&outFile, "output-file", "", "Write JSON results to this path.")
	addEvalFlag(cmd, &groupName)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// newRunOutputShowCommand reads one evaluated row by its id.
//
// The listing truncates the input and the reason to keep a table readable, so
// this is how the whole of either is seen.
func newRunOutputShowCommand() *cobra.Command {
	var (
		runID       string
		endpointFlg string
		groupName   string
	)

	cmd := &cobra.Command{
		Use:   "show <output-item>",
		Short: "Show a single evaluated row.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			itemID := args[0]

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			evalID, err := resolveEvalID(cmd, ec, nil, groupName)
			if err != nil {
				return err
			}

			run, err := ec.latestOrNamedRun(cmd, evalID, runID, runID != "")
			if err != nil {
				return err
			}

			item, err := ec.evalClient.GetOutputItem(ctx, evalID, run.ID, itemID)
			if err != nil {
				if eval_api.IsNotFound(err) {
					return messages.OutputItemNotFound(itemID, run.ID)
				}
				return messages.ReadingOutputItem(itemID, err)
			}
			return emitJSON(cmd.OutOrStdout(), item)
		},
	}

	cmd.Flags().StringVar(&runID, "run", "", "Run the item belongs to. Defaults to the most recent run.")
	addEvalFlag(cmd, &groupName)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newRunOutputExportCommand() *cobra.Command {
	var (
		format      string
		outFile     string
		endpointFlg string
		groupName   string
	)

	cmd := &cobra.Command{
		Use:   "export [run]",
		Short: "Export run results as JSON or CSV.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format = strings.ToLower(format)
			if format != "json" && format != "csv" {
				return messages.ExportFormatInvalid(format)
			}

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			evalID, err := resolveEvalID(cmd, ec, nil, groupName)
			if err != nil {
				return err
			}

			runID := firstArg(args)
			run, err := ec.latestOrNamedRun(cmd, evalID, runID, runID != "")
			if err != nil {
				return err
			}

			var w io.Writer = cmd.OutOrStdout()
			if outFile != "" {
				f, err := os.Create(outFile)
				if err != nil {
					return messages.Creating(outFile, err)
				}
				defer f.Close()
				w = f
			}

			switch format {
			case formatCSV:
				return writeResultsCSV(w, run)
			case formatJSON:
				return emitJSON(w, run)
			case formatJSONL:
				return writeResultsJSONL(w, run)
			default:
				return messages.ExportFormatUnsupported(
					format, formatCSV, formatJSON, formatJSONL)
			}
		},
	}

	cmd.Flags().StringVar(&format, "format", formatCSV,
		fmt.Sprintf("Output format: %s, %s or %s.", formatCSV, formatJSON, formatJSONL))
	cmd.Flags().StringVar(&outFile, "output-file", "", "Write to this path instead of stdout.")
	addEvalFlag(cmd, &groupName)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// resolveEvalID takes the eval id from the argument, from --eval, or from the
// id cached in the azd environment.
//
// --eval accepts a name or a raw id on the one flag: an eval created outside a
// project has no declaration to name, and the environment records one id per
// name, so editing a declaration leaves every run of the previous eval
// reachable only by id.
func resolveEvalID(
	cmd *cobra.Command,
	ec *evalContext,
	args []string,
	groupName string,
) (string, error) {
	if len(args) > 0 && args[0] != "" {
		return args[0], nil
	}

	if groupName != "" {
		ref, err := ec.resolveEvalRef(cmd.Context(), ec.evalDir(cmd.Context(), ""), groupName)
		if err != nil {
			return "", err
		}
		return ref.ID, nil
	}

	if cached := ec.getEnvValue(cmd.Context(), envKeyEvalID); cached != "" {
		return cached, nil
	}
	return "", messages.NoEvalGiven()
}

// addEvalFlag registers the flag that says which eval a command acts on. It
// takes a name from the configuration or a raw service id, which is why there
// is no second --eval-id beside it.
func addEvalFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(target, "eval", "",
		"Name of the eval declared in the configuration, or its id.")
}

// addEvalPathFlag registers --path on a command that reads the configuration.
//
// It defaults to empty rather than to ./evals so that "not given" stays
// distinguishable from "given the default", which is what lets the path `init`
// recorded take effect in between.
func addEvalPathFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(target, "path", "",
		"Directory holding azure.eval.yaml. Defaults to the path `init` used, then ./evals.")
}

// latestOrNamedRun returns the named run, or the most recent one for the eval.
//
// explicit says whether the caller named the run rather than leaving it to
// default. A remembered run that no longer resolves is worth falling through
// on; one that was asked for by name is not.
func (ec *evalContext) latestOrNamedRun(
	cmd *cobra.Command,
	evalID, runID string,
	explicit bool,
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
		if explicit {
			return nil, messages.ReadingRun(runID, err)
		}
	}

	list, err := ec.evalClient.ListOpenAIEvalRuns(ctx, evalID, 1)
	if err != nil {
		if eval_api.IsNotFound(err) {
			return nil, messages.EvalNotDeployed(evalID)
		}
		return nil, messages.ListingRuns(evalID, err)
	}
	if len(list.Data) == 0 {
		return nil, messages.EvalHasNoRuns(evalID)
	}
	return &list.Data[0], nil
}

func renderResults(
	w io.Writer,
	run *eval_api.OpenAIEvalRun,
	items []eval_api.OutputItem,
	failedOnly bool,
) error {
	fmt.Fprint(w, messages.RunStatusHeading(run.ID, run.Status))

	if c := run.ResultCounts; c != nil {
		fmt.Fprint(w, messages.ResultTotals(c.Passed, c.Failed, c.Errored))
	}

	if len(run.PerTestingCriteria) > 0 {
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
		if len(rows) > 0 {
			if err := emitTable(w, []string{"CRITERION", "PASSED", "FAILED"}, rows); err != nil {
				return err
			}
		}
	}

	// The rows are the point of `results show`: totals say how many failed,
	// these say which and why.
	if len(items) == 0 {
		if failedOnly {
			fmt.Fprint(w, messages.NoFailingRows())
		} else {
			fmt.Fprint(w, messages.NoRowsScored())
		}
	} else {
		fmt.Fprintln(w)
		rows := make([][]string, 0, len(items))
		for i, it := range items {
			// One row per evaluated sample, not per verdict: a sample that
			// failed three evaluators is one sample to go and look at, and
			// listing it three times buries how much is actually wrong.
			var failed []string
			reason := ""
			for _, r := range it.Results {
				if r.Passed {
					continue
				}
				failed = append(failed, r.Name)
				if reason == "" {
					reason = r.Reason
				}
			}
			if failedOnly && len(failed) == 0 {
				continue
			}
			verdicts := strings.Join(failed, ", ")
			if verdicts == "" {
				verdicts = "-"
			}
			rows = append(rows, []string{
				it.ID,
				strconv.Itoa(i + 1),
				truncate(verdicts, 40),
				truncate(reason, 44),
			})
		}
		if err := emitTable(w,
			[]string{"ITEM", "SAMPLE", "FAILED EVALUATORS", "REASON (first failure)"},
			rows); err != nil {
			return err
		}
		if n := len(rows); failedOnly && n > 0 {
			fmt.Fprint(w, messages.SamplesFailedAtLeastOne(n))
		}
	}

	if run.ReportURL != "" {
		fmt.Fprint(w, messages.ReportLinkAfterRows(run.ReportURL))
	}
	return nil
}

// truncate keeps a table readable when a reason runs to a paragraph. The full
// text is always in `-o json`.
func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", "")
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
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

// Export formats. csv is the default because the results are a table and a
// build artifact is normally read by a spreadsheet or a diff.
const (
	formatCSV   = "csv"
	formatJSON  = "json"
	formatJSONL = "jsonl"
)

// writeResultsJSONL emits one criterion per line, which is what a downstream
// job can stream without holding the whole run in memory.
func writeResultsJSONL(w io.Writer, run *eval_api.OpenAIEvalRun) error {
	enc := json.NewEncoder(w)
	if len(run.PerTestingCriteria) == 0 {
		return enc.Encode(map[string]any{"run_id": run.ID, "status": run.Status})
	}
	for _, cr := range run.PerTestingCriteria {
		if err := enc.Encode(map[string]any{
			"run_id":           run.ID,
			"status":           run.Status,
			"testing_criteria": cr.TestingCriteria,
			"passed":           cr.Passed,
			"failed":           cr.Failed,
		}); err != nil {
			return err
		}
	}
	return nil
}
