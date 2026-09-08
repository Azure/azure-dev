// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// runOutputListFlags carries what `run output list` was asked for.
type runOutputListFlags struct {
	failedOnly bool
	status     string
	outFile    string
	endpoint   string
	groupName  string
	run        string
	limit      int
	pageToken  string
	all        bool
}

// runOutputListAction lists the per-sample results of one run.
type runOutputListAction struct {
	cmd   *cobra.Command
	flags *runOutputListFlags
	runID string
}

func newRunOutputListCommand() *cobra.Command {
	flags := &runOutputListFlags{}

	cmd := &cobra.Command{
		Use:   "list [run]",
		Short: "List the per-sample results of a run.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The positional wins over the flag; both name the same run, and
			// the one typed at the end of the line is the more deliberate.
			runID := firstArg(args)
			if runID == "" {
				runID = flags.run
			}
			return (&runOutputListAction{cmd: cmd, flags: flags, runID: runID}).Run()
		},
	}

	cmd.Flags().BoolVar(&flags.failedOnly, "failed-only", false,
		"Show only the items that failed. Shorthand for --status failed.")
	cmd.Flags().StringVar(&flags.status, "status", "",
		"Show only items with these outcomes: passed, failed, errored, skipped (comma-separated).")
	cmd.Flags().StringVar(&flags.outFile, "output-file", "",
		"Write JSON results to this path. Writes every row unless --limit narrows it.")
	addPagingFlags(cmd, &flags.limit, &flags.pageToken, &flags.all, outputItemPageSize)
	addRunFlag(cmd, &flags.run)
	addEvalFlag(cmd, &flags.groupName)
	// Registered wherever a declared name is resolved, so a configuration
	// outside ./evals can be addressed by every command, not just `run start`.
	addEvalPathFlag(cmd, new(string))
	cmd.Flags().StringVar(&flags.endpoint, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// walksEveryPage reports whether the listing should fetch every page.
//
// --output-file is a bulk destination and takes the whole run by default, but
// an explicit --limit is the caller naming a size, and quietly writing more
// than they asked for is no smaller a surprise than writing less. Changed()
// rather than the value, so an explicit 0 still counts as having asked.
func (f *runOutputListFlags) walksEveryPage(cmd *cobra.Command) bool {
	return f.all || (f.outFile != "" && !cmd.Flags().Changed("limit"))
}

func (a *runOutputListAction) Run() error {
	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.flags.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	evalID, err := resolveEvalID(a.cmd, ec, a.flags.groupName)
	if err != nil {
		return err
	}

	run, err := ec.latestOrNamedRun(a.cmd, evalID, a.runID, true)
	if err != nil {
		return err
	}

	// The run carries totals and a per-criterion breakdown. The output
	// items are the rows themselves, which is what "which one failed,
	// and why" needs. A run that never produced any still renders its
	// totals rather than failing.
	// A generated dataset runs to a thousand rows, each carrying a
	// result per evaluator, so an unbounded listing floods the terminal.
	pageSize := pageSizeOr(a.flags.limit, a.flags.walksEveryPage(a.cmd), outputItemPageSize)
	// One predicate for both views, so `-o json` and the table cannot
	// disagree about which rows the filter kept.
	keep, err := parseStatusFilter(a.flags.status)
	if err != nil {
		return err
	}
	if a.flags.failedOnly {
		if keep == nil {
			keep = map[string]bool{}
		}
		keep[itemFailed] = true
	}
	items, err := filteredItemPage(
		ctx, ec.evalClient, evalID, run.ID, pageSize, a.flags.pageToken, keep, fetchItemPage)
	if err != nil {
		return messages.ReadingRunResults(run.ID, err)
	}
	rows := items.Data

	// A bare array, as every other list emits. Wrapping the rows beside
	// the run made `-o json` the one listing a script could not iterate,
	// and it failed silently: the loop walked the two keys instead. The
	// run itself is what `run show` answers.
	if a.flags.outFile != "" {
		// These rows carry prompts, answers and the reasons an evaluator gave.
		// os.Create takes the process umask, which commonly leaves them
		// world-readable; writeFileAtomic keeps them at 0600 and replaces the
		// destination in one step whatever it was before.
		var body bytes.Buffer
		if err := emitJSONList(&body, rows); err != nil {
			return err
		}
		if err := writeFileAtomic(a.flags.outFile, body.Bytes()); err != nil {
			return err
		}
		// A file narrowed by --limit holds part of the run, and on disk there is
		// nothing left to say so. Stdout carries no rows on this path, so the
		// cursor goes there rather than leaving the file to look complete.
		if items.HasMore && items.LastID != "" && !isJSON(a.cmd) {
			fmt.Fprint(a.cmd.OutOrStdout(), messages.MoreItemsToList(items.LastID))
		}
		return nil
	}
	if isJSON(a.cmd) {
		cursor := ""
		if items.HasMore {
			cursor = items.LastID
		}
		return emitJSONPage(a.cmd.OutOrStdout(), rows, nil, cursor)
	}
	if err := renderResults(a.cmd.OutOrStdout(), evalID, run, rows, a.flags.failedOnly); err != nil {
		return err
	}
	if items.HasMore && items.LastID != "" {
		fmt.Fprint(a.cmd.OutOrStdout(), messages.MoreItemsToList(items.LastID))
	}
	return nil
}

// newRunOutputShowCommand reads one evaluated row by its id.
//
// The listing truncates the input and the reason to keep a table readable, so
// this is how the whole of either is seen.
// runOutputShowFlags carries what `run output show` was asked for.
type runOutputShowFlags struct {
	run       string
	endpoint  string
	groupName string
}

// runOutputShowAction reports one evaluated row.
type runOutputShowAction struct {
	cmd    *cobra.Command
	flags  *runOutputShowFlags
	itemID string
}

func newRunOutputShowCommand() *cobra.Command {
	flags := &runOutputShowFlags{}

	cmd := &cobra.Command{
		Use:   "show <output-item>",
		Short: "Show a single evaluated row. The id is the ITEM column of `run output list`.",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return messages.OutputItemRequired(flags.groupName)
			}
			return requiredArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&runOutputShowAction{cmd: cmd, flags: flags, itemID: args[0]}).Run()
		},
	}

	addRunFlag(cmd, &flags.run)
	addEvalFlag(cmd, &flags.groupName)
	// Registered wherever a declared name is resolved, so a configuration
	// outside ./evals can be addressed by every command, not just `run start`.
	addEvalPathFlag(cmd, new(string))
	cmd.Flags().StringVar(&flags.endpoint, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *runOutputShowAction) Run() error {
	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.flags.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	evalID, err := resolveEvalID(a.cmd, ec, a.flags.groupName)
	if err != nil {
		return err
	}

	run, err := ec.latestOrNamedRun(a.cmd, evalID, a.flags.run, true)
	if err != nil {
		return err
	}

	item, err := ec.evalClient.GetOutputItem(ctx, evalID, run.ID, a.itemID)
	if err != nil {
		if eval_api.IsNotFound(err) {
			return messages.OutputItemNotFound(a.itemID, run.ID)
		}
		return messages.ReadingOutputItem(a.itemID, err)
	}
	if isJSON(a.cmd) {
		return emitJSON(a.cmd.OutOrStdout(), item)
	}
	return renderOutputItem(a.cmd.OutOrStdout(), item)
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

// runOutputExportFlags carries what `run output export` was asked for.
type runOutputExportFlags struct {
	format    string
	outFile   string
	force     bool
	endpoint  string
	groupName string
	run       string
}

// runOutputExportAction writes the complete result document for one run.
type runOutputExportAction struct {
	cmd   *cobra.Command
	flags *runOutputExportFlags
	runID string
}

func newRunOutputExportCommand() *cobra.Command {
	flags := &runOutputExportFlags{}

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
			// The positional wins over the flag; both name the same run, and
			// the one typed at the end of the line is the more deliberate.
			runID := firstArg(args)
			if runID == "" {
				runID = flags.run
			}
			return (&runOutputExportAction{cmd: cmd, flags: flags, runID: runID}).Run()
		},
	}

	addRunFlag(cmd, &flags.run)
	cmd.Flags().StringVar(&flags.format, "format", formatJSON,
		fmt.Sprintf("Output format. Only %s is supported.", formatJSON))
	cmd.Flags().StringVar(&flags.outFile, "output-file", "",
		"Write the document to this path. Required; pass - to write to stdout.")
	cmd.Flags().BoolVar(&flags.force, "force", false,
		"Replace an output file that already exists.")
	addEvalFlag(cmd, &flags.groupName)
	// Registered wherever a declared name is resolved, so a configuration
	// outside ./evals can be addressed by every command, not just `run start`.
	addEvalPathFlag(cmd, new(string))
	cmd.Flags().StringVar(&flags.endpoint, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *runOutputExportAction) Run() error {
	if f := strings.ToLower(strings.TrimSpace(a.flags.format)); f != "" && f != formatJSON {
		return messages.ExportFormatUnsupported(a.flags.format, formatJSON)
	}

	// Both settled before the run is read. The document carries every evaluated
	// row, so where it lands is not a detail to discover after two round trips
	// -- and a destination that already holds one is refused before anything
	// could overwrite it.
	dest := strings.TrimSpace(a.flags.outFile)
	if dest == "" {
		return messages.ExportNeedsAnOutputFile()
	}
	if dest != exportToStdout {
		if err := refuseExisting(dest, a.flags.force); err != nil {
			return err
		}
	}

	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.flags.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	evalID, err := resolveEvalID(a.cmd, ec, a.flags.groupName)
	if err != nil {
		return err
	}

	run, err := ec.latestOrNamedRun(a.cmd, evalID, a.runID, true)
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

	if dest == exportToStdout {
		return writeExport(a.cmd.OutOrStdout(), doc)
	}

	// On stderr, so it cannot reach a document being parsed downstream.
	fmt.Fprint(a.cmd.ErrOrStderr(), messages.ExportCarriesSourceContent(dest))

	// The document carries every evaluated row, so it holds prompts, answers
	// and evaluator reasons. os.Create takes the process umask and commonly
	// leaves that world-readable.
	//
	// Streamed into the temporary file rather than buffered and copied: the
	// size of a run's export is the service's to decide, not this command's,
	// and the only reason to hold it whole was to hand it to a writer that
	// takes bytes.
	if err := writeFileAtomicFunc(dest, func(w io.Writer) error {
		return writeExport(w, doc)
	}); err != nil {
		return err
	}
	// Counted after the write, so the number describes a file that is there.
	fmt.Fprint(a.cmd.ErrOrStderr(), messages.ExportedTestCases(len(doc.Items), dest))
	return nil
}

