// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
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
		statusFlag  string
		outFile     string
		endpointFlg string
		groupName   string
		runFlag     string
		limit       int
		pageToken   string
		all         bool
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
			if runID == "" {
				runID = runFlag
			}
			run, err := ec.latestOrNamedRun(cmd, evalID, runID, true)
			if err != nil {
				return err
			}

			// The run carries totals and a per-criterion breakdown. The output
			// items are the rows themselves, which is what "which one failed,
			// and why" needs. A run that never produced any still renders its
			// totals rather than failing.
			// A generated dataset runs to a thousand rows, each carrying a
			// result per evaluator, so an unbounded listing floods the terminal.
			// --output-file and `run output export` are the bulk paths and take
			// everything.
			pageSize := pageSizeOr(limit, all || outFile != "", defaultPageSize)
			items, err := ec.evalClient.ListOutputItemsPage(ctx, evalID, run.ID, pageSize, pageToken)
			if err != nil {
				return messages.ReadingRunResults(run.ID, err)
			}
			rows := items.Data
			// One predicate for both views, so `-o json` and the table cannot
			// disagree about which rows the filter kept.
			keep, err := parseStatusFilter(statusFlag)
			if err != nil {
				return err
			}
			if failedOnly {
				if keep == nil {
					keep = map[string]bool{}
				}
				keep[itemFailed] = true
			}
			if keep != nil {
				kept := make([]eval_api.OutputItem, 0, len(rows))
				for _, it := range rows {
					if keep[classifyItem(it).Status] {
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
			if err := renderResults(cmd.OutOrStdout(), run, rows, failedOnly); err != nil {
				return err
			}
			if items.HasMore && items.LastID != "" {
				fmt.Fprint(cmd.OutOrStdout(), messages.MoreItemsToList(items.LastID))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&failedOnly, "failed-only", false,
		"Show only the items that failed. Shorthand for --status failed.")
	cmd.Flags().StringVar(&statusFlag, "status", "",
		"Show only items with these outcomes: passed, failed, errored, skipped (comma-separated).")
	cmd.Flags().StringVar(&outFile, "output-file", "", "Write JSON results to this path.")
	addPagingFlags(cmd, &limit, &pageToken, &all, defaultPageSize)
	addRunFlag(cmd, &runFlag)
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
		Short: "Show a single evaluated row. The id is the ITEM column of `run output list`.",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return messages.OutputItemRequired(groupName)
			}
			return requiredArgs(1)(cmd, args)
		},
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

			run, err := ec.latestOrNamedRun(cmd, evalID, runID, true)
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

	addRunFlag(cmd, &runID)
	addEvalFlag(cmd, &groupName)
	// Registered wherever a declared name is resolved, so a configuration
	// outside ./evals can be addressed by every command, not just `run start`.
	addEvalPathFlag(cmd, new(string))
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// writeExport writes the complete result document for a run.
//
// Separate from the command so the file path can close explicitly and report
// the failure. A deferred Close cannot reach an unnamed return, so discarding
// it exits 0 over an export that stops mid-row.
//
// One format, and it is the whole result. The CSV and JSONL this replaced were
// not other serializations of this document -- they were a per-evaluator
// summary carrying passed and failed only, so a run with errored or skipped
// results exported numbers that did not add up to it, and no format carried the
// rows at all. A projection is a `jq` away from this file; the data it needs is
// not recoverable from a summary.
func writeExport(w io.Writer, doc exportDocument) error {
	return emitJSON(w, doc)
}

// exportDocument is the machine-readable result of a run: the run as the
// service described it, and every evaluated row beneath it.
//
// Both are held as raw service JSON rather than the typed models, which carry
// only what the CLI renders. Exporting through them dropped job logs, per-model
// usage and anything the service added after this client was written.
type exportDocument struct {
	Run   json.RawMessage   `json:"run"`
	Items []json.RawMessage `json:"items"`
}

func newRunOutputExportCommand() *cobra.Command {
	var (
		format      string
		outFile     string
		endpointFlg string
		groupName   string
		runFlag     string
	)

	cmd := &cobra.Command{
		Use:   "export [run]",
		Short: "Export the complete run results as JSON.",
		Long: `Export the complete results of a run as one JSON document.

The document holds the run exactly as the service described it, and every
evaluated row beneath it: the item that was evaluated, what the target
answered, and each evaluator's score, verdict and reason.

This is the machine-readable path. ` + "`run output list`" + ` is the readable one.
Derive any other shape from this file, for example:

  jq -c '.items[]' results.json > results.jsonl`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if f := strings.ToLower(strings.TrimSpace(format)); f != "" && f != formatJSON {
				return messages.ExportFormatUnsupported(format, formatJSON)
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
			if runID == "" {
				runID = runFlag
			}
			run, err := ec.latestOrNamedRun(cmd, evalID, runID, true)
			if err != nil {
				return err
			}

			// Read back raw, so the export carries the service's own fields
			// rather than the subset these models decode.
			rawRun, err := ec.evalClient.GetRunRaw(ctx, evalID, run.ID)
			if err != nil {
				return messages.ReadingRun(run.ID, err)
			}
			rawItems, err := ec.evalClient.ListOutputItemsRaw(ctx, evalID, run.ID)
			if err != nil {
				return messages.ReadingRunResults(run.ID, err)
			}
			if rawItems == nil {
				rawItems = []json.RawMessage{}
			}
			doc := exportDocument{Run: rawRun, Items: rawItems}

			if outFile == "" {
				return writeExport(cmd.OutOrStdout(), doc)
			}

			f, createErr := os.Create(outFile)
			if createErr != nil {
				return messages.Creating(outFile, createErr)
			}
			writeErr := writeExport(f, doc)
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

	addRunFlag(cmd, &runFlag)
	cmd.Flags().StringVar(&format, "format", formatJSON,
		fmt.Sprintf("Output format. Only %s is supported.", formatJSON))
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

// latestOrNamedRun returns the run a command should act on.
//
// Three sources, in order: the id the caller named, the id this environment
// recorded, and -- only when mayGuess -- the newest run the service lists.
//
// mayGuess is off for the commands that change a run. The listing is the one
// source that can name a run this environment never started: ListOpenAIEvalRuns
// sends no order parameter, so "the first row" is not promised to be the newest,
// and on a shared project it may belong to someone else. Reading the wrong run
// is a confusing answer; cancelling it is somebody's lost work.
//
// A remembered id the service no longer has is worth falling through on. One
// that fails for any other reason -- a 403, a 500, a timeout -- is reported
// instead: a run that is merely unreachable has not been replaced by a
// different one, and quietly acting on another is how the wrong run gets
// cancelled during an outage.
func (ec *evalContext) latestOrNamedRun(
	cmd *cobra.Command,
	evalID, runID string,
	mayGuess bool,
) (*eval_api.OpenAIEvalRun, error) {
	ctx := cmd.Context()
	explicit := runID != ""

	// The remembered run is per group. A single shared one belongs to whichever
	// group ran last, and asking another group for it returns 404 rather than
	// that group's own latest run.
	if runID == "" {
		runID = ec.privateValue(ctx, idKey("evalrun", evalID))
	}
	if runID != "" {
		run, err := ec.evalClient.GetOpenAIEvalRun(ctx, evalID, runID)
		if err == nil {
			ec.sayWhichRun(cmd, explicit, run.ID)
			return run, nil
		}
		if explicit || !eval_api.IsNotFound(err) {
			return nil, messages.ReadingRun(runID, err)
		}
	}

	if !mayGuess {
		return nil, messages.RunMustBeNamed(evalID)
	}

	// The service does not order runs, so one row is not enough to know which is
	// newest -- asking for a single one and taking it silently settled on
	// whatever came back first, which `run cancel` and the output commands then
	// acted on.
	list, err := ec.evalClient.ListOpenAIEvalRuns(ctx, evalID, newestRunCandidates)
	if err != nil {
		if eval_api.IsNotFound(err) {
			return nil, messages.EvalNotDeployed(evalID, ec.deployCommand(ctx))
		}
		return nil, messages.ListingRuns(evalID, err)
	}
	if list == nil || len(list.Data) == 0 {
		return nil, messages.EvalHasNoRuns(evalID)
	}
	newest := newestRunIn(list.Data)
	ec.sayWhichRun(cmd, explicit, newest.ID)
	return newest, nil
}

// newestRunCandidates bounds the page read to settle which run is newest.
const newestRunCandidates = 50

// newestRunIn picks the most recently created run.
//
// The same rule idsNamedIn uses for evals: timestampString normalizes both
// shapes the service writes created_at in, and those sort chronologically as
// text. A run with no usable timestamp never wins on the strength of its
// position, and if none of them carry one the list order is all there is.
func newestRunIn(runs []eval_api.OpenAIEvalRun) *eval_api.OpenAIEvalRun {
	best, bestAt := -1, ""
	for i := range runs {
		at := timestampString(runs[i].CreatedAt)
		if at == "" {
			continue
		}
		if best == -1 || at > bestAt {
			best, bestAt = i, at
		}
	}
	if best == -1 {
		return &runs[0]
	}
	return &runs[best]
}

// sayWhichRun names the run a command settled on for itself.
//
// The fallback is the reason these commands are usable without an id, and it
// is also the reason a reader can be looking at a different run than they
// think. Naming it costs one line and removes the doubt. A caller that named
// the run already knows, and JSON is parsed rather than read.
func (ec *evalContext) sayWhichRun(cmd *cobra.Command, explicit bool, runID string) {
	if explicit || isJSON(cmd) {
		return
	}
	fmt.Fprint(cmd.ErrOrStderr(), messages.UsingLastRun(runID))
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
		fmt.Fprint(w, messages.ItemResultTotals(c.Total, c.Passed, c.Failed, c.Errored, c.Skipped))
		fmt.Fprint(w, messages.ScoredPassRateLine(c.Passed, c.Passed+c.Failed))
		fmt.Fprintln(w)
	}

	if len(run.PerTestingCriteria) > 0 {
		if c := run.ResultCounts; c != nil && c.Total > 0 {
			fmt.Fprint(w, messages.CriterionResultReconciliation(
				c.Total, len(run.PerTestingCriteria), c.Total*len(run.PerTestingCriteria)))
		}
		rows := make([][]string, 0, len(run.PerTestingCriteria))
		for _, cr := range run.PerTestingCriteria {
			if failedOnly && cr.Failed == 0 {
				continue
			}
			// Every status the service models gets a column. Reporting only
			// passed and failed made a criterion's counts fall short of the
			// run's item count with nothing on screen explaining the shortfall.
			scored := cr.Passed + cr.Failed
			total := scored + cr.Errored + cr.Skipped
			rows = append(rows, []string{
				cr.TestingCriteria,
				strconv.Itoa(cr.Passed),
				strconv.Itoa(cr.Failed),
				strconv.Itoa(cr.Skipped),
				strconv.Itoa(cr.Errored),
				fmt.Sprintf("%d/%d", scored, total),
				criterionPassRate(cr.Passed, scored),
			})
		}
		if len(rows) > 0 {
			if err := emitTable(w,
				[]string{"CRITERION", "PASS", "FAIL", "SKIP", "ERROR", "SCORED", "PASS RATE"},
				rows); err != nil {
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
		shown := 0
		for _, it := range items {
			// One row per evaluated sample, not per verdict: a sample that
			// failed three evaluators is one sample to go and look at, and
			// listing it three times buries how much is actually wrong.
			out := classifyItem(it)

			// --failed-only means failed. It used to keep any row that did not
			// pass, so a run that errored everywhere answered it with rows the
			// totals counted as errored, and the footer then called them
			// failures.
			if failedOnly && out.Status != itemFailed {
				continue
			}
			shown++

			// No position column. It numbered within the current filter, so the
			// same sample carried a different number depending on the flags while
			// reading like an identifier -- and ITEM already carries the id, which
			// is what `run output show` accepts.
			//
			// No aggregate score either: averaging evaluators that measure
			// different things on different scales produces a number no
			// evaluator reported. Scores live per evaluator in `output show`.
			rows = append(rows, []string{
				it.ID,
				out.Status,
				out.ResultsBreakdown(),
				truncate(out.AttentionText(2), 40),
				truncate(out.Reason, 44),
			})
		}
		// Only the first failure's reason fits a cell; `run output show` has
		// the rest.
		if err := emitTable(w,
			[]string{"ITEM", "STATUS", "RESULTS", "ATTENTION", "REASON"},
			rows); err != nil {
			return err
		}
		if failedOnly {
			fmt.Fprint(w, messages.FilteredItemCount(shown, len(items), itemFailed))
		}
	}

	if url := runLink(run.ReportURL, run.PortalURL); url != "" {
		fmt.Fprint(w, messages.PortalLinkAfterRows(color.CyanString(url)))
	}
	return nil
}

// truncate keeps a table readable when a reason runs to a paragraph. The full
// text is always in `-o json`.
//
// Counted in runes: an evaluator name or reason is free-form text, and cutting
// it at a byte split multibyte characters down the middle, which reaches the
// terminal as a replacement glyph.
func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", "")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:max(n, 0)])
	}
	return string(r[:n-1]) + "…"
}

// Export format. Only JSON: the results are a nested document, and the flat
// formats that used to sit beside it exported a different object.
const formatJSON = "json"

// criterionPassRate reports a criterion's rate over what it actually scored.
//
// Rows it errored on or skipped are outside the denominator: they say nothing
// about the evaluator's judgement, and counting them turns an infrastructure
// problem into a quality signal.
func criterionPassRate(passed, scored int) string {
	if scored == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(passed)/float64(scored))
}
