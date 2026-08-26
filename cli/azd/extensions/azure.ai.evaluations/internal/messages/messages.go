// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package messages holds every string this extension shows a user.
//
// One file, so the whole voice of the CLI can be reviewed in one sitting and a
// wording change never has to be hunted through the command tree. The only
// extension package it imports is exterrors, which holds no wording of its own,
// so every other package can use this one.
//
// Conventions, so the set stays consistent:
//
//   - Errors state what went wrong and, where there is one, the way out.
//     Lowercase, no trailing period: azd renders them after "ERROR: ".
//   - A name the user chose is quoted with %q; an identifier the service
//     assigned is not, because it is already unmistakable.
//   - A filesystem path goes through filepath.ToSlash first. %q escapes a
//     Windows separator, so `evals\eval.yaml` prints as "evals\\eval.yaml" and
//     a reader who copies it back gets a path that does not exist.
//   - Progress and success lines are sentences with a capital and no period.
//   - A printed line carries its own newlines, so a call site is a bare Fprint.
//   - Nothing here decides *whether* to print. That stays at the call site.
package messages

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"azureaieval/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ---------------------------------------------------------------------------
// Running an eval
// ---------------------------------------------------------------------------

// NoEvalToRun reports a run with nothing resolved to run.
func NoEvalToRun() error {
	return errors.New("no eval to run")
}

// EvalHasNoDataset reports an eval whose rows cannot be located.
//
// Named separately from the traces and responses cases because the way out is
// different: this one is answered by a dataset, not by a source block.
func EvalHasNoDataset(eval string) error {
	return fmt.Errorf(
		"eval %q references no dataset and declares no source. Add a dataset: to "+
			"score rows you supply, or a source: to score traces or stored responses",
		eval)
}

// DatasetFileEmpty reports a local dataset file that parsed but held no rows.
func DatasetFileEmpty(path string) error {
	return fmt.Errorf("dataset file %q has no rows", filepath.ToSlash(path))
}

// DatasetOverrideNeedsDeclaredEval reports --dataset passed against a bare id.
func DatasetOverrideNeedsDeclaredEval() error {
	return errors.New(
		"--dataset overrides the dataset an eval declares, so it needs a " +
			"declared eval; pass --eval with a name from the configuration")
}

// DatasetNotInCatalog reports a --dataset the configuration does not declare.
func DatasetNotInCatalog(dataset, configPath string) error {
	return fmt.Errorf("dataset %q is not in the catalog in %s", dataset, filepath.ToSlash(configPath))
}

// NothingToGenerateFrom refuses a generation request carrying no sources.
//
// The service answers one with "At least one source is required", wrapped in a
// 400 and thirty lines of JSON. Nothing about that names the two things a
// reader can actually supply.
func NothingToGenerateFrom() error {
	return errors.New(
		"nothing to generate from: pass --target <agent> to seed from an " +
			"agent's instructions, or declare one under target: in the eval " +
			"configuration. A trace-backed eval names its agent under source:, " +
			"which selects traces to read and does not seed generation")
}

// SelectEvaluatorsPrompt asks which references the eval grades on.
func SelectEvaluatorsPrompt() string {
	return "Select evaluators to grade with:"
}

// SelectingEvaluators reports a failed evaluator prompt.
func SelectingEvaluators(err error) error {
	return fmt.Errorf("selecting evaluators: %w", err)
}

// NoEvaluatorsChosen reports an eval that would grade on nothing.
func NoEvaluatorsChosen() error {
	return errors.New(
		"an eval has to grade on at least one evaluator: select one, or pass " +
			"--evaluator")
}

// GateNeedsTheWait refuses a gate on a run the command will not wait for.
//
// The two flags together read as "start it and tell me if it regressed", but
// the verdict does not exist yet when --no-wait returns, so the gate was
// silently dropped and the command exited 0 however the run turned out.
func GateNeedsTheWait() error {
	return errors.New(
		"--fail-on needs a result to judge, and --no-wait returns before there " +
			"is one. Drop --no-wait, or reattach with `azd ai eval run show " +
			"<run> --wait --fail-on <gate>`")
}

// GateOutlivedTheWait reports a gate that never got a verdict because the run
// outlived the wait.
//
// Without a gate this is not a failure and exits 0, which is why the run was
// reported and the reattach line printed. With one, exiting 0 tells a pipeline
// the gate passed when nothing was ever judged -- the same silent drop
// GateNeedsTheWait refuses up front, arrived at by running long instead.
func GateOutlivedTheWait(runID string, budget time.Duration) error {
	return fmt.Errorf(
		"run %s outlived the %s wait, so --fail-on never got a result to judge. "+
			"The run is still going: reattach with `azd ai eval run show %s "+
			"--wait --fail-on <gate>`", runID, budget, runID)
}

// DatasetHasUnregisteredEdits reports local rows no deployed version holds.
func DatasetHasUnregisteredEdits(dataset, deployCmd string) error {
	return fmt.Errorf(
		"dataset %q has local edits that are not registered.\n"+
			"  Run `%s` to register them, or `--eval <id>` to run against "+
			"an existing eval",
		dataset, deployCmd)
}

// StartingRun reports the service refusing to start the run.
func StartingRun(err error) error {
	return fmt.Errorf("starting the evaluation run: %w", err)
}

// RunStarted reports a submitted run that was not waited on.
func RunStarted(runID, status string) string {
	return fmt.Sprintf("Started run %s (status: %s)\n", runID, status)
}

// ReattachToRun says how to come back to a run started with --no-wait.
func ReattachToRun(runID, evalID string) string {
	return fmt.Sprintf("Reattach with: azd ai eval run show %s --eval %s\n", runID, evalID)
}

// ReadingPreviousRuns reports a failure to look up what an eval last ran.
func ReadingPreviousRuns(evalID string, err error) error {
	return fmt.Errorf("reading previous runs of eval %s: %w", evalID, err)
}

// EvalHasNoPreviousRun reports an eval named by id that has nothing to repeat.
func EvalHasNoPreviousRun(evalID string) error {
	return fmt.Errorf(
		"eval %s has no previous run to repeat, so there is no target or dataset "+
			"to reuse.\n"+
			"  Run it from the config once with `azd ai eval run start`, or name an "+
			"eval that declares one with `--eval`",
		evalID)
}

// PollingRun reports a failure while waiting for a run to finish.
func PollingRun(runID string, err error) error {
	return fmt.Errorf("polling run %s: %w", runID, err)
}

// WaitBudgetSpent reports a run that outlived the foreground wait.
func WaitBudgetSpent(runID string, budget time.Duration) string {
	return fmt.Sprintf(
		"Run %s is still going after %s, so the wait stopped, not the run.\n",
		runID, budget)
}

// WaitInterrupted reports a wait cut short, naming the run still in flight.
func WaitInterrupted(runID string, err error) error {
	return fmt.Errorf(
		"stopped waiting on run %s, which is still running: %w. "+
			"Pick it back up with `azd ai eval run show %s`",
		runID, err, runID)
}

// RunStatusLine reports a status change seen while polling.
func RunStatusLine(status string) string {
	return fmt.Sprintf("  status: %s\n", status)
}

// RunFinishedWithStatus reports a run that ended in something other than completed.
func RunFinishedWithStatus(runID, status string) error {
	return fmt.Errorf("run %s finished with status %s", runID, status)
}

// OverallPassRate reports the share of the rows an evaluator scored that passed
// every evaluator.
//
// The denominator is named rather than left as a bare fraction. Rows nothing
// could grade are outside it, so a run that errored on most of its samples can
// report a high rate, and "of N scored" is what stops that reading as a verdict
// on the whole run. It is also the figure `--fail-on pass-rate` compares.
func OverallPassRate(rate string, passed, scored, unscored int) string {
	if unscored > 0 {
		return fmt.Sprintf("\nOverall pass rate: %s  (%d of %d scored; %d not scored)\n",
			rate, passed, scored, unscored)
	}
	return fmt.Sprintf("\nOverall pass rate: %s  (%d/%d)\n", rate, passed, scored)
}

// SamplesErrored reports rows the run could not score at all.
func SamplesErrored(errored int) string {
	return fmt.Sprintf("%d sample(s) errored and were not scored.\n", errored)
}

// ViewFailingSamples points at the command that lists the rows that failed.
func ViewFailingSamples() string {
	return "\nView failing samples: azd ai eval run output list --failed-only\n"
}

// ErroredNotScored annotates an evaluator's row with what it could not score.
func ErroredNotScored(errored int) string {
	return fmt.Sprintf("(%d errored, not scored)", errored)
}

// EvalNotDeployed reports an eval id the project does not hold.
func EvalNotDeployed(evalID, deployCmd string) error {
	return fmt.Errorf(
		"no eval %q in this project; "+
			"`%s` creates the ones your config declares", evalID, deployCmd)
}

// NoEnvironmentToRememberEval reports an eval whose id had nowhere to be kept.
//
// `create` publishes the eval and records its id in the azd environment. With
// no environment there is nowhere to record it, so create reports success and
// the next command cannot find what it made. Saying "not deployed" there sends
// the reader to deploy it again, which lands in the same place.
func NoEnvironmentToRememberEval(eval string) error {
	return fmt.Errorf(
		"eval %q may exist in the project, but this directory has no azd "+
			"environment to have recorded its id in. Create one with "+
			"`azd env new <name>` and run `azd ai eval create` again, or name "+
			"the eval's id with --eval", eval)
}

// EvalNotDeployedYet reports a declared eval that no deploy has created.
func EvalNotDeployedYet(eval, deployCmd string) error {
	return fmt.Errorf(
		"eval %q is declared but has not been deployed to this environment yet; "+
			"run `%s` first", eval, deployCmd)
}

// NoEvalNamedOrDeclared reports a command with no eval to act on.
func NoEvalNamedOrDeclared(configPath string) error {
	return fmt.Errorf(
		"no eval was named and none is declared in %s; pass --eval with a name or an id",
		filepath.ToSlash(configPath))
}

// ListingRuns reports a failure to list an eval's runs.
func ListingRuns(evalID string, err error) error {
	return fmt.Errorf("listing runs for %q: %w", evalID, err)
}

// EvalHasNoRunsLine reports an eval with no runs to list.
func EvalHasNoRunsLine(evalID string) string {
	return fmt.Sprintf("Eval %s has no runs yet.\n", evalID)
}

// EvalHasNoRuns reports an eval with no run to fall back on.
func EvalHasNoRuns(evalID string) error {
	return fmt.Errorf("eval %s has no runs yet", evalID)
}

// RunMustBeNamed reports a command that changes a run reaching no run it can be
// sure of. It does not guess: the alternative is the newest run the service
// lists, which on a shared project may be someone else's.
func RunMustBeNamed(evalID string) error {
	return fmt.Errorf(
		"name the run to act on: this environment has no run recorded for eval %s, "+
			"and a command that changes a run will not pick one for you. "+
			"`azd ai eval run list --eval %s` shows the runs there are",
		evalID, evalID)
}

// ReadingRun reports a failure to read the run the caller named.
func ReadingRun(runID string, err error) error {
	return fmt.Errorf("reading run %s: %w", runID, err)
}

// CountsSummary renders a run's verdict counts on one line.
func CountsSummary(passed, failed, errored int) string {
	return fmt.Sprintf("%d passed, %d failed, %d errored", passed, failed, errored)
}

// RunAlreadyFinished reports a cancel asked of a run that already ended.
func RunAlreadyFinished(runID, status string) error {
	return fmt.Errorf("run %s already finished with status %q", runID, status)
}

// CancellingRun reports the service refusing to cancel the run.
func CancellingRun(runID string, err error) error {
	return fmt.Errorf("cancelling run %s: %w", runID, err)
}

// RunIsNow reports the state a cancelled run moved to.
func RunIsNow(runID, status string) string {
	return fmt.Sprintf("Run %s is now %s\n", runID, status)
}

// RunNotFound reports a run id the eval does not hold.
func RunNotFound(runID, evalID string) error {
	return fmt.Errorf("no run %q on eval %q", runID, evalID)
}

// DeletingRun reports the service refusing to delete the run.
func DeletingRun(runID string, err error) error {
	return fmt.Errorf("deleting run %s: %w", runID, err)
}

// RunDeleted confirms a deleted run.
func RunDeleted(runID string) string {
	return fmt.Sprintf("Deleted run %s\n", runID)
}

// ReadingRunResults reports a failure to read a run's per-sample rows.
func ReadingRunResults(runID string, err error) error {
	return fmt.Errorf("reading the results of run %s: %w", runID, err)
}

// OutputItemNotFound reports an output item the run does not hold.
func OutputItemNotFound(itemID, runID string) error {
	return fmt.Errorf(
		"no output item %q on run %s; "+
			"`azd ai eval run output list` shows the ones there are",
		itemID, runID)
}

// ReadingOutputItem reports a failure to read one evaluated row.
func ReadingOutputItem(itemID string, err error) error {
	return fmt.Errorf("reading output item %q: %w", itemID, err)
}

// RunStatusHeading opens the per-sample view of a run.
func RunStatusHeading(runID, status string) string {
	return fmt.Sprintf("Run %s  status: %s\n", runID, status)
}

// ResultTotals reports a run's verdict counts above the rows.
func ResultTotals(passed, failed, errored int) string {
	return fmt.Sprintf("Totals: %d passed, %d failed, %d errored\n\n", passed, failed, errored)
}