// exportToStdout is the --output-file value that means "do not write a file".
// Spelled rather than defaulted, so nothing lands on a terminal by accident.
const exportToStdout = "-"

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
	// acted on. A capped read has the same defect one step removed: the newest
	// row need not be in the first N of an unordered listing. collectPages
	// bounds the walk and reports one it could not finish as an error.
	list, err := ec.evalClient.ListOpenAIEvalRuns(ctx, evalID, 0)
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
	outcome := classifyItem(*item)

	// The service's own status is `completed` even for a row whose every result
	// errored, so it is reported beside the derived outcome rather than instead
	// of it: printing the lifecycle state alone made a failed row read as a
	// success, and dropping it hides which of the two the reader is seeing.
	fmt.Fprint(w, messages.TestCaseHeading(outcome.Status))
	fields := []field{
		{"Item ID", item.ID},
		{"Run", item.RunID},
		{"Status", outcome.Status},
		{"Results", outcome.ResultsBreakdown()},
	}
	if item.Status != "" && item.Status != outcome.Status {
		fields = append(fields, field{"Service status", item.Status})
	}
	if outcome.Reason != "" {
		fields = append(fields, field{"Reason", outcome.Reason})
	}
	if err := emitDetail(w, fields); err != nil {
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
		if err := renderEvaluatorResult(w, name, byName[name]); err != nil {
			return err
		}
	}
	return nil
}

