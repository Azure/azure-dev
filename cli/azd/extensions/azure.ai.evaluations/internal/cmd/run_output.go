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

	"github.com/fatih/color"
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

			evalID, err := resolveEvalID(cmd, ec, groupName)
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

			// A bare array, as every other list emits. Wrapping the rows beside
			// the run made `-o json` the one listing a script could not iterate,
			// and it failed silently: the loop walked the two keys instead. The
			// run itself is what `run show` answers.
			if outFile != "" {
				f, err := os.Create(outFile)
				if err != nil {
					return messages.Creating(outFile, err)
				}
				if err := emitJSONList(f, rows); err != nil {
					_ = f.Close()
					return err
				}
				// The last write is flushed by Close, so discarding its error
				// reports success over a file that stops mid-row.
				if err := f.Close(); err != nil {
					return messages.Writing(outFile, err)
				}
				return nil
			}
			if isJSON(cmd) {
				return emitJSONList(cmd.OutOrStdout(), rows)
			}
			return renderResults(cmd.OutOrStdout(), run, rows, failedOnly)
		},
	}

	cmd.Flags().BoolVar(&failedOnly, "failed-only", false, "Show only the rows that failed.")
	cmd.Flags().StringVar(&outFile, "output-file", "", "Write JSON results to this path.")
	addEvalFlag(cmd, &groupName)
	// Registered wherever a declared name is resolved, so a configuration
	// outside ./evals can be addressed by every command, not just `run start`.
	addEvalPathFlag(cmd, new(string))
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

			evalID, err := resolveEvalID(cmd, ec, groupName)
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
			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), item)
			}
			return renderOutputItem(cmd.OutOrStdout(), item)
		},
	}

	cmd.Flags().StringVar(&runID, "run", "", "Run the item belongs to. Defaults to the most recent run.")
	addEvalFlag(cmd, &groupName)
	// Registered wherever a declared name is resolved, so a configuration
	// outside ./evals can be addressed by every command, not just `run start`.
	addEvalPathFlag(cmd, new(string))
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// writeExport renders a run in the requested export format.
//
// Separate from the command so the file path can close explicitly and report
// the failure. A deferred Close cannot reach an unnamed return, so discarding
// it exits 0 over an export that stops mid-row.
func writeExport(w io.Writer, format string, run *eval_api.OpenAIEvalRun) error {
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
		Short: "Export run results as CSV, JSON or JSONL.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format = strings.ToLower(format)
			// Checked against the same set the writer switches on: this guard
			// used to name only json and csv, so --format jsonl was refused by a
			// CLI whose own help offered it.
			switch format {
			case formatCSV, formatJSON, formatJSONL:
			default:
				return messages.ExportFormatUnsupported(
					format, formatCSV, formatJSON, formatJSONL)
			}

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			evalID, err := resolveEvalID(cmd, ec, groupName)
			if err != nil {
				return err
			}

			runID := firstArg(args)
			run, err := ec.latestOrNamedRun(cmd, evalID, runID, runID != "")
			if err != nil {
				return err
			}

			if outFile == "" {
				return writeExport(cmd.OutOrStdout(), format, run)
			}

			f, createErr := os.Create(outFile)
			if createErr != nil {
				return messages.Creating(outFile, createErr)
			}
			writeErr := writeExport(f, format, run)
			// The last write is flushed by Close, so discarding its error
			// reports success over a file that stops mid-row.
			closeErr := f.Close()
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return messages.Writing(outFile, closeErr)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", formatCSV,
		fmt.Sprintf("Output format: %s, %s or %s.", formatCSV, formatJSON, formatJSONL))
	cmd.Flags().StringVar(&outFile, "output-file", "", "Write to this path instead of stdout.")
	addEvalFlag(cmd, &groupName)
	// Registered wherever a declared name is resolved, so a configuration
	// outside ./evals can be addressed by every command, not just `run start`.
	addEvalPathFlag(cmd, new(string))
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// resolveEvalID resolves the eval a run command is about, from --eval or from
// the declaration the configuration holds.
//
// --eval accepts a name or a raw id on the one flag: an eval created outside a
// project has no declaration to name, and the environment records one id per
// name, so editing a declaration leaves every run of the previous eval
// reachable only by id.
//
// It takes no positional argument, deliberately. The positional on `run show`,
// `run cancel` and `run output *` is a *run* id, and a signature that accepted
// either would let one be resolved as the other -- a destructive verb aimed at
// a resource picked by accident.
//
// It reads no EVAL_ID either. Every deploy writes that key, so nothing tells a
// value meant for this declaration from one left behind by the eval it
// replaced; `run cancel` used to cancel a run of an eval the file no longer
// described. The declaration is asked instead, which is how `run start`
// decides it, so the two doors cannot pick different evals.
func resolveEvalID(cmd *cobra.Command, ec *evalContext, groupName string) (string, error) {
	evalDir, err := ec.evalDir(cmd.Context(), evalPathFlag(cmd))
	if err != nil {
		return "", err
	}
	// The same prompt `run start` gets. Without it a project declaring two
	// evals could start a run by answering a question, and then not list,
	// show or cancel it without repeating the answer as a flag.
	ref, err := ec.resolveEvalRef(cmd.Context(), evalDir, chooseEvalIn(cmd, evalDir, groupName))
	if err != nil {
		return "", err
	}
	return ref.ID, nil
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
	// No backticks: pflag reads a word in back quotes as the value placeholder,
	// so `init` rendered the flag as "--path init" instead of "--path string".
	cmd.Flags().StringVar(target, "path", "",
		"Directory holding azure.eval.yaml. Defaults to the path init used, then ./evals.")
}