// NoFailingRows reports a --failed-only listing with nothing in it.
func NoFailingRows() string {
	return "\nNo failing rows.\n"
}

// NoRowsScored reports a run that has produced no rows yet.
func NoRowsScored() string {
	return "\nNo rows have been scored yet.\n"
}

// SamplesNeedingALook closes a --failed-only listing, holding the rows that
// failed apart from the rows nothing managed to score.
//
// One count covering both contradicted the totals printed two lines above it,
// which is what a reader compares it with: a run reporting 5 failed and 8
// errored closed with "13 sample(s) failed at least one evaluator".
func SamplesNeedingALook(failed, unscored int) string {
	if unscored == 0 {
		return fmt.Sprintf("\n%d sample(s) failed at least one evaluator.\n", failed)
	}
	if failed == 0 {
		return fmt.Sprintf("\n%d sample(s) could not be scored.\n", unscored)
	}
	return fmt.Sprintf(
		"\n%d sample(s) failed at least one evaluator, and %d could not be scored.\n",
		failed, unscored)
}

// GateSawUnscoredRows warns that a pass-rate gate judged only part of the run.
//
// The rate excludes rows nothing could grade, so a run that errored on most of
// its samples can clear a threshold on the few that survived. The gate is the
// one place a pipeline is guaranteed to read, so it is said there rather than
// left for someone to notice in the summary.
func GateSawUnscoredRows(unscored, total int) error {
	return fmt.Errorf(
		"%d of %d samples were not scored, so the pass rate this gate read covers "+
			"only the rest; use --fail-on any-failure to count them against the run",
		unscored, total)
}

// GeneratedNameNotAFileName reports a generated artifact name that would not
// stay inside the output directory, or that would produce a file whose name is
// read as a flag by whatever the path is handed to next.
func GeneratedNameNotAFileName(kind, name string) error {
	return fmt.Errorf(
		"%s name %q cannot be used as a file name: remove any of / \\ : , "+
			"do not start with -, and do not use . or ..",
		kind, name)
}

// OutputItemEmpty reports a row the service acknowledged but returned nothing
// for, which is a service fault rather than a missing item.
func OutputItemEmpty() error {
	return errors.New("the service returned no content for this output item")
}

// NotARegularFile reports an --output-file that names a directory or a device.
func NotARegularFile(path string) error {
	return fmt.Errorf("%s is not a regular file, so it will not be overwritten", filepath.ToSlash(path))
}

// CannotWriteInDirectory reports a destination directory that cannot be written
// to. A missing directory is reported as such: the wrapped error names the
// temporary file the writer chose, which the caller never asked for.
func CannotWriteInDirectory(dir string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s does not exist", filepath.ToSlash(dir))
	}
	return fmt.Errorf("cannot write in %s: %w", dir, err)
}

// OutputItemVerdict is one evaluator's line in `run output show`.
func OutputItemVerdict(evaluator, score, verdict string) string {
	return fmt.Sprintf("%s  %s  %s\n", evaluator, score, verdict)
}

// OutputItemEvaluator heads the dimensions of a rubric that scored per metric.
func OutputItemEvaluator(evaluator string) string {
	return evaluator + "\n"
}

// OutputItemMetric is one scored dimension under its evaluator.
func OutputItemMetric(metric, score, verdict string) string {
	return fmt.Sprintf("  %s  %s  %s\n", metric, score, verdict)
}

// OutputItemReason is the judge's explanation, indented under its verdict.
func OutputItemReason(reason string) string {
	return fmt.Sprintf("  %s\n", reason)
}

// OutputFileCannotHoldBothArtifacts reports one --output-dir file for two
// artifacts.
//
// Both jobs would resolve to it and both would write it, concurrently, and the
// configuration would then name it as a dataset and as an evaluator.
func OutputFileCannotHoldBothArtifacts(outputDir string) error {
	return fmt.Errorf(
		"--output-dir %q names a file, and this generates a dataset and an evaluator; "+
			"name a directory, or add --dataset or --evaluator to generate one of them",
		filepath.ToSlash(outputDir))
}

// UsingLastRun names the run a command chose when it was not given one.
//
// Written to stderr so it does not land in a redirected listing.
func UsingLastRun(runID string) string {
	return fmt.Sprintf("Using last run: %s\n", runID)
}

// PortalLinkAfterRows closes a per-sample listing with the run's one link.
//
// Labelled the way every other view labels it: the run's report page is in the
// portal, and a reader looking for the link should not have to know two words
// for it.
func PortalLinkAfterRows(url string) string {
	return fmt.Sprintf("\nPortal: %s\n", url)
}

// ExportFormatUnsupported reports an --format the export command cannot write.
func ExportFormatUnsupported(format, csv, json, jsonl string) error {
	return fmt.Errorf(
		"--format %q is not supported; use %s, %s or %s",
		format, csv, json, jsonl)
}

// FailOnInvalid reports a --fail-on value that is neither form of threshold.
func FailOnInvalid(spec string) error {
	return fmt.Errorf("--fail-on must be any-failure or pass-rate=<0..1>, got %q", spec)
}

// FailOnRateNotNumber reports a --fail-on pass rate that will not parse.
func FailOnRateNotNumber(rate string) error {
	return fmt.Errorf("--fail-on pass-rate must be a number, got %q", rate)
}

// FailOnRateOutOfRange reports a --fail-on pass rate outside 0..1.
func FailOnRateOutOfRange(value float64) error {
	return fmt.Errorf("--fail-on pass-rate must be between 0 and 1, got %v", value)
}

// GateNoResultCounts reports a gate that has nothing to measure against.
func GateNoResultCounts() string {
	return "the run reported no result counts, so the threshold cannot be checked"
}

// GateSamplesDidNotPass reports an any-failure gate that was breached.
func GateSamplesDidNotPass(unpassed, total int) string {
	return fmt.Sprintf("%d of %d samples did not pass", unpassed, total)
}

// GateNoRowsScored reports a pass-rate gate over a run that scored nothing.
func GateNoRowsScored() string {
	return "the run scored no rows, so its pass rate is below any threshold"
}

// GatePassRateBelow reports a pass-rate gate that was breached.
//
// One decimal, which is what the spec's hero scenario shows -- except when that
// rounds the actual rate onto the threshold. The gate compares exact values, so
// 7996/10000 breaches 0.8 while both read "80.0%", and the line would say a
// rate is below itself. Only that case is given more precision.
func GatePassRateBelow(actual, required float64) string {
	shown := fmt.Sprintf("%.1f", actual*100)
	if shown == fmt.Sprintf("%.1f", required*100) {
		shown = strconv.FormatFloat(actual*100, 'f', -1, 64)
	}
	return fmt.Sprintf("pass rate %s%% is below the required %.1f%%",
		shown, required*100)
}

// GateBreached is the block a breached gate leaves in a pipeline's log.
func GateBreached(reason string) string {
	return fmt.Sprintf("%s Evaluation gate: %s\n\nERROR: evaluation quality gate not met.\n",
		failedMark, reason)
}

// ---------------------------------------------------------------------------
// Generation
// ---------------------------------------------------------------------------

// GeneratedNameNeedsATarget reports a generation that can name neither the
// artifact nor the agent to derive its name from.
func GeneratedNameNeedsATarget(kind string) error {
	return fmt.Errorf(
		"no name for the generated %s and no target to derive one from: "+
			"pass --%s-name, or --target", kind, kind)
}

// GenerationFailed labels one half of a composite generate that did not finish.
//
// The label goes inside a structured error rather than around it: azd
// serializes a LocalError's own message and drops any wrapper, so wrapping
// would throw away the one word saying which job failed.
func GenerationFailed(kind string, err error) error {
	var local *azdext.LocalError
	if errors.As(err, &local) {
		labelled := *local
		labelled.Message = "generating the " + kind + ": " + local.Message
		return &labelled
	}
	return fmt.Errorf("generating the %s: %w", kind, err)
}

// multiError presents several failures as one line while keeping every cause
// reachable through errors.Is and errors.As.
//
// errors.Join would keep the causes but renders them one per line, and this is
// a single error the CLI prints after "ERROR: ".
type multiError struct {
	msg    string
	causes []error
}

func (m *multiError) Error() string   { return m.msg }
func (m *multiError) Unwrap() []error { return m.causes }

// SomeGenerationsFailed reports a composite generate where at least one job
// did not finish. The others may well have.
//
// Two structured failures of the same category stay structured, so an expired
// login still arrives as an auth error carrying its suggestion rather than as
// a flat string.
func SomeGenerationsFailed(failures []error) error {
	if len(failures) == 1 {
		return failures[0]
	}

	parts := make([]string, 0, len(failures))
	for _, f := range failures {
		parts = append(parts, f.Error())
	}
	joined := strings.Join(parts, "; ")

	var first *azdext.LocalError
	if !errors.As(failures[0], &first) {
		return &multiError{msg: joined, causes: failures}
	}
	for _, f := range failures[1:] {
		var other *azdext.LocalError
		if !errors.As(f, &other) || other.Category != first.Category {
			return &multiError{msg: joined, causes: failures}
		}
	}
	merged := *first
	merged.Message = joined
	return &merged
}

// GenerationStarting announces a job before it is submitted, so a long
// generation is not silent while it runs.
func GenerationStarting(kind, name string) string {
	return fmt.Sprintf("  Starting %s generation for %q...\n", kind, name)
}

// GenerationModelRequired reports a generation with no deployment to run on.
//
// Reached only when the target agent could not supply one either, so the flag
// is the whole of the way out.
func GenerationModelRequired() error {
	return errors.New("a model deployment is required to generate: pass --generation-model")
}

// ReadingInstructionFile reports an --agent-instruction-file that would not read.
func ReadingInstructionFile(path string, err error) error {
	return fmt.Errorf("reading --agent-instruction-file %q: %w", filepath.ToSlash(path), err)
}

// InstructionFileEmpty reports an --agent-instruction-file with nothing in it.
func InstructionFileEmpty(path string) error {
	return fmt.Errorf("--agent-instruction-file %q is empty", filepath.ToSlash(path))
}

// ReadingInstructions reports a declared instructions file that would not read.
func ReadingInstructions(named string, err error) error {
	return fmt.Errorf("reading instructions %q: %w", named, err)
}

// SeedingFromFile names the local file generation was seeded from.
func SeedingFromFile(path string) string {
	return fmt.Sprintf("  Seeding generation from %s.\n", filepath.ToSlash(path))
}

// SeedingFromAgent names the agent whose published instructions seeded generation.
func SeedingFromAgent(agent string) string {
	return fmt.Sprintf("  Seeding generation from the instructions of agent %q.\n", agent)
}

// WarningAgentUnreadable reports an agent that could not supply context.
//
// A misspelled --target is the common cause and answers 404, whose body is ten
// lines of URL, status rule and nested JSON for a fact that fits on one.
func WarningAgentUnreadable(agent string, err error) string {
	if notFound(err) {
		return fmt.Sprintf("  warning: no agent %q in this project, so generation "+
			"has no agent context to work from\n", agent)
	}
	return fmt.Sprintf("  warning: could not read agent %q for generation context: %v\n",
		agent, err)
}

// notFound reports a service answer of 404.
//
// Written here rather than imported from eval_api, because that package imports
// this one for its own wording and the dependency only goes one way.
func notFound(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound
}

// WarningAgentSeedFailedRetrying reports the retry that drops the agent source.
func WarningAgentSeedFailedRetrying(agent string) string {
	return fmt.Sprintf(
		"  warning: generating from agent %q failed in the service; "+
			"retrying from the instruction alone.\n", agent)
}

// GeneratingRubric reports a rubric generation job about to be submitted.
func GeneratingRubric(name string) string {
	return fmt.Sprintf("Generating rubric %s...\n", name)
}

// GeneratingDataset reports a dataset generation job about to be submitted.
func GeneratingDataset(name string, samples int) string {
	return fmt.Sprintf("Generating dataset %s (%d samples)...\n", name, samples)
}

// SubmittingRubricJob reports the service refusing the rubric job.
func SubmittingRubricJob(err error) error {
	return fmt.Errorf("submitting the rubric generation job: %w", err)
}

// SubmittingDataJob reports the service refusing the data generation job.
func SubmittingDataJob(err error) error {
	return fmt.Errorf("submitting the data generation job: %w", err)
}

// RubricGeneration reports a rubric job that did not finish successfully.
func RubricGeneration(err error) error {
	return fmt.Errorf("rubric generation: %w", err)
}

// DataGeneration reports a data job that did not finish successfully.
func DataGeneration(err error) error {
	return fmt.Errorf("data generation: %w", err)
}

// RubricJobReturnedNoResult reports a completed rubric job with nothing to write.
func RubricJobReturnedNoResult() error {
	return errors.New("the rubric generation job returned no result")
}

// DataJobReturnedNoDataset reports a completed data job with nothing to fetch.
func DataJobReturnedNoDataset() error {
	return errors.New("the data generation job returned no dataset reference")
}

// ReadingGeneratedDataset reports the generated dataset not being there to read.
func ReadingGeneratedDataset(name string, err error) error {
	return fmt.Errorf("reading the generated dataset %q: %w", name, err)
}

// DownloadingGeneratedDataset reports a failure to fetch the generated rows.
func DownloadingGeneratedDataset(name string, err error) error {
	return fmt.Errorf("downloading the generated dataset %q: %w", name, err)
}