// renderEvaluatorResult prints one evaluator's section of the detail view.
//
// Everything it says comes from the result. Reasons are printed whole: this
// command is the one place they are not truncated, which is the reason to run
// it at all, and the listing above already showed the clipped version.
func renderEvaluatorResult(w io.Writer, name string, results []eval_api.OutputResult) error {
	if len(results) == 0 {
		return nil
	}
	// The service repeats the evaluator's name in `metric` for a single-score
	// evaluator, so a result only names a dimension when it says something else.
	dimensions := make([]eval_api.OutputResult, 0, len(results))
	for _, r := range results {
		if r.Metric != "" && r.Metric != name {
			dimensions = append(dimensions, r)
		}
	}
	lead := results[0]

	fmt.Fprint(w, messages.EvaluatorSectionHeading(name))
	section := []field{{"Status", lead.Outcome()}}
	if e := lead.SampleError(); e != nil && e.Code != "" {
		section = append(section, field{"Code", e.Code})
	}
	if score := formatScore(lead.Score); score != "-" && len(dimensions) == 0 {
		section = append(section, field{"Score", score})
	}
	if err := emitDetail(w, section); err != nil {
		return err
	}
	if why := resultExplanation(lead); why != "" {
		fmt.Fprint(w, messages.EvaluatorSectionReason(lead.Outcome(), why))
	}

	if len(dimensions) == 0 {
		// Said rather than left blank, and never invented: a reader who cannot
		// see dimensions needs to know whether this rubric has none or the
		// service did not return them.
		if isRubricName(name) {
			fmt.Fprint(w, messages.RubricDimensionsNotReturned())
		}
		return nil
	}

	rows := make([][]string, 0, len(dimensions))
	for _, d := range dimensions {
		rows = append(rows, []string{
			d.Metric,
			formatScore(d.Score),
			d.Outcome(),
			singleLine(d.Reason),
		})
	}
	fmt.Fprint(w, messages.RubricDimensionsHeading())
	return emitTable(w, []string{"DIMENSION", "SCORE", "RESULT", "REASON"}, rows)
}