// evalPathFlag reads --path from whichever command is resolving a declared
// name, so every one of them can be told where the configuration is.
//
// Read off the command rather than threaded through seven call sites. Without
// it only `run start` offered the flag, so a configuration outside ./evals
// could be run and then not listed, shown or cancelled -- the fallback that
// covers the difference is a path recorded in the azd environment, which a
// --project-endpoint caller does not have.
func evalPathFlag(cmd *cobra.Command) string {
	if f := cmd.Flags().Lookup("path"); f != nil {
		return f.Value.String()
	}
	return ""
}

// latestOrNamedRun returns the named run, or the most recent one for the eval.
//
// explicit says whether the caller named the run rather than leaving it to
// default. A remembered run that no longer resolves is worth falling through
// on; one that was asked for by name is not.
//
// The remembered id is preferred over the service's listing, and deliberately.
// Listing looks like the fix for two concurrent starts leaving this key holding
// whichever wrote last, but ListOpenAIEvalRuns sends no order parameter, so
// "the first row" is not promised to be the newest; and `run cancel` defaults
// through here, so guessing would cancel a run this environment never started.
// The remembered id is at least scoped to the environment that made it.
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
			return nil, messages.EvalNotDeployed(evalID, ec.deployCommand(ctx))
		}
		return nil, messages.ListingRuns(evalID, err)
	}
	if list == nil || len(list.Data) == 0 {
		return nil, messages.EvalHasNoRuns(evalID)
	}
	return &list.Data[0], nil
}

// renderOutputItem is the detail view for one evaluated row.
//
// This was the one `show` that emitted raw JSON whatever was asked for, which
// made the command a person reaches for after a failing listing the hardest one
// to read. The listing truncates the reason to a cell; this is where the whole
// of it lives, so the reasons are printed in full rather than wrapped or cut.
//
// Results are grouped by evaluator: a rubric reports one result per dimension,
// all carrying the evaluator's name, and printing them flat would read as
// several evaluators that happen to share a name.
func renderOutputItem(w io.Writer, item *eval_api.OutputItem) error {
	if item == nil {
		return messages.OutputItemEmpty()
	}
	if err := emitDetail(w, []field{
		{"Item", item.ID},
		{"Run", item.RunID},
		{"Status", item.Status},
	}); err != nil {
		return err
	}

	order := make([]string, 0, len(item.Results))
	byName := make(map[string][]eval_api.OutputResult, len(item.Results))
	for _, r := range item.Results {
		if _, seen := byName[r.Name]; !seen {
			order = append(order, r.Name)
		}
		byName[r.Name] = append(byName[r.Name], r)
	}

	for _, name := range order {
		results := byName[name]
		fmt.Fprintln(w)

		// The service repeats the evaluator's name in `metric` for a
		// single-score evaluator, so a group is only worth nesting when its
		// results name dimensions of their own.
		if len(results) == 1 && (results[0].Metric == "" || results[0].Metric == name) {
			r := results[0]
			fmt.Fprint(w, messages.OutputItemVerdict(
				name, formatScore(r.Score), verdictWord(r)))
			if r.Reason != "" {
				fmt.Fprint(w, messages.OutputItemReason(r.Reason))
			}
			continue
		}

		fmt.Fprint(w, messages.OutputItemEvaluator(name))
		for _, r := range results {
			label := r.Metric
			if label == "" {
				label = r.Name
			}
			fmt.Fprint(w, messages.OutputItemMetric(
				label, formatScore(r.Score), verdictWord(r)))
			if r.Reason != "" {
				fmt.Fprint(w, messages.OutputItemReason(r.Reason))
			}
		}
	}
	return nil
}

// verdictWord spells a boolean the way the rest of the output does.
func verdictWord(r eval_api.OutputResult) string {
	if !r.Judged() {
		// The evaluator returned no verdict, which is not the same as returning
		// a failing one -- it says nothing about the sample.
		return "no verdict"
	}
	if r.DidPass() {
		return "pass"
	}
	return "fail"
}