// AgentSeededGenerationFailing explains the service-side failure that hits
// every agent, so the caller does not retry against a deterministic failure.
func AgentSeededGenerationFailing(err error, agent string) error {
	return fmt.Errorf(
		"%w\n\n"+
			"This job seeded generation from agent %q. Agent-seeded data generation is "+
			"currently failing in the service for every agent, so retrying will not help.\n"+
			"Workarounds: supply your own dataset with --dataset, or run without --target "+
			"to generate from the instruction alone.",
		err, agent)
}

// FromPromptNeedsInstruction reports --from prompt with nothing to prompt with.
func FromPromptNeedsInstruction() string {
	return "--from prompt needs --agent-instruction or --agent-instruction-file"
}

// FromAgentNeedsTarget reports --from agent with no agent to read.
func FromAgentNeedsTarget() string {
	return "--from agent needs a target agent; pass --target, " +
		"or declare one under target: in azure.eval.yaml"
}

// FromFileNotASource reports --from file, which generation has no path for.
func FromFileNotASource() string {
	return "--from file is not a generation source; " +
		"register the file with `azd ai eval dataset create` instead"
}

// FromNotBuildable reports a --from this plan cannot satisfy.
func FromNotBuildable(kind string) string {
	return fmt.Sprintf("--from %s cannot be built from this plan", kind)
}

// UnbuildableSources reports every --from the plan could not honour at once.
func UnbuildableSources(reasons []string) error {
	return errors.New(strings.Join(reasons, "; "))
}

// JobSubmitted reports the id of a job started with --no-wait.
func JobSubmitted(jobID string) string {
	return fmt.Sprintf("  submitted job %s\n", jobID)
}

// ReattachToJob says how to come back to a job started with --no-wait.
//
// The selector is part of the line because `job` requires it: the two
// collections share an id shape, so an id alone does not say which to call.
func ReattachToJob(selector, jobID string) string {
	return fmt.Sprintf("\nReattach with: azd ai eval job show %s --%s\n", jobID, selector)
}

// WroteArtifact reports where a generated artifact landed.
func WroteArtifact(path string) string {
	return fmt.Sprintf("%s Downloaded %s\n", doneMark, filepath.ToSlash(path))
}

// ArtifactExists reports a generation that would overwrite a checked-in file.
func ArtifactExists(path string) error {
	return fmt.Errorf(
		"%s already exists; pass --force to overwrite it, or --output-dir to write elsewhere",
		path)
}

// JobKindRequired reports a job command that does not say which collection.
func JobKindRequired() error {
	return errors.New("pass --dataset or --evaluator to say which generation jobs to act on")
}

// ListingJobs reports a failure to list one kind of generation job.
func ListingJobs(kind string, err error) error {
	return fmt.Errorf("listing %s generation jobs: %w", kind, err)
}

// NoJobs reports a project with no generation jobs of that kind.
func NoJobs(kind string) string {
	return fmt.Sprintf("No %s generation jobs found.\n", kind)
}

// JobLine renders one generation job in a listing or a detail view.
func JobLine(jobID, status string) string {
	return fmt.Sprintf("%s  %s\n", jobID, status)
}

// JobErrorLine reports why a generation job failed.
func JobErrorLine(message string) string {
	return fmt.Sprintf("error: %s\n", message)
}

// JobCancelled confirms a cancelled generation job.
func JobCancelled(kind, jobID, status string) string {
	return fmt.Sprintf("Cancelled %s generation job %s (%s)\n", kind, jobID, status)
}

// JobDeleted confirms a deleted generation job record.
func JobDeleted(kind, jobID string) string {
	return fmt.Sprintf("Deleted %s generation job %s\n", kind, jobID)
}

// JobNotFound reports a job id that is not in this group, naming the other one.
//
// Phrased to avoid an article before the kind: "a evaluator" is what the
// obvious wording produces.
func JobNotFound(kind, jobID, other string) error {
	return fmt.Errorf(
		"no %s generation job %q in this project; try the %s job group",
		kind, jobID, other)
}

// JobActionFailed reports a job operation that was not a read, so the sentence
// names what was attempted. A delete that reports "reading" sends the reader
// looking for a read that never happened.
func JobActionFailed(action, kind, jobID string, err error) error {
	return fmt.Errorf("%s %s generation job %s: %w", action, kind, jobID, err)
}

// JobFailedWithReason reports a polled job that failed and said why.
func JobFailedWithReason(status, message string) string {
	return fmt.Sprintf("job failed with status %q: %s", status, message)
}

// JobFailed reports a polled job that failed without saying why.
func JobFailed(status string) string {
	return fmt.Sprintf("job failed with status %q", status)
}

// PollerTimedOut reports a job that was still running when polling gave up.
func PollerTimedOut(operationID string, attempts int) string {
	return fmt.Sprintf("operation %s did not complete within %d attempts",
		operationID, attempts)
}

// OperationIDEmpty reports a poll with nothing to poll for.
func OperationIDEmpty() error {
	return errors.New("operation ID is empty")
}

// ---------------------------------------------------------------------------
// Datasets
// ---------------------------------------------------------------------------

// ReadingDataset reports a dataset that could not be read, by name or by path.
func ReadingDataset(dataset string, err error) error {
	return fmt.Errorf("reading dataset %q: %w", dataset, err)
}

// DatasetHasNoVersionsToRead reports a registered dataset with nothing published.
func DatasetHasNoVersionsToRead(dataset string) error {
	return fmt.Errorf("dataset %q has no versions to read", dataset)
}

// ReadingDatasetVersion reports one version of a dataset failing to read.
func ReadingDatasetVersion(dataset, version string, err error) error {
	return fmt.Errorf("reading dataset %q version %s: %w", dataset, version, err)
}

// CheckingDataset reports the read that decides whether a name is already
// taken. It is worth its own message because that read is what separates
// `create` from `update`, and a failure answered as "not there" turns a create
// into a silent update.
func CheckingDataset(dataset string, err error) error {
	return fmt.Errorf(
		"checking whether dataset %q already exists: %w", dataset, err)
}

// DatasetVersionEmpty reports a published version that holds no rows.
func DatasetVersionEmpty(dataset, version string) error {
	return fmt.Errorf("dataset %q version %s has no rows", dataset, version)
}

// JSONLLineInvalid reports a row that is not JSON, by line.
func JSONLLineInvalid(line int, err error) error {
	return fmt.Errorf("line %d is not valid JSON: %w", line, err)
}

// JSONLRowInvalid reports a row that is not JSON before the file is published.
func JSONLRowInvalid(path string, line int, err error) error {
	return fmt.Errorf(
		"%s line %d is not valid JSON: %w. Every line must be one JSON object",
		path, line, err)
}

// JSONLRowEmpty reports a row that parses to nothing to evaluate.
func JSONLRowEmpty(path string, line int) error {
	return fmt.Errorf("%s line %d is an empty object, which evaluates to nothing", path, line)
}

// JSONLNoRows reports a dataset file with nothing in it to evaluate.
func JSONLNoRows(path string) error {
	return fmt.Errorf("%s has no rows to evaluate", filepath.ToSlash(path))
}

// ReadingFromFile reports a --from-file that would not stat.
//
// A path that is simply absent is reported as absent: the wrapped error is a
// syscall name that says nothing to the person who mistyped it.
func ReadingFromFile(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("--from-file %q does not exist", filepath.ToSlash(path))
	}
	return fmt.Errorf("reading --from-file %q: %w", filepath.ToSlash(path), err)
}

// FromFileMustBeJSONL reports a --from-file that is not a dataset.
func FromFileMustBeJSONL(path string) error {
	return fmt.Errorf(
		"--from-file must be a .jsonl file or a directory containing one, got %q",
		filepath.ToSlash(path))
}

// FromFileDirectoryHasNoJSONL reports a directory with nothing to upload.
func FromFileDirectoryHasNoJSONL(dir string) error {
	return fmt.Errorf("no .jsonl file in %q; --from-file needs one to upload", filepath.ToSlash(dir))
}

// FromFileDirectoryIsAmbiguous refuses to guess which dataset was meant.
func FromFileDirectoryIsAmbiguous(dir string, names []string) error {
	return fmt.Errorf(
		"%q holds %d .jsonl files (%s); name the one to upload with --from-file",
		filepath.ToSlash(dir), len(names), strings.Join(names, ", "))
}

// InvalidDatasetName reports a name the service will not accept.
func InvalidDatasetName(name string) error {
	return invalidAssetName("dataset", name)
}

// InvalidEvaluatorName reports a name the service will not accept.
func InvalidEvaluatorName(name string) error {
	return invalidAssetName("evaluator", name)
}

func invalidAssetName(kind, name string) error {
	return fmt.Errorf(
		"%s name %q is invalid: use letters, digits, dashes and underscores, "+
			"up to 255 characters", kind, name)
}

// RegisteringDataset reports the service refusing to publish the dataset.
func RegisteringDataset(dataset string, err error) error {
	return fmt.Errorf("registering dataset %q: %w", dataset, err)
}

// DatasetRegistered confirms a published dataset version.
func DatasetRegistered(dataset, version string) string {
	return fmt.Sprintf("Registered dataset %s version %s\n", dataset, version)
}

// ListingDatasets reports a failure to list the project's datasets.
func ListingDatasets(err error) error {
	return fmt.Errorf("listing datasets: %w", err)
}

// ListingDatasetVersions reports a failure to list one dataset's versions.
func ListingDatasetVersions(dataset string, err error) error {
	return fmt.Errorf("listing versions of dataset %q: %w", dataset, err)
}

// NoDatasets reports a project with no datasets to list.
func NoDatasets() string {
	return "No datasets found.\n"
}

// NoDatasetVersions reports a name whose versions listed nothing.
//
// Listing a name that does not exist is not an error — a delete is checked for
// idempotence this way — so this has to read as an answer about that name
// rather than as a report about the project, which holds other datasets.
//
// The suggested command carries no placeholder, so it pastes and runs; the file
// is the one thing only the caller knows, and is named outside the command.
func NoDatasetVersions(dataset string) string {
	return fmt.Sprintf("No versions of dataset %q. Publish one with "+
		"`azd ai eval dataset create %s` and a --from-file path.\n", dataset, shellArg(dataset))
}

// ResolvingLatestDatasetVersion reports a failure to find what "latest" means.
func ResolvingLatestDatasetVersion(dataset string, err error) error {
	return fmt.Errorf("resolving the latest version of %q: %w", dataset, err)
}

// DatasetNotFound reports a name the project does not hold.
//
// The service answers an unknown name with an empty version list rather than a
// 404, and a dataset cannot exist with no versions, so an empty list means the
// dataset is absent rather than empty.
func DatasetNotFound(dataset string) error {
	return fmt.Errorf(
		"no dataset %q in this project; "+
			"`azd ai eval dataset list` shows the ones there are", dataset)
}

// DatasetVersionNotFoundWithHint reports a dataset version the project does not hold.
func DatasetVersionNotFoundWithHint(dataset, version string) error {
	return fmt.Errorf(
		"no dataset %q at version %q in this project; "+
			"`azd ai eval dataset versions list %s` shows the ones there are",
		dataset, version, shellArg(dataset))
}

// DatasetVersionNotFound reports a dataset version there is nothing to delete at.
func DatasetVersionNotFound(dataset, version string) error {
	return fmt.Errorf("no dataset %q at version %q in this project", dataset, version)
}

// DeletingDatasetVersion reports the service refusing the delete.
func DeletingDatasetVersion(dataset, version string, err error) error {
	return fmt.Errorf("deleting dataset %q version %q: %w", dataset, version, err)
}

// DatasetDeleted confirms a deleted dataset version.
func DatasetDeleted(dataset, version string) string {
	return fmt.Sprintf("Deleted dataset %s version %s\n", dataset, version)
}

// DatasetProblem attributes a failure to the dataset it happened under.
func DatasetProblem(dataset string, err error) error {
	return fmt.Errorf("dataset %q: %w", dataset, err)
}

// DatasetSource reports a declared source that is not on disk.
func DatasetSource(path string, err error) error {
	return fmt.Errorf("dataset source %q: %w", filepath.ToSlash(path), err)
}

// DatasetNotGeneratedYet reports a declared dataset whose rows are not written
// yet.
//
// `init` declares the dataset it plans and names the command that produces it,
// so reaching a deploy without one is an ordering mistake rather than a broken
// configuration. Said plainly, because the bare stat failure underneath is a
// Windows syscall name and a path with doubled separators.
//
// Both callers wrap this with DatasetProblem, which names the dataset, so this
// does not name it again.
func DatasetNotGeneratedYet(dataset, path string) error {
	return fmt.Errorf(
		"its rows %s have not been generated yet. "+
			"Run `azd ai eval generate --dataset --dataset-name %s` to write them, "+
			"or point the declaration at a .jsonl you already have. "+
			"If this entry came from a `$ref`, note that a relative `file:` inside "+
			"the referenced file resolves against azure.eval.yaml rather than against "+
			"that file -- write the path relative to the configuration instead",
		filepath.ToSlash(path), shellArg(dataset))
}

// DatasetNotLocalNorFound reports a source-less dataset the project rejected.
func DatasetNotLocalNorFound(dataset string, err error) error {
	return fmt.Errorf(
		"dataset %q has no local source and could not be found on the project: %w",
		dataset, err)
}

// DatasetNotLocalNorRegistered reports a source-less dataset nobody published.
func DatasetNotLocalNorRegistered(dataset string) error {
	return fmt.Errorf(
		"dataset %q has no local source and is not registered on the project", dataset)
}