// isRubricName reports whether a missing dimension list is worth remarking on.
//
// Only a rubric has dimensions to be missing. Saying "not returned by service"
// under every built-in would report an absence that was never expected.
func isRubricName(name string) bool {
	return strings.Contains(strings.ToLower(name), "rubric")
}

// singleLine flattens a reason for a table cell. The whole text is in the
// section above it and in `-o json`.
func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// resultExplanation is the line printed under a verdict to say why.
//
// A judge's reason is what a failing row is looked at for, so it wins. An
// evaluator that never ran has no reason to give but does carry the failure it
// recorded, and printing a bare "errored" with nothing under it sent the reader
// to the portal for a code the response already held. A result that arrived
// carrying neither is its own finding, and saying so is what stops it reading
// as a rendering bug.
func resultExplanation(r eval_api.OutputResult) string {
	if r.Reason != "" {
		return r.Reason
	}
	if e := r.SampleError(); e != nil {
		switch {
		case e.Code != "" && e.Message != "":
			return e.Code + ": " + e.Message
		case e.Code != "":
			return e.Code
		default:
			return e.Message
		}
	}
	if r.Outcome() == eval_api.ResultErrored && r.Status == "" {
		return messages.EvaluatorReturnedNothing()
	}
	return ""
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
	resolvedEval string,
	run *eval_api.OpenAIEvalRun,
	items []eval_api.OutputItem,
	failedOnly bool,
) error {
	evalName := runEvalName(run, resolvedEval)
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
		// The export is the whole run, so it is the answer to "nothing here
		// matched, where is the rest of it" -- which is exactly the case that
		// used to be answered with a full stop.
		fmt.Fprint(w, messages.ExportCompleteResults(evalName, run.ID))
	} else {
		fmt.Fprintln(w)
		rows := make([][]string, 0, len(items))
		shown := 0
		firstItem := ""
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
			if firstItem == "" {
				firstItem = it.ID
			}

			// No position column. It numbered within the current filter, so the
			// same sample carried a different number depending on the flags while
			// reading like an identifier -- and ITEM already carries the id, which
			// is what `run output show` accepts.
			//
			// No aggregate score either: averaging evaluators that measure
			// different things on different scales produces a number no
			// evaluator reported. Scores live per evaluator in `output show`.
			//
			// No evaluator names and no reason: both are one evaluator's account
			// of one row, and a cell truncated to forty characters is the worst
			// place to read either. They belong to `output show`, which prints
			// them whole.
			rows = append(rows, []string{it.ID, out.Status, out.ResultsBreakdown()})
		}
		if err := emitTable(w, []string{"ITEM", "STATUS", "RESULTS"}, rows); err != nil {
			return err
		}
		if failedOnly {
			// Against the run's own item total, not the rows on screen. The slice
			// arriving here is already filtered, so counting it both ways printed
			// "6 of 6" for a run of fifteen.
			total := len(items)
			if c := run.ResultCounts; c != nil && c.Total > 0 {
				total = c.Total
			}
			fmt.Fprint(w, messages.FilteredItemCount(shown, total, itemFailed))
		}
		if firstItem != "" {
			// Printed resolved, down to an item that is actually in the table
			// above. A reader who has to work out which eval a run belonged to,
			// and then retype a row id, is being asked to redo the lookup the
			// listing just did -- and a line with a placeholder in it reads like
			// a command and is not one.
			fmt.Fprint(w, messages.ViewItemDetails(evalName, run.ID, firstItem))
		}
		// Offered whether or not a row survived the filter. The export is the
		// whole run, so it is the answer to "nothing here matched, where is the
		// rest of it" -- which is exactly when it used to be withheld.
		fmt.Fprint(w, messages.ExportCompleteResults(evalName, run.ID))
	}

	if url := runLink(run.ReportURL, run.PortalURL); url != "" {
		fmt.Fprint(w, messages.PortalLinkAfterRows(color.CyanString(url)))
	}
	return nil
}