// formatScore prints a judge's score at the two decimals the scale carries.
// formatScore shows a score, or a dash where there is none. An evaluator that
// errored on a row still sends a result, and its score decodes to NaN; printing
// that verbatim put "NaN" in the SCORE column.
func formatScore(score eval_api.LenientFloat) string {
	if !score.Defined() {
		return "-"
	}
	return strconv.FormatFloat(float64(score), 'f', 2, 64)
}

// meanScoreOf averages a sample's scores so the list can tell a bare pass from
// a strong one. Pass/fail alone sent anyone asking "how well?" to the portal.
//
// Rows an evaluator errored on are left out rather than counted, the same rule
// criteriaMeans applies to the summary: averaging a NaN in makes the whole
// sample read NaN, and counting it as zero drags the mean toward a number no
// evaluator produced.
func meanScoreOf(results []eval_api.OutputResult) string {
	total := 0.0
	scored := 0
	for _, r := range results {
		if !r.Score.Defined() {
			continue
		}
		total += float64(r.Score)
		scored++
	}
	if scored == 0 {
		return "-"
	}
	return strconv.FormatFloat(total/float64(scored), 'f', 2, 64)
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
		var rowsFailed, rowsUnscored int
		for _, it := range items {
			// One row per evaluated sample, not per verdict: a sample that
			// failed three evaluators is one sample to go and look at, and
			// listing it three times buries how much is actually wrong.
			//
			// An evaluator that returned no verdict is held apart from one that
			// returned a failing verdict. Both keep the row, because the row is
			// still worth looking at, but naming an errored evaluator among the
			// ones the sample failed states something about the sample that
			// nothing measured.
			var failed, unjudged []string
			reason := ""
			for _, r := range it.Results {
				switch {
				case !r.Judged():
					unjudged = append(unjudged, r.Name)
				case r.DidPass():
					continue
				default:
					failed = append(failed, r.Name)
				}
				if reason == "" {
					reason = r.Reason
				}
			}
			// The same predicate `-o json` filters on, so the two views of
			// --failed-only cannot disagree about which rows went wrong.
			if failedOnly && !it.Failed() {
				continue
			}
			verdicts := strings.Join(failed, ", ")
			if len(unjudged) > 0 {
				note := strings.Join(unjudged, ", ") + " (no verdict)"
				if verdicts == "" {
					verdicts = note
				} else {
					verdicts += "; " + note
				}
			}
			if verdicts == "" {
				verdicts = "-"
			}
			if len(failed) > 0 {
				rowsFailed++
			} else {
				rowsUnscored++
			}
			// No position column. It numbered within the current filter, so the
			// same sample carried a different number depending on the flags while
			// reading like an identifier -- and ITEM already carries the id, which
			// is what `run output show` accepts.
			rows = append(rows, []string{
				it.ID,
				meanScoreOf(it.Results),
				truncate(verdicts, 40),
				truncate(reason, 44),
			})
		}
		// Only the first failure's reason fits a cell; `run output show` has
		// the rest.
		if err := emitTable(w,
			[]string{"ITEM", "SCORE", "EVALUATORS", "REASON"},
			rows); err != nil {
			return err
		}
		// Counting unscored rows as failures put a number here that contradicted
		// the totals two lines above, which is what a reader compares it with.
		if failedOnly && len(rows) > 0 {
			fmt.Fprint(w, messages.SamplesNeedingALook(rowsFailed, rowsUnscored))
		}
	}

	if url := runLink(run.ReportURL, run.PortalURL); url != "" {
		fmt.Fprint(w, messages.ReportLinkAfterRows(color.CyanString(url)))
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

	// Named as the service names it, and as the jsonl export already did, so a
	// pipeline reading both formats needs one spelling rather than two.
	if err := cw.Write([]string{"run_id", "status", "testing_criteria", "passed", "failed"}); err != nil {
		return err
	}
	if len(run.PerTestingCriteria) == 0 {
		if err := cw.Write([]string{run.ID, run.Status, "", "", ""}); err != nil {
			return err
		}
		return flushCSV(cw)
	}
	for _, cr := range run.PerTestingCriteria {
		if err := cw.Write([]string{
			run.ID, run.Status, cr.TestingCriteria,
			strconv.Itoa(cr.Passed), strconv.Itoa(cr.Failed),
		}); err != nil {
			return err
		}
	}
	return flushCSV(cw)
}

// flushCSV reports what the buffer swallowed.
//
// csv.Writer buffers, so a disk that filled or a pipe that closed shows up only
// in Error() after the final Flush. Deferring the flush and returning nil made
// `run output export` report success over a file it had not finished writing.
func flushCSV(cw *csv.Writer) error {
	cw.Flush()
	return cw.Error()
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