// DatasetVersionConflict reports a pinned version the local file disagrees with.
func DatasetVersionConflict(dataset, version string) error {
	return fmt.Errorf(
		"dataset %q version %s already exists and the local file differs from it. "+
			"Raise `version:` to publish the change, or drop it to let each "+
			"deploy take the next version",
		dataset, version)
}

// DatasetDrifted reports a version published outside this configuration since
// the last deploy.
//
// `azd ai eval dataset update` publishes without recording the per-dataset
// version the reconciler reads, so it is a likely cause and naming it saves the
// reader looking for a colleague who did nothing. "Pull the newer content
// locally" was the other half of the old advice and is a no-op when the bytes
// already match, which is the common case.
func DatasetDrifted(dataset, latest, recorded string) error {
	return fmt.Errorf(
		"dataset %q is at version %s on the project but %s was recorded at the last deploy; "+
			"something published outside this configuration, which `azd ai eval dataset update` "+
			"on the same dataset also does. Pin it with `version: %s` on the dataset to deploy "+
			"what is already there, or publish a new version from the configuration's source, "+
			"then deploy again",
		dataset, latest, recorded, latest)
}

// ReadingDatasetDirectory reports the upload scan failing to read the directory.
func ReadingDatasetDirectory(err error) error {
	return fmt.Errorf("reading directory: %w", err)
}

// DatasetFileHasNoRows reports an empty dataset file, refused before upload.
func DatasetFileHasNoRows(name string) error {
	return fmt.Errorf(
		"dataset file %q has no rows, so there would be nothing to evaluate", name)
}

// NoJSONLInDirectory reports an upload directory holding no dataset.
func NoJSONLInDirectory(dir string) error {
	return fmt.Errorf("no .jsonl file found in %s", filepath.ToSlash(dir))
}

// ReadingDatasetFromDir reports the upload failing to gather the local rows.
func ReadingDatasetFromDir(dir string, err error) error {
	return fmt.Errorf("reading dataset from %s: %w", dir, err)
}

// StartingPendingUpload reports the service refusing to open an upload.
func StartingPendingUpload(err error) error {
	return fmt.Errorf("starting pending upload: %w", err)
}

// NoUploadURI reports an accepted upload the service gave nowhere to write to.
func NoUploadURI() error {
	return errors.New("no upload SAS URI returned from startPendingUpload")
}

// NoBlobURI reports an accepted upload the service gave no way to finalize.
//
// Separate from NoUploadURI because they are different fields of the same
// response: the SAS says where to write, the blob URI says what to register,
// and a response can carry one without the other.
func NoBlobURI() error {
	return errors.New("no blob URI returned from startPendingUpload, so there is nothing to register the upload as")
}

// UploadingBlob reports the dataset content failing to upload.
func UploadingBlob(err error) error {
	return fmt.Errorf("uploading blob: %w", err)
}

// ReadingDownloadCredentials reports the service refusing to hand out a read URI.
func ReadingDownloadCredentials(dataset string, err error) error {
	return fmt.Errorf("reading download credentials for %q: %w", dataset, err)
}

// NoDownloadURI reports a dataset the service gave nowhere to read from.
func NoDownloadURI(dataset string) error {
	return fmt.Errorf("no download URI returned for dataset %q", dataset)
}

// ListingDatasetContent reports a failure to list what a dataset version holds.
func ListingDatasetContent(dataset string, err error) error {
	return fmt.Errorf("listing the content of dataset %q: %w", dataset, err)
}

// DatasetHasNoFile reports a dataset version with nothing to download.
func DatasetHasNoFile(dataset string) error {
	return fmt.Errorf("dataset %q holds no downloadable file", dataset)
}

// ---------------------------------------------------------------------------
// Evaluators
// ---------------------------------------------------------------------------

// EvaluatorNeedsFields reports required inputs the dataset does not carry.
func EvaluatorNeedsFields(evaluator string, missing []string) error {
	return fmt.Errorf(
		"evaluator %q requires %s, which the dataset does not provide; "+
			"add %s to the dataset, or bind it with `data_mapping`",
		evaluator, quoteList(missing), pluralColumns(missing))
}

// EvaluatorLevelUnsupported reports an evaluation level the evaluator refuses.
func EvaluatorLevelUnsupported(evaluator, level string, supported []string) error {
	return fmt.Errorf(
		"evaluator %q does not support evaluation level %q; it supports %s",
		evaluator, level, quoteList(supported))
}

// EvaluatorNeedsInitParams reports required initialization parameters left unset.
func EvaluatorNeedsInitParams(evaluator string, missing []string) error {
	return fmt.Errorf(
		"evaluator %q requires %s; set it under the evaluator's "+
			"`initialization_parameters` in the eval config",
		evaluator, quoteList(missing))
}

// ReadingEvaluator reports an evaluator that could not be read, by name or path.
func ReadingEvaluator(evaluator string, err error) error {
	return fmt.Errorf("reading evaluator %q: %w", evaluator, err)
}

// EvaluatorProblem attributes a failure to the evaluator it happened under.
func EvaluatorProblem(evaluator string, err error) error {
	return fmt.Errorf("evaluator %q: %w", evaluator, err)
}

// EvaluatorSource reports a declared source that is not on disk.
func EvaluatorSource(path string, err error) error {
	return fmt.Errorf("evaluator source %q: %w", filepath.ToSlash(path), err)
}

// EvaluatorNotGeneratedYet reports a declared evaluator whose definition has
// not been written yet.
//
// `init` declares the rubric it plans and names the command that produces it,
// so reaching a deploy without one is an ordering mistake rather than a broken
// configuration. Said plainly, because the bare stat failure underneath is a
// Windows syscall name and a path with doubled separators.
//
// Both callers wrap this with EvaluatorProblem, which names the evaluator, so
// this does not name it again.
func EvaluatorNotGeneratedYet(evaluator, path string) error {
	return fmt.Errorf(
		"its definition %s has not been generated yet. "+
			"Run `azd ai eval generate --evaluator --evaluator-name %s` to write it, "+
			"or drop the evaluator from azure.eval.yaml. "+
			"If this entry came from a `$ref`, note that a relative `source:` inside "+
			"the referenced file resolves against azure.eval.yaml rather than against "+
			"that file -- carry the rubric under `definition:` instead",
		filepath.ToSlash(path), shellArg(evaluator))
}

// RubricBelongsUnderDefinition reports a rubric written at evaluator entry
// level, where a `$ref` to a bare rubric file puts it.
func RubricBelongsUnderDefinition(evaluator string) error {
	which := "an evaluator"
	if evaluator != "" {
		which = fmt.Sprintf("evaluator %q", evaluator)
	}
	return fmt.Errorf(
		"%s carries rubric keys such as `dimensions:` beside `name:`. "+
			"A rubric belongs under `definition:` -- write it there, or point at "+
			"its file with `definition:` and a nested `$ref:`, which fills that "+
			"field with the file's contents",
		which)
}

// CheckingEvaluatorExists reports a failure to tell create from update.
func CheckingEvaluatorExists(evaluator string, err error) error {
	return fmt.Errorf("checking whether evaluator %q exists: %w", evaluator, err)
}

// RegisteringEvaluator reports the service refusing to publish the evaluator.
func RegisteringEvaluator(evaluator string, err error) error {
	return fmt.Errorf("registering evaluator %q: %w", evaluator, err)
}

// EvaluatorRegistered confirms a published evaluator version.
func EvaluatorRegistered(evaluator, version string) string {
	return fmt.Sprintf("Registered evaluator %s version %s\n", evaluator, version)
}

// AssetAlreadyExists reports `create` asked of a name already in use.
func AssetAlreadyExists(kind, name string) error {
	return fmt.Errorf("%s %q already exists: use `update` to publish a new version", kind, name)
}

// AssetDoesNotExist reports `update` asked of a name nobody registered.
func AssetDoesNotExist(kind, name string) error {
	return fmt.Errorf("%s %q does not exist: use `create` to register it", kind, name)
}

// DefinitionNotJSONObject reports an evaluator definition that is not an object.
func DefinitionNotJSONObject(err error) error {
	return fmt.Errorf("the definition is not a JSON object: %w", err)
}

// NotValidJSON reports an evaluator file that will not parse at all.
func NotValidJSON(err error) error {
	return fmt.Errorf("not valid JSON: %w", err)
}

// RubricMissingDimensions reports a file that is neither rubric nor document.
func RubricMissingDimensions() error {
	return errors.New(
		"expected a rubric definition with 'dimensions', or a document with 'definition'")
}

// ListingEvaluators reports a failure to list the project's evaluators.
func ListingEvaluators(err error) error {
	return fmt.Errorf("listing evaluators: %w", err)
}

// ListingEvaluatorVersions reports a failure to list one evaluator's versions.
func ListingEvaluatorVersions(evaluator string, err error) error {
	return fmt.Errorf("listing versions of evaluator %q: %w", evaluator, err)
}

// NoEvaluators reports a project with no evaluators to list.
func NoEvaluators() string {
	return "No evaluators found.\n"
}

// EvaluatorNotFound reports an evaluator the project does not hold.
func EvaluatorNotFound(evaluator string) error {
	return fmt.Errorf(
		"no evaluator %q in this project; "+
			"`azd ai eval evaluator list` shows the ones there are", evaluator)
}

// EvaluatorVersionNotFound reports an evaluator version there is nothing to delete at.
func EvaluatorVersionNotFound(evaluator, version string) error {
	return fmt.Errorf("no evaluator %q at version %q in this project", evaluator, version)
}

// DeletingEvaluatorVersion reports the service refusing the delete.
func DeletingEvaluatorVersion(evaluator, version string, err error) error {
	return fmt.Errorf("deleting evaluator %q version %q: %w", evaluator, version, err)
}

// EvaluatorDeleted confirms a deleted evaluator version.
func EvaluatorDeleted(evaluator, version string) string {
	return fmt.Sprintf("Deleted evaluator %s version %s\n", evaluator, version)
}

// EvaluatorNotLocalNorFound reports a source-less evaluator the project rejected.
func EvaluatorNotLocalNorFound(evaluator string, err error) error {
	return fmt.Errorf(
		"evaluator %q has no local source and could not be found on the project: %w",
		evaluator, err)
}

// EvaluatorDrifted reports a version published outside this configuration since
// the last deploy.
//
// `azd ai eval evaluator update` publishes without recording the version the
// reconciler reads, so it is a likely cause and naming it saves the reader
// looking for a colleague who did nothing.
func EvaluatorDrifted(evaluator, remote, recorded string) error {
	return fmt.Errorf(
		"evaluator %q is at version %s on the project but %s was recorded at the last "+
			"deploy, and the local definition does not match it: something published a "+
			"version outside this configuration, which `azd ai eval evaluator update` on "+
			"the same evaluator also does. Publishing over it would leave that change "+
			"behind, so read it with `azd ai eval evaluator show %s --version %s "+
			"--output-file <path>` and bring it into the declared source before "+
			"deploying again, or delete that version if it was a mistake",
		evaluator, remote, recorded, evaluator, remote)
}

// EvaluatorVersionNotAdvancing reports a publish the service kept answering with
// a version that already existed.
func EvaluatorVersionNotAdvancing(evaluator, version string, waited fmt.Stringer) error {
	return fmt.Errorf(
		"publishing evaluator %q kept returning version %s, which already "+
			"existed. The service was still assigning that version after %s, so "+
			"version %s now holds what was just published and any eval bound to "+
			"it is scoring against it",
		evaluator, version, waited, version)
}

// EvaluatorHasNoVersions reports an evaluator nothing was ever published under.
func EvaluatorHasNoVersions(evaluator string) error {
	return fmt.Errorf("evaluator %q has no versions", evaluator)
}

// EvaluatorHasNoUsableVersion reports versions none of which can be resolved.
func EvaluatorHasNoUsableVersion(evaluator string) error {
	return fmt.Errorf("evaluator %q has no usable version", evaluator)
}

// BareEvaluatorEntry reports an evaluators: entry written as a plain string.
func BareEvaluatorEntry(name string) error {
	return fmt.Errorf(
		"an evaluator entry is a mapping, not a bare string: "+
			"write `- evaluator: %s`", name)
}

// EvaluatorsMustBeSequence reports an evaluators: block that is not a list.
func EvaluatorsMustBeSequence(kind string) error {
	return fmt.Errorf("evaluators must be a list, got %s", kind)
}

// EvaluatorsMustBeList reports an evaluators: block that is not a JSON array.
func EvaluatorsMustBeList(err error) error {
	return fmt.Errorf("evaluators must be a list: %w", err)
}

// DecodingEvaluatorName reports an evaluator entry whose name will not decode.
func DecodingEvaluatorName(err error) error {
	return fmt.Errorf("decoding evaluator name: %w", err)
}

// DecodingEvaluator reports an evaluator entry that will not decode.
func DecodingEvaluator(err error) error {
	return fmt.Errorf("decoding evaluator: %w", err)
}

// EvaluatorEntryMissingEvaluator reports an entry that names no evaluator.
func EvaluatorEntryMissingEvaluator() error {
	return errors.New("evaluator entry is missing 'evaluator'")
}

// EvaluatorEntryMustBeMapping reports an entry that is neither map nor string.
func EvaluatorEntryMustBeMapping(kind string) error {
	return fmt.Errorf("evaluator entry must be a mapping, got %s", kind)
}