// itemPager fetches one service page of output items. Named so the walk below
// can be exercised without a service; production passes fetchItemPage.
type itemPager func(
	ctx context.Context,
	client *eval_api.EvalClient,
	evalID, runID string,
	pageSize int,
	after string,
) (*eval_api.OutputItemList, error)

func fetchItemPage(
	ctx context.Context,
	client *eval_api.EvalClient,
	evalID, runID string,
	pageSize int,
	after string,
) (*eval_api.OutputItemList, error) {
	return client.ListOutputItemsPage(ctx, evalID, runID, pageSize, after)
}

// filteredItemPage fills one page with rows the filter keeps, reading as many
// service pages as that takes.
//
// The output-items endpoint has no status parameter, so a filter can only be
// applied here. Applying it to a single fetched page meant `--failed-only`
// answered "no failing rows" whenever the first ten happened to pass, while the
// failures sat on page two -- a wrong answer to the one question the flag
// exists for. The walk stops as soon as the page is full, so a failing run
// still costs one request.
func filteredItemPage(
	ctx context.Context,
	client *eval_api.EvalClient,
	evalID, runID string,
	pageSize int,
	after string,
	keep map[string]bool,
	fetch itemPager,
) (*eval_api.OutputItemList, error) {
	page, err := fetch(ctx, client, evalID, runID, pageSize, after)
	if err != nil {
		return nil, err
	}
	// No filter, or no page size to fill: one page is the page.
	if keep == nil || pageSize <= 0 {
		return page, nil
	}

	kept := make([]eval_api.OutputItem, 0, len(page.Data))
	for {
		for _, it := range page.Data {
			if keep[classifyItem(it).Status] {
				kept = append(kept, it)
			}
		}
		if len(kept) >= pageSize {
			// The cursor is the last row this page actually shows, so resuming
			// from it neither repeats nor skips one.
			kept = kept[:pageSize]
			return &eval_api.OutputItemList{
				Data:    kept,
				HasMore: true,
				LastID:  kept[len(kept)-1].ID,
			}, nil
		}
		if !page.HasMore || page.LastID == "" {
			return &eval_api.OutputItemList{Data: kept}, nil
		}
		if page, err = fetch(ctx, client, evalID, runID, pageSize, page.LastID); err != nil {
			return nil, err
		}
	}
}

// runEvalName is the declared name the run belongs to, falling back to the
// service id and then to the identifier the caller resolved to fetch it.
//
// The declared one is what the reader recognizes; the id is what the response
// carries. The caller's is the backstop, because a run that carries neither
// printed `--eval ` with nothing after it -- a suggested command that cannot
// run, in the one place whose whole claim is that it can.
func runEvalName(run *eval_api.OpenAIEvalRun, resolved string) string {
	if name := run.Metadata[metaEvalName]; name != "" {
		return name
	}
	if run.EvalID != "" {
		return run.EvalID
	}
	return resolved
}

// truncate keeps a table readable when a reason runs to a paragraph. The full
// text is always in `-o json`.
//
// Counted in runes: an evaluator name or reason is free-form text, and cutting
// it at a byte split multi-byte characters down the middle, which reaches the
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