// EvaluatorAliasIsCircular reports an anchor that contains its own alias.
//
// Expanding it has no end, so it is named rather than followed.
func EvaluatorAliasIsCircular(anchor string) error {
	return fmt.Errorf("anchor %q refers to itself, so it cannot be expanded", anchor)
}

// ---------------------------------------------------------------------------
// Deploy and reconcile
// ---------------------------------------------------------------------------

// EvalConfigInvalid reports a configuration a deploy will not act on.
func EvalConfigInvalid(err error) error {
	return fmt.Errorf("eval config is invalid: %w", err)
}

// ServiceCarriesNoConfig reports an azure.yaml entry with nothing to deploy.
func ServiceCarriesNoConfig(service string) error {
	return fmt.Errorf(
		"service %q carries no eval configuration; expected evaluators, datasets, or evals",
		service)
}

// ResolvingServiceRefs reports a $ref that could not be followed.
func ResolvingServiceRefs(err error) error {
	return fmt.Errorf("resolving $ref in the eval service configuration: %w", err)
}

// RefNeedsAProjectRoot reports an include reached without a directory to
// resolve it against.
func RefNeedsAProjectRoot(service string) error {
	return fmt.Errorf(
		"service %q uses `$ref`, but the project directory could not be determined, "+
			"so the referenced file cannot be found. Run from inside the azd project, "+
			"or inline the configuration this service points at",
		service)
}

// ProjectRootUnavailable reports a deploy that could not learn where the
// project is, which every relative path in the configuration is measured from.
func ProjectRootUnavailable(service string) error {
	return fmt.Errorf(
		"deploying service %q: azd could not report the project directory, and every "+
			"relative path in the evaluation configuration is resolved against it. "+
			"Continuing would measure them from wherever this command was started, "+
			"which can publish a same-named dataset from the wrong directory. Re-run "+
			"from inside the azd project",
		service)
}

// CatalogNameBehindAnInclude reports a name declared through a `$ref`, which
// this command cannot edit in place.
//
// One message for two shapes, so the cause is stated as what they share: the
// entry lives in the referenced file. Naming only the duplicate-on-resolve case
// would be wrong for an overlay `name`, which collides with nothing and instead
// ends up declaring the rubric twice.
func CatalogNameBehindAnInclude(kind, name string) error {
	return fmt.Errorf(
		"%s %q is declared in a file pulled in with `$ref`, so this command cannot "+
			"update it here: an entry written beside the directive takes effect only "+
			"once the include is resolved, and not as it reads. Edit the referenced "+
			"file, or generate under a different name",
		kind, name)
}

// EvaluatorRubricWrittenInPlace reports an evaluator whose rubric is already
// written out under `definition:`, so there is nowhere to record a generated
// file without declaring the rubric twice.
func EvaluatorRubricWrittenInPlace(name string) error {
	return fmt.Errorf(
		"evaluator %q already carries its rubric under `definition:`, so this "+
			"command cannot record a generated file against it: an entry holding "+
			"both a `definition:` and a `source:` is refused on the next read. Edit "+
			"the rubric in place, or generate under a different name",
		name)
}

// EvaluatorPinnedToAVersion reports an evaluator pinned to a registered
// version, which leaves nowhere to record a generated file.
func EvaluatorPinnedToAVersion(name string) error {
	return fmt.Errorf(
		"evaluator %q is pinned to a registered `version:`, so this command cannot "+
			"record a generated file against it: an entry holding both a `version:` "+
			"and a `source:` is refused on the next read. Remove the pin to publish "+
			"from a file, or generate under a different name",
		name)
}

// ReadingServiceConfig reports the service entry failing to serialize.
func ReadingServiceConfig(err error) error {
	return fmt.Errorf("reading the eval service configuration: %w", err)
}

// ReconcilingDataset reports the dataset a deploy has reached.
func ReconcilingDataset(dataset string) string {
	return fmt.Sprintf("Reconciling dataset %s", dataset)
}

// ReconcilingEvaluator reports the evaluator a deploy has reached.
func ReconcilingEvaluator(evaluator string) string {
	return fmt.Sprintf("Reconciling evaluator %s", evaluator)
}

// ReconcilingEval reports the eval a deploy has reached.
func ReconcilingEval(eval string) string {
	return fmt.Sprintf("Reconciling eval %s", eval)
}

// PublishedVersion reports an artifact a deploy published.
func PublishedVersion(kind, name, version string) string {
	return fmt.Sprintf("Published %s %s version %s", kind, name, version)
}

// UnchangedAtVersion reports an artifact a deploy left alone.
func UnchangedAtVersion(kind, name, version string) string {
	return fmt.Sprintf("%s %s is unchanged at version %s",
		strings.ToUpper(kind[:1])+kind[1:], name, version)
}

// EvalProblem attributes a failure to the eval it happened under.
func EvalProblem(eval string, err error) error {
	return fmt.Errorf("eval %q: %w", eval, err)
}

// EvalCreatedProgress and EvalUnchangedProgress are the deploy-time equivalents
// of EvalCreated and EvalUnchanged, without the status marks azd adds itself.
func EvalCreatedProgress(eval, id string) string {
	return fmt.Sprintf("Created eval %s (%s)", eval, id)
}

func EvalUnchangedProgress(eval, id string) string {
	return fmt.Sprintf("Eval %s is unchanged (%s)", eval, id)
}

// EvalCreated confirms a single eval created outside a full deploy.
func EvalCreated(eval, id string) string {
	return fmt.Sprintf("%s Created eval: %s (%s)\n", doneMark, eval, id)
}

// EvalUnchanged reports an eval a create found already in place.
//
// An eval is immutable, so re-running create against an unedited declaration
// creates nothing. Saying "Created" there claims work that did not happen, and
// hides the one thing worth checking: that the id, and so the run history
// hanging off it, survived.
func EvalUnchanged(eval, id string) string {
	return fmt.Sprintf("%s Eval %s is unchanged (%s)\n", skippedMark, eval, id)
}

// ListingEvals reports a failure to list the project's evals.
func ListingEvals(err error) error {
	return fmt.Errorf("listing evals: %w", err)
}

// NoEvals reports a project with no evals to list.
func NoEvals() string {
	return "No evals found.\n"
}

// EvalNotFound reports an eval id the project does not hold.
func EvalNotFound(evalID string) error {
	return fmt.Errorf(
		"no eval %q in this project; "+
			"`azd ai eval list` shows the ones there are", evalID)
}

// AmbiguousEvalName reports a name carried by more than one eval.
//
// An eval is immutable, so editing a declaration creates another under the same
// name and leaves the previous one holding its run history. Deleting takes the
// runs with it, so which one is meant has to be said rather than guessed.
func AmbiguousEvalName(name string, ids []string) error {
	return fmt.Errorf(
		"%d evals are named %q, and deleting one discards its runs, so name the "+
			"id instead: %s", len(ids), name, strings.Join(ids, ", "))
}

// EvalGone reports an eval id there is nothing to delete at.
func EvalGone(evalID string) error {
	return fmt.Errorf("no eval %q in this project", evalID)
}

// ReadingEval reports a failure to read one eval.
func ReadingEval(evalID string, err error) error {
	return fmt.Errorf("reading eval %q: %w", evalID, err)
}

// DeletingEval reports the service refusing the delete.
func DeletingEval(evalID string, err error) error {
	return fmt.Errorf("deleting eval %q: %w", evalID, err)
}

// EvalDeleted confirms a deleted eval.
func EvalDeleted(evalID string) string {
	return fmt.Sprintf("Deleted eval %s\n", evalID)
}

// Hashing reports a local artifact that could not be fingerprinted.
func Hashing(path string, err error) error {
	return fmt.Errorf("hashing %q: %w", filepath.ToSlash(path), err)
}

// HashingEval reports an eval declaration that could not be fingerprinted.
func HashingEval(eval string, err error) error {
	return fmt.Errorf("hashing eval %q: %w", eval, err)
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// NoAzdProject reports a command that found no project to attach to.
func NoAzdProject() error {
	return errors.New(
		"no azd project found in this directory. Run `azd init` first, " +
			"or run this from the root of an existing one; the eval service is added to " +
			"its azure.yaml")
}

// ConnectingToAzd reports the azd daemon being unreachable.
func ConnectingToAzd(err error) error {
	return fmt.Errorf("connecting to azd: %w", err)
}

// CreatingCredential reports the Azure credential failing to build.
func CreatingCredential(err error) error {
	return fmt.Errorf("creating Azure credential: %w", err)
}

// ErrNoAzdEnvironment reports that there is no azd environment to persist into.
var ErrNoAzdEnvironment = errors.New("no active azd environment")

// NoAzdEnvironmentToWrite reports a value with nowhere to be remembered.
func NoAzdEnvironmentToWrite(key string) error {
	return fmt.Errorf("%w to write %s into", ErrNoAzdEnvironment, key)
}

// WritingEnvValue reports the azd environment refusing a write.
func WritingEnvValue(key string, err error) error {
	return fmt.Errorf("writing %s to the azd environment: %w", key, err)
}

// BuildingServiceEntry reports the eval service entry failing to build.
func BuildingServiceEntry(err error) error {
	return fmt.Errorf("building the eval service entry: %w", err)
}

// AddingServiceTo reports azd refusing to add the eval service.
func AddingServiceTo(rootConfig string, err error) error {
	return fmt.Errorf("adding the eval service to %s: %w", rootConfig, err)
}

// SourceNotADataSource reports an --source that names nothing rows come from.
func SourceNotADataSource(source, dataset, traces string) error {
	return fmt.Errorf("--source %q is not a data source; use %q or %q", source, dataset, traces)
}

// TracesTakesNoDataset reports --dataset paired with a trace-backed eval.
func TracesTakesNoDataset() error {
	return errors.New("--source traces reads production traces, so it takes no --dataset")
}

// MaxTracesNeedsTraceSource reports --max-traces without a trace-backed eval.
func MaxTracesNeedsTraceSource() error {
	return errors.New("--max-traces caps a trace-backed eval; pass --source traces")
}

// MaxTracesMustBePositive reports a negative --max-traces.
func MaxTracesMustBePositive() error {
	return errors.New("--max-traces must be positive")
}

// EvalAlreadyDeclared reports an init that would overwrite a hand-tuned eval.
func EvalAlreadyDeclared(eval, configPath string) error {
	return fmt.Errorf(
		"an eval named %q already exists in %s; choose another name with --name, "+
			"or pass --force to replace it. `init` only adds: editing an eval is a file edit",
		eval, configPath)
}

// CreatingDatasetsDir reports the datasets directory failing to be created.
func CreatingDatasetsDir(err error) error {
	return fmt.Errorf("creating the datasets directory: %w", err)
}

// CreatingEvaluatorsDir reports the evaluators directory failing to be created.
func CreatingEvaluatorsDir(err error) error {
	return fmt.Errorf("creating the evaluators directory: %w", err)
}

// DetectedTarget reports the agent the scaffolded eval will evaluate.
func DetectedTarget(target string) string {
	return fmt.Sprintf("%s Detected agent target: %s\n", doneMark, target)
}

// NoAgentToEvaluate reports a project declaring no agent service.
func NoAgentToEvaluate() error {
	return errors.New(
		"this project declares no agent service to evaluate. Add one, or name an " +
			"existing agent with --target")
}

// AmbiguousAgentTarget reports several agents where only one can be scaffolded.
func AmbiguousAgentTarget(agents []string) error {
	return fmt.Errorf(
		"this project declares more than one agent (%s), so --target says which to "+
			"evaluate", strings.Join(agents, ", "))
}

// SelectAgentPrompt asks which agent the eval is for.
func SelectAgentPrompt() string {
	return "Select the agent to evaluate:"
}

// SelectingAgent reports a failed agent prompt.
func SelectingAgent(err error) error {
	return fmt.Errorf("selecting an agent to evaluate: %w", err)
}

// JudgeModelRequired reports a scaffold that has no deployment to judge with.
//
// Reached when the project declares no model deployment to read one from, so
// the flag is the whole of the way out. Failing here is deliberate: the judging
// built-ins declare the deployment as required, so a config written without one
// is rejected by the service later, far from the command that wrote it.
func JudgeModelRequired() error {
	return errors.New(
		"a model deployment is required to judge with: pass --judge-model. " +
			"This project declares no deployments, and the azd environment sets no " +
			"AZURE_AI_MODEL_DEPLOYMENT_NAME")
}

// EvaluatorRefEmpty reports an --evaluator that carries no name, which is what
// a stray comma leaves behind.
func EvaluatorRefEmpty() error {
	return errors.New("--evaluator was given an empty reference: name an evaluator, " +
		"or use builtin.<name> for a built-in")
}

// EvaluatorRefMalformed reports a reference no evaluator can be found under.
func EvaluatorRefMalformed(ref string) error {
	return fmt.Errorf("%q is not an evaluator reference: repeat --evaluator, or separate "+
		"them with commas, and use builtin.<name> for a built-in", ref)
}

// EvaluatorRefNotAPath reports an --evaluator value carrying path separators.
//
// The value becomes ./evaluators/<ref>.json, so one with separators in it
// scaffolds a source outside the evaluators directory, and deploy reads and
// uploads whatever that resolves to. Refused rather than cleaned up: a reader
// who typed a path meant something other than this flag.
func EvaluatorRefNotAPath(ref string) error {
	return fmt.Errorf("%q looks like a path, not an evaluator name: pass a name such as "+
		"builtin.relevance or quality, and declare a rubric file with source: in the "+
		"configuration instead", ref)
}

// GateNeedsATerminalRun refuses to gate a run that is still moving.
//
// The counts are partial until the run stops, so a threshold read from them
// can fail a run that would have passed. Ignoring the flag instead would leave
// a pipeline believing it is gated when it is not.
func GateNeedsATerminalRun(runID, status string) error {
	return fmt.Errorf(
		"run %s is %s, so --fail-on has only partial results to judge: "+
			"add --wait to gate once it finishes",
		runID, status)
}

// InEvalAt says which declaration an error came from.
//
// The source rules are checked in one place and reported from two, because the
// same file is read when it is validated and again when a run is built. Only
// the first of those has an index to name.
func InEvalAt(i int, eval string, err error) error {
	return fmt.Errorf("evals[%d] (%s): %w", i, eval, err)
}

// InEval says which eval an error came from, where there is no index.
func InEval(eval string, err error) error {
	return fmt.Errorf("eval %q: %w", eval, err)
}

// TraceWindowNotATime reports a window bound that is not a timestamp.
func TraceWindowNotATime(field, value string) error {
	return fmt.Errorf(
		"source.%s is %q, which is not a time: use RFC 3339, "+
			"for example 2026-08-18T09:00:00Z",
		field, value)
}

// TraceWindowBoundUnusable reports a bound that parses but says nothing.
//
// Both this layer and the wire read a zero as "no bound", so a bound that
// resolves to one would be dropped from the request rather than applied.
func TraceWindowBoundUnusable(field, value string) error {
	return fmt.Errorf(
		"source.%s is %q, which is not a time any traces were recorded at: "+
			"give a time the agent was running",
		field, value)
}

// TraceWindowEndsBeforeItStarts reports a window that can hold no traces.
func TraceWindowEndsBeforeItStarts(start, end string) error {
	return fmt.Errorf(
		"source.end_time %q is not after source.start_time %q, "+
			"so the window holds no traces",
		end, start)
}

// TraceWindowOverSpecified reports a window declared twice over.
//
// lookback_hours measures back from where the window closes and start_time is
// an absolute bound, so a file carrying both does not say which was meant.
func TraceWindowOverSpecified() error {
	return errors.New(
		"source declares both start_time and lookback_hours, which are two ways " +
			"of saying where the window opens: keep one")
}

// NegativeLookbackHours reports a lookback that is not a length.
func NegativeLookbackHours(hours int) error {
	return fmt.Errorf(
		"source.lookback_hours is %d, and how far back to look cannot be "+
			"negative: give the hours to look back",
		hours)
}

// LookbackTooLarge reports a lookback beyond the span a window may cover.
func LookbackTooLarge(hours, limit int) error {
	return fmt.Errorf(
		"source.lookback_hours is %d, which is beyond the %d hours a window can "+
			"reach back: give a shorter lookback, or replace it with a start_time",
		hours, limit)
}

// MaxTracesUnusable reports a negative cap written into the file.
//
// The flag that writes it is already guarded; this catches the file being
// edited afterwards, where a negative value is sent as-is and the run comes
// back empty.
func MaxTracesUnusable(maxTraces int) error {
	return fmt.Errorf(
		"source.max_traces is %d: give a positive cap, or leave it out to use "+
			"the service's default",
		maxTraces)
}

// SourceFieldsNotRead reports fields the declared source type ignores.
func SourceFieldsNotRead(sourceType string, fields []string) error {
	if len(fields) == 1 {
		return fmt.Errorf(
			"source declares %s, which a %q source does not read: "+
				"remove it, or change the type to one that does",
			fields[0], sourceType)
	}
	return fmt.Errorf(
		"source declares %s, which a %q source does not read: "+
			"remove them, or change the type to one that does",
		strings.Join(fields, ", "), sourceType)
}

// MaxTurnsUnusable reports a turn cap a run could not apply.
func MaxTurnsUnusable(maxTurns int) error {
	return fmt.Errorf(
		"source.max_turns is %d: give a positive cap, or leave it out to use "+
			"the service's default",
		maxTurns)
}

// LookbackReachesTooFarBack reports a lookback that lands on an unusable start.
func LookbackReachesTooFarBack(hours int) error {
	return fmt.Errorf(
		"source.lookback_hours is %d, which opens the window before any trace "+
			"was recorded: give a shorter lookback",
		hours)
}

// AmbiguousJudgeModel reports several deployments where only one can be used.
func AmbiguousJudgeModel(models []string) error {
	return fmt.Errorf(
		"this project declares more than one model deployment (%s), so "+
			"--judge-model says which the graders judge with", strings.Join(models, ", "))
}

// SelectJudgeModelPrompt asks which deployment the graders judge with.
func SelectJudgeModelPrompt() string {
	return "Select the model deployment the graders judge with:"
}

// SelectEvalPrompt asks which of the declared evals a command means.
func SelectEvalPrompt() string {
	return "Select the eval to use:"
}

// SelectingJudgeModel reports a failed judge model prompt.
func SelectingJudgeModel(err error) error {
	return fmt.Errorf("selecting a judge model deployment: %w", err)
}

// UsingTraceSource reports a scaffold that reads production traces.
//
// Naming Application Insights is a claim about the project, so it is only made
// when a connection was actually found. `init` makes no service calls and
// cannot verify one it did not see.
func UsingTraceSource(connected bool) string {
	if connected {
		return fmt.Sprintf("%s Using data source: traces (Application Insights)\n", doneMark)
	}
	return fmt.Sprintf(
		"%s Using data source: traces. No Application Insights connection is recorded "+
			"in this environment, so the run finds rows only if the project has one\n",
		doneMark)
}

// JudgeModelDeployment reports the deployment the graders will judge with.
func JudgeModelDeployment(model string) string {
	return fmt.Sprintf("%s Judge model deployment: %s\n", doneMark, model)
}

// GradingWith reports the evaluators the scaffold settled on.
//
// Omitting --evaluator picks them, so without this the one thing `init` decided
// on the reader's behalf is the one thing it does not mention.
func GradingWith(evaluators []string) string {
	return fmt.Sprintf("%s Grading with: %s\n", doneMark, strings.Join(evaluators, ", "))
}

// createdHeading opens the list of what a scaffold wrote.
func createdHeading() string {
	return "\nCreated\n"
}

// ScaffoldHeading opens the list of what a scaffold wrote. `init` appends to an
// existing configuration rather than replacing it, and a reader who sees
// "Created" over a file they already had reasonably fears it was overwritten.
func ScaffoldHeading(existed bool) string {
	if existed {
		return "\nUpdated\n"
	}
	return createdHeading()
}

// createdConfigLine names the configuration a scaffold wrote.
func createdConfigLine(configPath string) string {
	return fmt.Sprintf("  %-33s evaluation configuration\n", configPath)
}

// ScaffoldConfigLine names the configuration a scaffold wrote or added to.
func ScaffoldConfigLine(configPath string, existed bool) string {
	if existed {
		return fmt.Sprintf("  %-33s evaluation configuration (eval added)\n", configPath)
	}
	return createdConfigLine(configPath)
}

// AddedServiceLine reports the eval service being added to the root config.
func AddedServiceLine(rootConfig, service string) string {
	return fmt.Sprintf("  %-33s added service '%s'\n", rootConfig, service)
}

// AlreadyDeclaresServiceLine reports a root config that already referenced the eval.
func AlreadyDeclaresServiceLine(rootConfig, service string) string {
	return fmt.Sprintf("  %-33s already declares service '%s'\n", rootConfig, service)
}

// FirstNextStep opens the list of commands to run after a scaffold.
func FirstNextStep(step string) string {
	return fmt.Sprintf("\nNext: %s\n", step)
}

// FurtherNextStep continues the list of commands to run after a scaffold.
func FurtherNextStep(step string) string {
	return fmt.Sprintf("      %s\n", step)
}

// CreatedCatalogFile reports a configuration created to hold a catalog entry.
func CreatedCatalogFile(configPath string) string {
	return fmt.Sprintf("%s Created %s with the catalog entry\n", doneMark, filepath.ToSlash(configPath))
}

// AddedToCatalog reports a generated artifact recorded in the configuration.
func AddedToCatalog(kind, artifact, configPath string) string {
	return fmt.Sprintf("%s Added %s %s to %s\n", doneMark, kind, artifact, filepath.ToSlash(configPath))
}

// ArtifactDescription names a catalogued artifact, with its version when there is one.
func ArtifactDescription(name, version string) string {
	if version == "" || version == "latest" {
		return fmt.Sprintf("'%s'", name)
	}
	return fmt.Sprintf("'%s' (version %s)", name, version)
}

// NoEvalsDeclared reports a configuration with nothing to act on.
//
// The same sentence wherever it is reached. `generate` writes the dataset and
// evaluator it made into the catalog but declares no eval, so a `create` or a
// run straight afterwards lands here, and both need to be told the same way
// out.
func NoEvalsDeclared() error {
	return errors.New(
		"no eval is declared; `azd ai eval init` declares one. " +
			"`generate` only adds the dataset and evaluator it made")
}

// SeveralEvalsDeclared reports an unnamed eval where guessing would be wrong.
// The two commands that hit this name their eval differently, so neither form
// can be recommended on its own: `create` takes it as an argument, the run
// commands take --eval.
func SeveralEvalsDeclared(count int, names []string) error {
	return fmt.Errorf(
		"this configuration declares %d evals (%s); name the one you mean, "+
			"as an argument to `create` or with --eval on the run commands",
		count, strings.Join(names, ", "))
}

// EvalNotDeclared reports a name the configuration does not carry.
func EvalNotDeclared(eval string, names []string) error {
	// "this configuration has" with nothing after it is a sentence that stops
	// mid-clause, which is what an empty list produces.
	if len(names) == 0 {
		return fmt.Errorf("eval %q is not declared, and this configuration declares none", eval)
	}
	return fmt.Errorf(
		"eval %q is not declared; this configuration has %s",
		eval, strings.Join(names, ", "))
}

// AtLeastOneEvalRequired reports it on the way to deploying.
//
// One sentence for one fact: the deploy door and the run door reach this from
// different directions and both need the same way out.
func AtLeastOneEvalRequired() error {
	return NoEvalsDeclared()
}

// EvalNameRequired reports an eval entry with no name.
func EvalNameRequired(index int) error {
	return fmt.Errorf("evals[%d]: 'name' is required", index)
}

// DuplicateEvalName reports two evals answering to the same name.
func DuplicateEvalName(index int, eval string) error {
	return fmt.Errorf("evals[%d]: duplicate eval name %q", index, eval)
}

// EvalsIdenticalApartFromName reports two evals nothing can tell apart once deployed.
func EvalsIdenticalApartFromName(index int, eval, first string) error {
	return fmt.Errorf(
		"evals[%d] (%s): identical to %q apart from its name and description; "+
			"give them different evaluators, datasets or settings, or declare one",
		index, eval, first)
}

// DatasetNameRequired reports a catalog entry with no name.
func DatasetNameRequired(index int) error {
	return fmt.Errorf("datasets[%d]: 'name' is required", index)
}

// DuplicateDatasetName reports two catalog entries answering to the same name.
func DuplicateDatasetName(index int, dataset string) error {
	return fmt.Errorf("datasets[%d]: duplicate dataset name %q", index, dataset)
}

// EvaluatorNameRequired reports a catalog entry with no name.
func EvaluatorNameRequired(index int) error {
	return fmt.Errorf("evaluators[%d]: 'name' is required", index)
}

// DuplicateEvaluatorName reports two catalog entries answering to the same name.
func DuplicateEvaluatorName(index int, evaluator string) error {
	return fmt.Errorf("evaluators[%d]: duplicate evaluator name %q", index, evaluator)
}

// BuiltinNeedsNoCatalogEntry reports a built-in declared as though it were custom.
func BuiltinNeedsNoCatalogEntry(index int, evaluator string) error {
	return fmt.Errorf(
		"evaluators[%d] (%s): a built-in needs no catalog entry; reference it "+
			"straight from an eval", index, evaluator)
}

// EvaluatorVersionWithSource reports a pin the service would assign anyway.
func EvaluatorVersionWithSource(index int, evaluator string) error {
	return fmt.Errorf(
		"evaluators[%d] (%s): `version` cannot be set with `source`, because the "+
			"service assigns the version when it publishes. Drop `version` to "+
			"publish this file, or drop `source` to reference a version already "+
			"on the project", index, evaluator)
}

// EvaluatorVersionWithDefinition reports the same pin against a rubric written
// out in the configuration rather than named as a file.
func EvaluatorVersionWithDefinition(index int, evaluator string) error {
	return fmt.Errorf(
		"evaluators[%d] (%s): `version` cannot be set with `definition`, because "+
			"the service assigns the version when it publishes. Drop `version` to "+
			"publish this rubric, or drop `definition` to reference a version "+
			"already on the project", index, evaluator)
}

// EvaluatorRubricDeclaredTwice reports a rubric both named and written out.
//
// Publishing uses the written one, so leaving this to the schema alone would
// mean the file quietly never got read.
func EvaluatorRubricDeclaredTwice(index int, evaluator string) error {
	return fmt.Errorf(
		"evaluators[%d] (%s): `source` and `definition` both give the rubric; "+
			"declare one. Keep `definition` to publish what is written here, or "+
			"keep `source` to publish the file it names", index, evaluator)
}

// DatasetAndSourceDeclareTheSameThing reports it where there is no index.
func DatasetAndSourceDeclareTheSameThing() error {
	return errors.New("`dataset` and `source` both say where rows come from; declare one")
}

// NoEvalToValidate reports a declaration that is not there at all.
func NoEvalToValidate() error {
	return errors.New("no eval declaration to check")
}

// DatasetNotInDatasetsCatalog reports an eval naming a dataset nobody declared.
func DatasetNotInDatasetsCatalog(index int, eval, dataset string) error {
	return InEvalAt(index, eval, DatasetNotDeclared(dataset))
}

// DatasetNotDeclared reports it where there is no index.
func DatasetNotDeclared(dataset string) error {
	return fmt.Errorf("dataset %q is not in the datasets catalog", dataset)
}

// SourceTypeMissing reports it where there is no index.
func SourceTypeMissing() error {
	return errors.New("source.type is required")
}

// SourceTypeNotSupported reports the same, where there is no index to name.
func SourceTypeNotSupported(got, traces, responses string) error {
	return fmt.Errorf("source.type %q is not supported; use %q or %q", got, traces, responses)
}

// TraceSourceNeedsAnAgent reports it where there is no index.
//
// A target names one too, unless it names a model: a deployment name matches
// no spans, so it is not an answer to whose conversations to read.
func TraceSourceNeedsAnAgent() error {
	return errors.New(
		"source.agent_name is required for a trace source, " +
			"or declare an agent target.name")
}

// ResponsesSourceNeedsResponseIDs reports it where there is no index.
func ResponsesSourceNeedsResponseIDs() error {
	return errors.New("source.response_ids is required for a responses source")
}

// AtLeastOneEvaluatorRequired reports an eval that scores nothing.
func AtLeastOneEvaluatorRequired(index int, eval string) error {
	return fmt.Errorf("evals[%d] (%s): at least one evaluator is required", index, eval)
}

// EvaluatorFieldRequired reports an evaluators: entry with no evaluator named.
func EvaluatorFieldRequired(evalIndex, refIndex int) error {
	return fmt.Errorf("evals[%d].evaluators[%d]: 'evaluator' is required", evalIndex, refIndex)
}

// DuplicateCriterion reports two result rows nothing could tell apart.
func DuplicateCriterion(evalIndex, refIndex int, criterion string) error {
	return fmt.Errorf(
		"evals[%d].evaluators[%d]: duplicate criterion %q; give one a `name`",
		evalIndex, refIndex, criterion)
}

// EvaluatorNotInCatalog reports a reference to an evaluator nobody declared.
func EvaluatorNotInCatalog(evalIndex, refIndex int, evaluator string) error {
	return fmt.Errorf(
		"evals[%d].evaluators[%d]: evaluator %q is not in the evaluators catalog",
		evalIndex, refIndex, evaluator)
}

// TargetTypeNotSupported reports it where there is no index.
func TargetTypeNotSupported(got, agent, model string) error {
	return fmt.Errorf("target.type %q is not supported; use %q or %q", got, agent, model)
}

// EvaluationLevelNotSupported reports it where there is no index.
func EvaluationLevelNotSupported(got, turn, conversation string) error {
	return fmt.Errorf("evaluation_level %q is invalid; expected %q or %q", got, turn, conversation)
}

// TraceSourceCannotReadAModelTarget reports a trace eval pointed at a deployment.
//
// Its own sentence, because the general advice is "declare an agent target",
// which here reads as an invitation to relabel the deployment -- producing a
// filter that matches no spans and a run that reports nothing.
func TraceSourceCannotReadAModelTarget(name string) error {
	return fmt.Errorf(
		"source.agent_name is required for a trace source: target %q is a model "+
			"deployment, and traces are recorded against an agent, not a deployment",
		name)
}

// TargetNameMissing reports it where there is no index.
func TargetNameMissing() error {
	return errors.New(
		"target.name is required; remove the target: to score the dataset as it stands")
}

// AmbiguousEvalConfig reports a directory holding both configuration names.
func AmbiguousEvalConfig(current, legacy string) error {
	return fmt.Errorf(
		"%s and %s are both present, and azure.yaml can reference only one of them. "+
			"Keep %s and delete the other, or point the service's $ref at the one you want",
		filepath.ToSlash(current), filepath.ToSlash(legacy), filepath.ToSlash(current))
}

// ReadingEvalConfig reports a configuration file that would not read.
func ReadingEvalConfig(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return noEvalConfig(path)
	}
	return fmt.Errorf("reading eval config %q: %w", filepath.ToSlash(path), err)
}

// noEvalConfig reports a command run before anything scaffolded a config.
//
// The bare read failure underneath is a Windows syscall phrase about a path,
// which describes the symptom of running `create` before `init` without naming
// either command.
//
// Still unwraps to fs.ErrNotExist, because callers that tolerate an absent
// configuration — OpenEvalConfig, and the reference resolution above it — decide
// that by asking, and a nicer sentence that stopped answering would turn every
// one of those into a failure.
func noEvalConfig(path string) error {
	return &missingFileError{
		msg: fmt.Sprintf(
			"no eval configuration at %s; run `azd ai eval init` to scaffold one",
			filepath.ToSlash(path)),
	}
}

type missingFileError struct{ msg string }

func (e *missingFileError) Error() string { return e.msg }
func (e *missingFileError) Unwrap() error { return fs.ErrNotExist }

// ParsingEvalConfig reports a configuration file that would not parse.
func ParsingEvalConfig(path string, err error) error {
	return fmt.Errorf("parsing eval config %q: %w", filepath.ToSlash(path), err)
}

// SerializingEvalConfig reports a configuration that would not serialize.
func SerializingEvalConfig(err error) error {
	return fmt.Errorf("serializing eval config: %w", err)
}

// WritingEvalConfig reports a configuration file that would not be written.
func WritingEvalConfig(path string, err error) error {
	return fmt.Errorf("writing eval config %q: %w", filepath.ToSlash(path), err)
}

// ErrAmbiguousAgentService reports that a target name matched more than one service.
var ErrAmbiguousAgentService = errors.New("more than one agent service matches")

// AmbiguousAgentService reports a --target that names no single set of instructions.
func AmbiguousAgentService(agent string, matched []string) error {
	return fmt.Errorf(
		"%w %q: %s. Name one of them with --target, or pass the text with "+
			"--agent-instruction",
		ErrAmbiguousAgentService, agent, strings.Join(matched, ", "))
}

// InstructionFileUnreadable reports optimize metadata pointing at a missing file.
func InstructionFileUnreadable(metadataPath, named string, err error) error {
	return fmt.Errorf(
		"%s names instruction_file %q, which could not be read: %w",
		metadataPath, named, err)
}

// ListingTruncated reports a page walk that stopped before the end.
//
// Worth saying out loud rather than logging: a short evaluator listing resolves
// the latest version from the pages that arrived, so a truncated one can pick
// an older version and report nothing unusual.
func ListingTruncated(pages int) error {
	return fmt.Errorf(
		"stopped reading the listing after %d pages, so it may be incomplete", pages)
}

// ServiceRefPointsElsewhere reports an existing service entry wired to a
// different configuration than the one just scaffolded.
func ServiceRefPointsElsewhere(serviceName, have, want string) error {
	return fmt.Errorf(
		"service %q already points at %s, and the configuration just written is "+
			"%s; point the service's $ref at the one you want, or scaffold with "+
			"--path %s", serviceName, have, want, have)
}

// InstructionFileOutsideProject reports metadata pointing outside the project.
//
// The pointer is read from a file in the checkout, so it is only as trustworthy
// as the checkout: an absolute path or one climbing out with `..` would read
// something the project does not contain and send it on as agent instructions.
func InstructionFileOutsideProject(metadataPath, named string) error {
	return fmt.Errorf(
		"%s names instruction_file %q, which is outside the project; "+
			"name a path inside it", metadataPath, named)
}

// FromNotASource reports a --from value the generation service has no path for.
func FromNotASource(from string, sources []string) error {
	return fmt.Errorf(
		"--from %q is not a source; use one of %s",
		from, strings.Join(sources, ", "))
}

// ConfigLockUnavailable reports a config lock that could not be taken.
//
// Fatal, because the callers holding it read the configuration, change one
// entry and write the whole document back. Two of them running unlocked both
// report success and the later write drops the earlier one's entry, which is a
// worse outcome than being asked to run the command again.
func ConfigLockUnavailable(evalDir string, err error) error {
	if err == nil {
		return fmt.Errorf(
			"another process is still updating %s, so this update was not run: "+
				"wait for it to finish and try again", filepath.ToSlash(evalDir))
	}
	return fmt.Errorf(
		"could not lock %s, so this update was not run rather than risk "+
			"overwriting another process's changes: %w",
		filepath.ToSlash(evalDir), err)
}

// InvalidNextLink reports a pagination link the service sent that will not parse.
func InvalidNextLink(link string, err error) error {
	return fmt.Errorf("invalid nextLink %q: %w", link, err)
}

// NextLinkOffOrigin reports a pagination link pointing somewhere other than the
// project endpoint. Following it would send the caller's token to that host.
func NextLinkOffOrigin(origin string) error {
	return fmt.Errorf("refusing to follow nextLink to %s: it is not the project endpoint", origin)
}

// PageLinkLeftTheService reports a paging link pointing somewhere else.
//
// The link arrives in a response body and this client sends an Authorization
// header, so following one to another host would send the token there.
func PageLinkLeftTheService(expected, got string) error {
	return fmt.Errorf(
		"the service returned a paging link for %q while this client is "+
			"talking to %q, so it was not followed", got, expected)
}

// SampleSizeOutOfRange reports a row count the generation service would reject.
func SampleSizeOutOfRange(min, max, got int) error {
	return fmt.Errorf("sample size must be between %d and %d, got %d", min, max, got)
}

// MaxSamplesNegative reports a declared row cap below zero.
//
// Anything not above zero reads as "no cap", so this used to send the whole
// dataset to a run that is billed per row -- the opposite of what a cap asks
// for, and silent.
func MaxSamplesNegative(got int) error {
	return fmt.Errorf(
		"max_samples cannot be negative, got %d. "+
			"Remove it to send every row, or set the number of rows to send", got)
}

// NegativeMaxSamplesFlag reports the same thing given on the command line.
func NegativeMaxSamplesFlag(got int) error {
	return fmt.Errorf(
		"--max-samples cannot be negative, got %d. "+
			"Omit it to send every row, or give the number of rows to send", got)
}

// FlagDoesNotApply reports a flag given to a generate that produces nothing it
// could affect.
//
// Each of these is read while building one kind of artifact and ignored while
// building the other, so given for the wrong one they were accepted and
// dropped: `--evaluator --max-samples 50` produced a rubric and said nothing
// about the 50.
func FlagDoesNotApply(flag, narrowedBy string) error {
	return fmt.Errorf(
		"--%s has no effect on what %s generates. "+
			"Drop --%s, or drop %s to generate both", flag, narrowedBy, flag, narrowedBy)
}

// NegativeTraceDays reports a trace window below zero.
//
// Zero already means "do not read traces", so a negative value has nothing
// left to mean; it used to be accepted and treated as zero, which silently
// produced a rubric with none of the trace seeding that was asked for.
func NegativeTraceDays(got int) error {
	return fmt.Errorf(
		"--trace-days cannot be negative, got %d. "+
			"Use 0 to seed the rubric from no traces, or the number of days to read", got)
}

// OutputDirNeedsTheWait reports an output directory that nothing will be
// written to.
//
// --no-wait returns as soon as the job is submitted, so there is no artifact
// to place. Accepting both left the caller waiting for a file that was never
// coming.
func OutputDirNeedsTheWait() error {
	return errors.New(
		"--output-dir has nothing to write to with --no-wait, which returns " +
			"before the artifact exists. Drop --no-wait, or collect the " +
			"artifact later with `azd ai eval job show`")
}

// EndpointEmpty reports a project endpoint given as blank.
func EndpointEmpty() error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		"project endpoint must not be empty",
		"provide a Foundry project endpoint URL "+
			"(e.g. https://<account>.services.ai.azure.com/api/projects/<project>)",
	)
}

// EndpointUnparseable reports a project endpoint that is not a URL.
func EndpointUnparseable(err error) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf("invalid project endpoint URL: %v", err),
		"provide a valid https:// Foundry project endpoint URL",
	)
}

// EndpointNotHTTPS reports a project endpoint on the wrong scheme.
func EndpointNotHTTPS() error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		"project endpoint must use https",
		"provide an https:// URL",
	)
}

// EndpointInAnotherCloud reports a Foundry endpoint in a cloud this extension
// cannot reach.
//
// Separated from EndpointNotFoundryHost because the two are different problems
// with different answers. A malformed host is something the reader can fix; a
// Government endpoint is correct and simply unsupported, and telling them it is
// "not a recognized Foundry host" sends them to check a URL that is already
// right.
func EndpointInAnotherCloud(host, cloud string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf(
			"project endpoint %q is a Foundry endpoint in %s, which this extension "+
				"does not support yet", host, cloud,
		),
		"use a public Azure Foundry project, or track sovereign cloud support before "+
			"relying on this extension there",
	)
}

// EndpointNotFoundryHost reports a project endpoint pointing somewhere else.
func EndpointNotFoundryHost(host, suffix string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf(
			"project endpoint host %q is not a recognized Foundry host (*%s)",
			host, suffix,
		),
		"the host must end with "+suffix,
	)
}

// EndpointHasPort reports a project endpoint carrying an explicit port.
func EndpointHasPort(host string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf("project endpoint host %q must not include a port", host),
		"remove the explicit port from the URL",
	)
}

// NoEndpoint reports a project endpoint that no source could supply.
func NoEndpoint() error {
	return exterrors.Dependency(
		exterrors.CodeMissingProjectEndpoint,
		"no Foundry project endpoint resolved",
		"persist a workspace default with `azd ai project set <endpoint>`, "+
			"or set FOUNDRY_PROJECT_ENDPOINT (or AZURE_AI_PROJECT_ENDPOINT) "+
			"in the active azd environment, "+
			"or export FOUNDRY_PROJECT_ENDPOINT (or AZURE_AI_PROJECT_ENDPOINT) in your shell",
	)
}

// ProjectContextClient reports the config helper failing to build.
func ProjectContextClient(err error) error {
	return fmt.Errorf("getProjectContext: %w", err)
}

// ProjectContextRead reports the persisted project context failing to read.
func ProjectContextRead(err error) error {
	return fmt.Errorf("getProjectContext: failed to read config: %w", err)
}

// ---------------------------------------------------------------------------
// Output
// ---------------------------------------------------------------------------

// Progress markers from the azd style guide, so the extension's lines sit
// alongside core's without a second vocabulary.
const (
	doneMark    = "(✓) Done:"    // finished successfully
	skippedMark = "(-) Skipped:" // intentionally not done, not a failure
	failedMark  = "(x) Failed:"  // the step did not complete
)

// Warning reports a problem that is not worth failing the command over.
func Warning(err error) string {
	return fmt.Sprintf("warning: %v\n", err)
}

// PortalLink closes a detail view with the asset's portal URL.
func PortalLink(url string) string {
	return fmt.Sprintf("Portal: %s\n", url)
}

// FlagRequired reports a value the command needs and cannot settle itself.
//
// It used to add "(running with --no-prompt)", which was untrue at every call
// site: none of them prompts, so the parenthetical named a flag the caller had
// not passed and implied that dropping it would make the command ask.
func FlagRequired(name string) error {
	return fmt.Errorf("--%s is required", name)
}

// Creating reports a directory or file that could not be created.
func Creating(path string, err error) error {
	return fmt.Errorf("creating %q: %w", filepath.ToSlash(path), err)
}

// Serializing reports a value that could not be written out.
func Serializing(path string, err error) error {
	return fmt.Errorf("serializing %q: %w", filepath.ToSlash(path), err)
}

// Writing reports a file that could not be written.
func Writing(path string, err error) error {
	return fmt.Errorf("writing %q: %w", filepath.ToSlash(path), err)
}

// ReadingPath reports a file or directory that could not be read.
func ReadingPath(path string, err error) error {
	return fmt.Errorf("reading %s: %w", path, err)
}

// quoteList renders names as a readable "a", "b" and "c".
func quoteList(values []string) string {
	if len(values) == 0 {
		return "nothing"
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	sort.Strings(quoted)
	if len(quoted) == 1 {
		return quoted[0]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
}

// pluralColumns agrees with however many columns are missing.
func pluralColumns(values []string) string {
	if len(values) == 1 {
		return "that column"
	}
	return "those columns"
}

// ---------------------------------------------------------------------------
// Talking to the service
// ---------------------------------------------------------------------------

// InvalidEndpointURL reports a client built on an endpoint that will not parse.
func InvalidEndpointURL(err error) error {
	return fmt.Errorf("invalid endpoint URL: %w", err)
}

// InvalidRequestPath reports a request path that will not parse.
func InvalidRequestPath(path string, err error) error {
	return fmt.Errorf("invalid request path %q: %w", path, err)
}

// CreatingRequest reports a request that could not be built.
func CreatingRequest(err error) error {
	return fmt.Errorf("failed to create request: %w", err)
}

// MarshalingRequest reports a request body that would not serialize.
func MarshalingRequest(err error) error {
	return fmt.Errorf("failed to marshal request: %w", err)
}

// SettingRequestBody reports a request body that would not attach.
func SettingRequestBody(err error) error {
	return fmt.Errorf("failed to set request body: %w", err)
}

// RequestFailed reports a request that never reached an answer.
//
// A credential that cannot mint a token fails here rather than as a 401, and
// the SDK's own text for it names neither azd nor the way out. isCredentialFailure
// decides which is which; see it for how.
//
// The hint is in the message as well as the suggestion because the suggestion
// is not rendered on every surface, and it offers a retry first: this call
// shells out to `azd auth token`, which has been seen to fail transiently
// against a login that was perfectly valid -- measured once at over 70 seconds,
// long enough to lose to a deadline.
func RequestFailed(err error) error {
	if isCredentialUnavailable(err) {
		// Not an expired login, and `azd auth login` cannot be run to fix it.
		return exterrors.Auth(
			exterrors.CodeAuthFailed,
			fmt.Sprintf(
				"could not get a token for the Foundry project because azd itself "+
					"could not be run: %v", err),
			"check that `azd` is installed and on PATH")
	}
	if isCredentialFailure(err) {
		return exterrors.Auth(
			exterrors.CodeLoginExpired,
			fmt.Sprintf(
				"could not get a token for the Foundry project: %v. "+
					"Try again; if it keeps failing, run `azd auth login`", err),
			"try the command again, then `azd auth login` if it keeps failing")
	}
	return fmt.Errorf("HTTP request failed: %w", err)
}

// isCredentialUnavailable reports the credential never having run at all, as
// opposed to running and being refused.
//
// azidentity's credentialUnavailableError is unexported, so this matches the
// two messages it carries for that case. Worth separating because the answer
// to both is not `azd auth login` -- you cannot log in with a tool that is not
// on PATH.
func isCredentialUnavailable(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "executable not found on path") ||
		strings.Contains(text, "is not recognized")
}

// ServiceRefused turns an unauthorized answer into one that says what to do.
// Every other status is left as the service reported it.
func ServiceRefused(status int, err error) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return exterrors.Auth(
			exterrors.CodeAuthFailed,
			fmt.Sprintf(
				"the Foundry project refused the request (HTTP %d): %v. "+
					"Run `azd auth login`, and check you have access to this project",
				status, err),
			"run `azd auth login`, and check you have access to this project")
	}
	return err
}

// isCredentialFailure reports whether the request failed because no token could
// be minted, rather than for any of the other reasons a request fails.
//
// Decided on the SDK's own error types. This used to also match the phrase
// "failed to acquire a token" anywhere in the text, which any error is free to
// contain -- a service that could not acquire a token bucket lease was told its
// login had expired and to run `azd auth login`.
//
// The credential names stay as a fallback because credentialUnavailableError is
// unexported: a credential that never ran can only be recognized by the name it
// puts in its own message.
func isCredentialFailure(err error) bool {
	if err == nil {
		return false
	}

	var authFailed *azidentity.AuthenticationFailedError
	var authRequired *azidentity.AuthenticationRequiredError
	if errors.As(err, &authFailed) || errors.As(err, &authRequired) {
		return true
	}

	text := err.Error()
	for _, credential := range []string{
		"AzureDeveloperCLICredential",
		"DefaultAzureCredential",
	} {
		if strings.Contains(text, credential) {
			return true
		}
	}
	return false
}

// ReadingResponseBody reports a response that could not be read.
func ReadingResponseBody(err error) error {
	return fmt.Errorf("failed to read response body: %w", err)
}

// ParsingResponse reports a response that could not be parsed.
func ParsingResponse(err error) error {
	return fmt.Errorf("failed to parse response: %w", err)
}

// ParsingNumber reports a numeric field that did not arrive as a number.
func ParsingNumber(data string, err error) error {
	return fmt.Errorf("parsing number %s: %w", data, err)
}

// InvalidContainerURI reports a storage URI the service handed back unusable.
func InvalidContainerURI(err error) error {
	return fmt.Errorf("invalid container SAS URI: %w", err)
}

// CreatingUploadRequest reports the blob upload request failing to build.
func CreatingUploadRequest(err error) error {
	return fmt.Errorf("failed to create upload request: %w", err)
}

// UploadingBlobFailed reports the blob upload never reaching an answer.
func UploadingBlobFailed(err error) error {
	return fmt.Errorf("failed to upload blob: %w", err)
}

// BlobUploadStatus reports storage refusing the upload.
func BlobUploadStatus(status int, body string) error {
	return fmt.Errorf("blob upload failed with status %d: %s", status, body)
}

// CreatingDownloadRequest reports the dataset download request failing to build.
func CreatingDownloadRequest(err error) error {
	return fmt.Errorf("failed to create download request: %w", err)
}

// DownloadingDatasetBlob reports the dataset download never reaching an answer.
func DownloadingDatasetBlob(err error) error {
	return fmt.Errorf("failed to download dataset from blob: %w", err)
}

// BlobDownloadStatus reports storage refusing the download.
func BlobDownloadStatus(status int) error {
	return fmt.Errorf("blob download failed with status %d", status)
}

// ReadingDatasetContent reports a downloaded dataset that could not be read.
func ReadingDatasetContent(err error) error {
	return fmt.Errorf("failed to read dataset content: %w", err)
}

// CreatingListRequest reports the container listing request failing to build.
func CreatingListRequest(err error) error {
	return fmt.Errorf("failed to create list request: %w", err)
}

// ListingContainerBlobs reports the container listing never reaching an answer.
func ListingContainerBlobs(err error) error {
	return fmt.Errorf("failed to list container blobs: %w", err)
}

// ContainerListStatus reports storage refusing the listing.
func ContainerListStatus(status int) error {
	return fmt.Errorf("container list failed with status %d", status)
}

// ReadingListResponse reports a container listing that could not be read.
func ReadingListResponse(err error) error {
	return fmt.Errorf("failed to read list response: %w", err)
}

// CreatingBlobDownloadRequest reports the blob download request failing to build.
func CreatingBlobDownloadRequest(err error) error {
	return fmt.Errorf("failed to create blob download request: %w", err)
}

// DownloadingBlob reports one blob's download never reaching an answer.
func DownloadingBlob(err error) error {
	return fmt.Errorf("failed to download blob: %w", err)
}

// BlobDownloadStatusFor reports storage refusing one named blob.
func BlobDownloadStatusFor(status int, blobName string) error {
	return fmt.Errorf("blob download failed with status %d for %s", status, blobName)
}

// ReadingBlobContent reports a downloaded blob that could not be read.
func ReadingBlobContent(err error) error {
	return fmt.Errorf("failed to read blob content: %w", err)
}

// ParsingProjectResourceID reports a project ARM id that will not parse.
func ParsingProjectResourceID(err error) error {
	return fmt.Errorf("failed to parse project resource ID: %w", err)
}

// EncodingSubscriptionID reports a subscription that would not encode for a URL.
func EncodingSubscriptionID(err error) error {
	return fmt.Errorf("failed to encode subscription ID: %w", err)
}

// NotAFoundryProjectResourceID reports an ARM id that names something else.
func NotAFoundryProjectResourceID(resourceID string) error {
	return fmt.Errorf(
		"resource ID does not represent a Foundry project (missing parent account): %s",
		resourceID)
}

// InvalidSubscriptionID reports a subscription id that is not a GUID.
func InvalidSubscriptionID(err error) error {
	return fmt.Errorf("invalid subscription ID format: %w", err)
}

// CouldNotReadAgentForModel reports a target agent whose deployment could not
// be read, leaving generation without a default model.
func CouldNotReadAgentForModel(agent string, err error) string {
	if notFound(err) {
		return fmt.Sprintf("  warning: no agent %q in this project, so there is no "+
			"deployment to default to; pass --generation-model\n", agent)
	}
	return fmt.Sprintf("  warning: could not read agent %q for its deployment: %v\n", agent, err)
}

// shellArg wraps a value a shell would otherwise read as more than one
// argument.
//
// A suggested command is written to be pasted and run. A name the caller chose
// -- `--name "my eval"` becomes the dataset's name -- turns
// `--dataset-name my eval` into one flag and a stray positional argument, which
// generate refuses without naming the cause.
//
// Double quotes are what cmd, PowerShell, bash and zsh all read the same way.
// A value containing $ or a backtick has no portable answer and is wrapped
// anyway: one argument that may expand still beats two that certainly break.
// Backslashes are left alone, so a Windows path comes back out as itself.
func shellArg(v string) string {
	if v == "" {
		return `""`
	}
	if !strings.ContainsAny(v, " \t\n\"'`$&|;<>()*?[]#~!") {
		return v
	}
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}

// ShellArg is shellArg for the command builders outside this package, so one
// rule decides how every printed command quotes what it carries.
func ShellArg(v string) string {
	return shellArg(v)
}

// ConfirmDelete asks before removing something published.
func ConfirmDelete(subject string) string {
	return fmt.Sprintf("Delete %s? This cannot be undone.", subject)
}

// DeleteNeedsForce reports a delete that could not ask, because nobody is
// there to answer.
func DeleteNeedsForce(subject string) error {
	return fmt.Errorf(
		"deleting %s removes it for good, and this command cannot ask for "+
			"confirmation without a terminal. Re-run with --force to confirm it "+
			"in advance",
		subject)
}

// DeleteCancelled reports the author answering no.
//
// A line rather than an error: they were asked, they said no, and nothing was
// deleted. That is the command doing its job, and exiting non-zero for it makes
// a deliberate answer indistinguishable from a failure to anything scripted.
func DeleteCancelled(subject string) string {
	return fmt.Sprintf("Left %s alone.\n", subject)
}
