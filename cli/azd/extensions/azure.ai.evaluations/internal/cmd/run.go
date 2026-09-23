// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"
	"azureaieval/internal/urlsafe"

	"github.com/spf13/cobra"
)

// Terminal run states reported by the service.
var terminalRunStates = map[string]bool{
	"completed": true,
	"failed":    true,
	"canceled":  true,
	"cancelled": true,
	"error":     true,
}

// runCompleted turns a run that did not complete into an error, so that a
// caller who waited for it exits non-zero.
//
// The results have already been printed by the time this is asked, which is
// the point: a run that errored has a reason worth reading, and reporting it
// and then exiting 0 tells a pipeline the evaluation passed. It is checked
// before the gate because the gate's exit code means "the evaluation
// regressed", and a run that never produced results has not regressed — it did
// not run. Distinguishing those two is what the separate code is for.
func runCompleted(run *eval_api.OpenAIEvalRun) error {
	if run == nil {
		return nil
	}
	switch strings.ToLower(run.Status) {
	case "completed", "":
		return nil
	}
	return messages.RunFinishedWithStatus(run.ID, run.Status)
}

// runIsTerminal reports whether the run has stopped moving.
//
// A gate read from a run still in progress is read from partial counts: it can
// fail a run that would have passed, and it can pass one that has not finished
// failing.
//
// Derived from the polling vocabulary rather than repeating it: a state the
// poller stops waiting on is a state whose counts are final, and keeping two
// lists let "error" fall out of this one -- which told anyone gating an errored
// run to pass --wait, on a run that had already stopped. The empty status is
// the one deliberate difference: polling keeps waiting on a run the service has
// not described yet, while a gate reads the counts it was handed.
func runIsTerminal(run *eval_api.OpenAIEvalRun) bool {
	if run == nil {
		return false
	}
	status := strings.ToLower(run.Status)
	return status == "" || terminalRunStates[status]
}

// newRunCommand builds the run group.
//
// `run` is a group, not an executable verb: once `run output` exists, a bare
// `run` would make `azd ai eval run list` read as "run the thing called list".
func newRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start and inspect evaluation runs.",
	}
	addRunSubcommands(cmd)
	cmd.AddCommand(buildRunCommand(
		"start", "Start a run of an eval that has been deployed."))
	return cmd
}

// buildRunCommand builds `run start`.

// runStartFlags carries what `run start` was asked for.
type runStartFlags struct {
	groupName   string
	datasetName string
	name        string
	maxSamples  int
	wait        bool
	failOn      string
	endpoint    string
	evalPath    string
	// noWait is the spec's spelling of --wait=false. Cobra does not derive
	// one bool from the other, so PreRun folds this into wait.
	noWait bool
}

// runStartAction starts a run and, unless asked not to, waits for its verdict.
type runStartAction struct {
	cmd   *cobra.Command
	flags *runStartFlags
}

func buildRunCommand(use, short string) *cobra.Command {
	flags := &runStartFlags{}

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		// The eval is named by --eval. Without this, `run start other-eval`
		// is accepted, ignored, and bills a run against the default eval.
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&runStartAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.groupName, "eval", "",
		"Name of the eval to run, or its id. Defaults to the only one declared.")
	cmd.Flags().StringVar(&flags.datasetName, "dataset", "",
		"Catalog dataset to read instead of the one the eval declares. "+
			"Must satisfy the eval's column schema.")
	cmd.Flags().StringVar(&flags.name, "name", "", "Name for this run. Defaults to the eval name plus a timestamp.")
	cmd.Flags().IntVar(&flags.maxSamples, "max-samples", 0,
		"Cap local, unregistered dataset rows. Unsupported for registered datasets, source-backed runs, "+
			"or reruns by eval ID. 0 disables a configured cap.")
	cmd.Flags().BoolVar(&flags.wait, "wait", true, "Block until the run reaches a terminal state.")
	addFailOnFlag(cmd, &flags.failOn)
	// The spec documents --no-wait, and cobra does not derive it from a bool.
	cmd.Flags().BoolVar(&flags.noWait, "no-wait", false, "Submit the run and return immediately.")
	cmd.PreRun = func(*cobra.Command, []string) {
		if flags.noWait {
			flags.wait = false
		}
	}
	cmd.MarkFlagsMutuallyExclusive("wait", "no-wait")
	cmd.Flags().StringVar(&flags.endpoint, "project-endpoint", "", "Foundry project endpoint.")
	addEvalPathFlag(cmd, &flags.evalPath)

	return cmd
}

func (a *runStartAction) Run() error {
	ctx := a.cmd.Context()

	// Parsed before any network work, so a malformed threshold costs
	// nothing to find out about.
	threshold, err := parseGate(a.flags.failOn)
	if err != nil {
		return err
	}
	// A gate is a verdict on a result. Returning before there is one
	// used to drop the gate silently, so `--no-wait --fail-on ...`
	// exited 0 however the run turned out -- a pipeline that believes
	// it is gated and is not.
	if !a.flags.wait && threshold.set {
		return messages.GateNeedsTheWait()
	}
	// resolveMaxSamples reads anything not above zero as "no cap", so a
	// negative one sent the whole dataset to a billed run.
	if a.flags.maxSamples < 0 {
		return messages.NegativeMaxSamplesFlag(a.flags.maxSamples)
	}

	ec, err := newEvalContext(ctx, a.flags.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	return a.start(ctx, ec, threshold)
}

func (a *runStartAction) start(ctx context.Context, ec *evalContext, threshold gate) error {
	out := a.cmd.OutOrStdout()
	// One flag takes a name or an id. A declared name also brings the
	// declaration, which is what says where rows come from; a bare id
	// has none, so the pairing comes from the eval's previous run.
	evalDir, err := ec.evalDir(ctx, a.flags.evalPath)
	if err != nil {
		return err
	}
	chosen, err := chooseEvalIn(a.cmd, evalDir, a.flags.groupName)
	if err != nil {
		// Closing the picker is an answer, not a failure to name something.
		if isEvalSelectionCancelled(err) {
			reportCancelledSelection(a.cmd)
			return nil
		}
		return err
	}
	ref, err := ec.resolveEvalRef(ctx, evalDir, chosen)
	if err != nil {
		return err
	}
	evalID := ref.ID
	configPath := ref.ConfigPath

	group, err := withRunDatasetOverride(ref, a.flags.datasetName)
	if err != nil {
		return err
	}

	if ref.Declared() {
		if err := ec.checkDatasetRegistered(ctx, ref.Config, group, configPath); err != nil {
			return err
		}
	}

	maxSamples, err := runMaxSamples(a.cmd, a.flags.maxSamples, group)
	if err != nil {
		return err
	}
	var dataSource *eval_api.EvalRunDataSource
	var datasetVersion string
	// The level a bare id runs at comes from its previous run, for the same
	// reason the data source does: there is no declaration to read it from.
	var reusedLevel string
	switch {
	case group == nil:
		dataSource, reusedLevel, err = ec.reuseDataSourceFromLastRun(ctx, evalID)
	default:
		dataSource, datasetVersion, err = ec.buildRunDataSource(ctx, group, configPath, maxSamples)
	}
	if err != nil {
		return err
	}

	if err := ec.validateResponsesRun(ctx, evalID, dataSource); err != nil {
		return err
	}

	// Local, so a default name is derived per invocation rather than
	// written back over the flag the command was built with.
	runName := a.flags.name
	if runName == "" {
		base := "eval"
		if group != nil {
			base = group.Name
		}
		runName = fmt.Sprintf("%s-%s", base, time.Now().UTC().Format("20060102-150405"))
	}

	metadata := map[string]string{}
	// The declaration says it when there is one; a bare id repeats what its
	// previous run recorded.
	level := resolveLevel(group)
	if level == "" {
		level = reusedLevel
	}
	if level != "" {
		metadata[metaEvaluationLevel] = level
	}
	// The eval carries its name in its own metadata, but a run is read
	// on its own, and an id is not what the author called it.
	if group != nil && group.Name != "" {
		metadata[metaEvalName] = group.Name
	}
	// Recorded per run, not read from the configuration at list time:
	// comparing two runs is the point of that listing, and the dataset
	// under an eval can change between them. A source-backed run scored
	// no dataset, so it records none.
	if group != nil && group.Dataset != "" && group.Source == nil {
		metadata[metaDataset] = group.Dataset
		if datasetVersion != "" {
			metadata[metaDatasetVersion] = datasetVersion
		}
	}

	recordSimulationMetadata(metadata, dataSource)
	run, err := ec.evalClient.CreateOpenAIEvalRun(ctx, evalID, &eval_api.CreateOpenAIEvalRunRequest{
		Name: runName,
		// Also sent under metadata, where it stays readable to anything listing
		// runs. Only the top-level field is what the service builds rows from.
		EvaluationLevel: level,
		DataSource:      dataSource,
		Metadata:        metadata,
	})
	if err != nil {
		return messages.StartingRun(err)
	}

	// Recorded per eval. A single shared key belongs to whichever eval ran
	// last, so another asking for "the last run" was handed one that is not
	// its own.
	ec.remember(ctx, idKey("evalrun", evalID), run.ID)

	if !a.flags.wait {
		if isJSON(a.cmd) {
			return emitJSON(out, startedRun(run, evalID, group))
		}
		fmt.Fprint(out, messages.RunStarted(run.ID, run.Status))
		fmt.Fprint(out, messages.ReattachToRun(run.ID, evalID))
		return nil
	}

	if !isJSON(a.cmd) {
		fmt.Fprint(out, messages.WaitingForRun(run.ID))
	}
	final, err := ec.pollRun(ctx, evalID, run.ID, out, isJSON(a.cmd))
	if errors.Is(err, errWaitBudgetSpent) {
		// A gate asked for a verdict that never arrived. Exiting 0 here
		// would tell a pipeline the gate passed, which is the silent
		// drop --no-wait is refused for, reached by running long.
		if threshold.set {
			return messages.GateOutlivedTheWait(run.ID, waitBudget)
		}
		// The run did not fail, the wait ran out. Same contract as
		// --no-wait: exit 0 and say how to pick it back up.
		if isJSON(a.cmd) {
			return emitJSON(out, startedRun(run, evalID, group))
		}
		fmt.Fprint(out, messages.WaitBudgetSpent(run.ID, waitBudget))
		fmt.Fprint(out, messages.ReattachToRun(run.ID, evalID))
		return nil
	}
	if err != nil {
		return err
	}
	final = ec.withPortalLink(ctx, evalID, final)

	if isJSON(a.cmd) {
		if err := emitJSON(out, final); err != nil {
			return err
		}
	} else {
		display := runForDisplay(final, evalID, run.ID)
		if err := renderRun(out, display, ec.runOutputSummary(ctx, evalID, display)); err != nil {
			return err
		}
	}

	// Last, so that the results are reported whether or not the gate
	// holds: a pipeline that only learns it failed is worse off than
	// one that can see by how much.
	if err := runCompleted(final); err != nil {
		return err
	}
	applyGate(a.cmd, threshold, final)
	return nil
}

func withRunDatasetOverride(ref evalRef, override string) (*project.Eval, error) {
	if override == "" {
		return ref.Eval, nil
	}
	if !ref.Declared() {
		return nil, messages.DatasetOverrideNeedsDeclaredEval()
	}
	if _, ok := ref.Config.DatasetDeclaration(override); !ok {
		return nil, messages.DatasetNotInCatalog(override, filepath.ToSlash(ref.ConfigPath))
	}
	// The eval keeps its own declaration; only this run reads elsewhere.
	overridden := *ref.Eval
	overridden.Dataset = override
	overridden.Source = nil
	return &overridden, nil
}

// checkDatasetRegistered fails when the group's local dataset has edits that
// were never deployed.
//
// Runs reference the published version, so unregistered edits would otherwise
// be ignored. An explicit version pin deliberately selects published content
// rather than the current local file.
func (ec *evalContext) checkDatasetRegistered(
	ctx context.Context,
	cfg *project.EvalConfig,
	group *project.Eval,
	configPath string,
) error {
	if declaredDatasetVersion(configPath, group) != "" {
		return nil
	}
	localPath := localDatasetPath(configPath, group)
	if localPath == "" {
		return nil
	}

	decl, ok := cfg.DatasetDeclaration(group.Dataset)
	if !ok {
		return nil
	}

	recorded := ec.privateValue(ctx, project.FingerprintKey("dataset", decl.Name))
	if recorded == "" {
		return nil
	}

	digest, err := project.Fingerprint(localPath)
	if err != nil {
		return messages.ReadingDataset(localPath, err)
	}
	if digest == recorded {
		return nil
	}

	return messages.DatasetHasUnregisteredEdits(decl.Name, ec.deployCommand(ctx))
}

// reuseDataSourceFromLastRun rebuilds a run's data source from the group's most
// recent run.
//
// `--eval-id` deliberately ignores the config, but a run still needs a target
// and a dataset, and an eval carries neither: the group holds only its
// testing criteria, and the dataset travels on the run. The previous run is the
// only place that pairing survives, so re-running a group means repeating what
// it last ran.
//
// The evaluation level travels with it. It decides how the service builds rows
// out of the data source, so repeating the source without it grades a
// conversation eval turn-shaped -- which reports scores rather than an error,
// and so is not otherwise noticed. Empty when the previous run recorded none,
// which leaves the service's own default as before.
func (ec *evalContext) reuseDataSourceFromLastRun(
	ctx context.Context,
	evalID string,
) (*eval_api.EvalRunDataSource, string, error) {
	// The service promises no order, so one row is not the most recent run --
	// it is whichever the listing happened to put first. Restarting from it
	// scored a stale dataset or target on any eval with more than one run.
	//
	// Read whole rather than capped: a prefix of an unordered listing cannot
	// contain the newest row, so a cap would trade a wrong answer for a
	// cheaper one. collectPages bounds the walk and reports a listing it could
	// not finish as an error, so this is either right or it says so.
	list, err := ec.evalClient.ListOpenAIEvalRuns(ctx, evalID, 0)
	if err != nil {
		if eval_api.IsNotFound(err) {
			// The eval itself is missing, which is worth saying plainly rather
			// than as forty lines of the 404 that discovered it.
			return nil, "", messages.EvalNotFound(evalID)
		}
		return nil, "", messages.ReadingPreviousRuns(evalID, err)
	}
	if list == nil || len(list.Data) == 0 {
		return nil, "", messages.EvalHasNoPreviousRun(evalID)
	}
	newest := newestRunIn(list.Data)
	if newest.DataSource == nil {
		return nil, "", messages.EvalHasNoPreviousRun(evalID)
	}
	if source := newest.DataSource.Source; source != nil &&
		source.Type == eval_api.EvalRunDataContentTypeFileContent &&
		newest.Metadata[metaDataset] != "" {
		version, err := ec.resolveRunDatasetVersion(
			ctx, newest.Metadata[metaDataset], newest.Metadata[metaDatasetVersion], true)
		if err != nil {
			return nil, "", err
		}
		if version != "" {
			return nil, "", exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"the previous run used inline data attributed to a registered dataset",
				"Start the declared eval by name without --max-samples or max_samples to bind its registered version. "+
					"The previous inline rows may be a subset and cannot safely be replaced by the whole version.",
			)
		}
	}
	return pinReusedTraceWindow(newest.DataSource), reusedEvaluationLevel(newest), nil
}

// reusedEvaluationLevel is the level a rerun repeats.
//
// The service's own field wins. A run created by the portal or an SDK carries
// it and carries none of the metadata this extension writes, so reading only
// the metadata reran such a run turn-shaped whatever it had been. The metadata
// remains the fallback, because runs this extension made before the field was
// read carry the level only there.
func reusedEvaluationLevel(run *eval_api.OpenAIEvalRun) string {
	if run == nil {
		return ""
	}
	if run.EvaluationLevel != "" {
		return run.EvaluationLevel
	}
	return run.Metadata[metaEvaluationLevel]
}

// legacyTraceLookbackHours is the window a legacy source with no lookback ran
// under: the service's own default of seven days.
//
// Recorded here because the old data source had no start bound of its own --
// it carried agent_name, lookback_hours, end_time and max_traces, and nothing
// else -- so a run that set no lookback was graded over whatever the service
// chose. Carrying such a run forward with no start at all would widen it to all
// of history instead.
const legacyTraceLookbackHours = 24 * 7

// pinReusedTraceWindow closes the window a reattached run repeats.
//
// A run reached by id repeats whatever data source the last one sent, and a
// trace window with a start and no end means "up to now". Replaying it a week
// later grades a week more than the run it was copied from, and the run after
// that more again, so the span grows without limit and nothing says so.
//
// A window with no start at all is repeated as it stands: it says "everything",
// which is what it said when it was recorded, and closing it would freeze a
// declaration that never asked to be bounded. So is a window that already has
// an end, which cannot widen and is not this function's to move.
//
// It is graded over the span it covers rather than the span it covered: the
// declaration is where a window that should move with each run comes from, and
// a run reached by id has no declaration to read. Freezing it once is the
// closest a shape with no lookback can come to one.
//
// Pinning the end at now also excludes traces the service has not finished
// ingesting, which an open end would have picked up on the next run.
//
// The argument is never modified: what the previous run sent is history, and a
// caller that logs or emits it should see what was recorded.
func pinReusedTraceWindow(ds *eval_api.EvalRunDataSource) *eval_api.EvalRunDataSource {
	switch {
	case ds == nil:
		return ds
	case ds.Type == eval_api.EvalRunDataSourceTypeTraces:
		return upgradeLegacyTraceSource(ds)
	case ds.Type == eval_api.EvalRunDataSourceTypeTracePreview:
		if ds.TraceSource == nil || ds.TraceSource.EndTime != 0 || ds.TraceSource.StartTime == 0 {
			return ds
		}
		pinned := *ds
		filter := *ds.TraceSource
		filter.EndTime = time.Now().Unix()
		pinned.TraceSource = &filter
		return &pinned
	default:
		return ds
	}
}

// upgradeLegacyTraceSource carries a run recorded under the old trace shape
// onto the one that keeps what it is given.
//
// Without it, an eval whose last run predates the change would keep sending the
// version-blind source for good, and nothing would say so.
func upgradeLegacyTraceSource(ds *eval_api.EvalRunDataSource) *eval_api.EvalRunDataSource {
	// Without an agent the preview shape carries no filter at all, and
	// omitempty drops it: the reattached run would read every agent's spans,
	// a broader and costlier query than the one it is repeating. Repeating
	// what was recorded is the lesser wrong.
	if ds.AgentName == "" {
		return ds
	}
	end := time.Now()
	if ds.EndTime > 0 {
		end = time.Unix(ds.EndTime, 0)
		// A recorded end in the future would close the window after the last
		// trace that exists, which reads nothing past now and says nothing.
		if end.After(time.Now()) {
			end = time.Now()
		}
	}
	// The recorded values are whatever an older build sent, from before the
	// bounds existed, so they are clamped rather than trusted: a lookback beyond
	// what a window may cover reaches back further than any trace was recorded,
	// and the reattached run reads nothing.
	hours := ds.LookbackHours
	if hours <= 0 || hours > project.MaxLookbackHours {
		hours = legacyTraceLookbackHours
	}
	start := end.Add(-time.Duration(hours) * time.Hour)
	// A recorded end early enough to put the start at or before the epoch would
	// send a bound the wire drops, or a negative one -- the same silence a
	// declaration is refused for. Reaching back from now instead keeps the
	// length of the window the run asked for.
	if start.Unix() <= 0 {
		end = time.Now()
		start = end.Add(-time.Duration(hours) * time.Hour)
	}
	// Only reachable on a machine whose clock is set before about 1980, where
	// even now minus the longest window a declaration may name lands in the
	// pre-epoch. Dropping the bound says "everything", which is at least what
	// the legacy shape said when it carried no start.
	if start.Unix() <= 0 {
		start = time.Time{}
	}
	// A negative cap is no cap at all, and leaving it off means the service's
	// own default of a thousand traces -- a bigger, costlier run than the one
	// being repeated. The cap `init` writes is bounded and can be raised in the
	// declaration, which is the only place a considered value can come from.
	maxTraces := ds.MaxTraces
	if maxTraces < 0 {
		maxTraces = project.DefaultScaffoldMaxTraces
	}
	// The old shape carried no version, so this pins nothing that was not
	// pinned before; it stops the service choosing differently run to run only
	// once the declaration names one.
	return eval_api.NewTracePreviewDataSource(ds.AgentName, "", start, end, maxTraces)
}

// runnableEval refuses a declaration this run could not carry out.
//
// The rules live with the check the configuration runs, so the two cannot come
// to different conclusions about the same eval. Only the wrapper differs: the
// configuration has an index to name and a run does not.
func runnableEval(group *project.Eval) error {
	if err := project.ValidateRunnable(group); err != nil {
		return messages.InEval(group.Name, err)
	}
	return nil
}

// buildRunDataSource binds the eval's rows to the run and returns the resolved
// dataset version for metadata. Unregistered rows carry no version.
//
// Three shapes, in the order the configuration decides them. A `source:` block
// hands the gathering to the service and sends nothing local. Otherwise the
// rows come from a dataset, and `target:` says what to invoke for each one --
// including nothing at all, when the rows already hold both sides.
//
// This is where a declaration is refused, not merely where it is read. Resolving
// an eval by name does not validate what it says about itself, and a run reached
// by id has no declaration to validate, so every contradiction the configuration
// names has to be answered here as well. Settling one by evaluation order sends
// a request that succeeds and grades something the file did not ask for.
func (ec *evalContext) buildRunDataSource(
	ctx context.Context,
	group *project.Eval,
	configPath string,
	maxSamples int,
) (*eval_api.EvalRunDataSource, string, error) {
	if group == nil {
		return nil, "", messages.NoEvalToRun()
	}
	if err := runnableEval(group); err != nil {
		return nil, "", err
	}
	decl := &project.DatasetDecl{}
	if group.Dataset != "" && configPath != "" {
		cfg, err := project.LoadEvalConfig(configPath)
		if err != nil {
			return nil, "", err
		}
		var ok bool
		decl, ok = cfg.DatasetDeclaration(group.Dataset)
		if !ok {
			return nil, "", messages.InEval(group.Name, messages.DatasetNotDeclared(group.Dataset))
		}
	}

	// A simulation creates its conversations instead of reading rows that
	// already hold them, so it is settled before every shape that reads a
	// column: a source has none to read, and a scenario seed has no question
	// on it to bind. ValidateRunnable has already refused a declaration that
	// asks for both, so this order decides nothing on its own -- it is here so
	// that adding a shape below cannot quietly claim a simulation.
	if group.Simulation != nil {
		return ec.simulationDataSource(ctx, group, decl.Version, maxSamples)
	}

	if group.Source != nil {
		if maxSamples > 0 {
			return nil, "", sourceSampleConflict()
		}
		var ds *eval_api.EvalRunDataSource
		var err error
		switch group.Source.Type {
		case project.SourceTypeTraces:
			ds, err = tracesDataSource(group)
		default:
			ds, err = responsesDataSource(group)
		}
		return ds, "", err
	}

	var ds *eval_api.EvalRunDataSource
	switch {
	case group.Target == nil || group.Target.Name == "":
		// Nothing to invoke: the dataset is scored as it stands.
		ds = eval_api.NewDatasetOnlyDataSource()
	case group.Target.Type == project.TargetTypeModel:
		ds = eval_api.NewModelTargetDataSource(group.Target.Name)
	default:
		// `init` writes the azure.yaml service key here, which is a local label.
		// The agent is published under whatever the service declares, so the key
		// has to be resolved before it is sent or the run grades another agent.
		agent, err := ec.remoteAgentName(ctx, group.Target.Name)
		if err != nil {
			return nil, "", messages.InEval(group.Name, err)
		}
		ds = eval_api.NewAgentTargetDataSource(agent, nil)
	}

	if group.Dataset == "" {
		return nil, "", messages.EvalHasNoDataset(group.Name)
	}

	localPath := decl.File
	if localPath != "" && !filepath.IsAbs(localPath) {
		localPath = filepath.Join(filepath.Dir(configPath), localPath)
	}
	version, err := ec.resolveRunDatasetVersion(
		ctx, group.Dataset, decl.Version, localPath != "")
	if err != nil {
		return nil, "", err
	}
	if version != "" {
		if maxSamples > 0 {
			return nil, "", exterrors.Validation(
				exterrors.CodeConflictingArguments,
				fmt.Sprintf("eval %q: --max-samples or max_samples (%d) conflicts with registered dataset %q version %q",
					group.Name, maxSamples, group.Dataset, version),
				"Registered datasets must retain their version identity; this run API has no supported row-subset option. "+
					"Remove the cap (or pass --max-samples 0), or explicitly publish a smaller dataset and select it. "+
					"No temporary dataset is published automatically.",
			)
		}
		id, err := ec.datasetResourceID(ctx, group.Dataset, version)
		if err != nil {
			return nil, "", err
		}
		items, err := ec.readDatasetVersion(ctx, group.Dataset, version)
		if err != nil {
			return nil, "", err
		}
		if err := refuseUnboundTemplate(group, ds, items); err != nil {
			return nil, "", err
		}
		ds.SetFileID(id)
		return ds, version, nil
	}

	items, err := readJSONL(localPath, maxSamples)
	if err != nil {
		return nil, "", err
	}
	if len(items) == 0 {
		return nil, "", messages.DatasetFileEmpty(localPath)
	}
	if err := refuseUnboundTemplate(group, ds, items); err != nil {
		return nil, "", err
	}
	ds.SetFileContent(items)
	return ds, "", nil
}

// refuseUnboundTemplate refuses a run whose target invocation reads a column no
// row carries.
//
// The service does not report this. It invokes the target with an empty value
// and scores whatever comes back, so a conversation dataset run against an
// agent target produced confident scores for a question nobody asked -- and
// the seeded content, not the target's answer, is what got graded.
func refuseUnboundTemplate(
	group *project.Eval, ds *eval_api.EvalRunDataSource, items []map[string]any,
) error {
	missing := ds.MissingTemplateFields(items)
	if len(missing) == 0 {
		return nil
	}

	// Named inside the structured message rather than wrapped with InEval: azd
	// serializes a structured error's own message, so an outer %w prefix is not
	// what the reader is shown.
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf("eval %q: dataset %q carries no %s column, which invoking the target reads from every row",
			group.Name, group.Dataset, quotedList(missing)),
		fmt.Sprintf("Rows carry %s. Score these rows as they stand by removing the target from the eval, "+
			"or point the eval at a dataset whose rows carry %s.",
			quotedList(datasetColumns(items)), quotedList(missing)),
	)
}

// datasetColumns is every key the rows carry, sorted so two runs of the same
// dataset report it the same way.
func datasetColumns(items []map[string]any) []string {
	seen := map[string]struct{}{}
	for _, item := range items {
		for key := range item {
			seen[key] = struct{}{}
		}
	}
	columns := make([]string, 0, len(seen))
	for key := range seen {
		columns = append(columns, key)
	}
	sort.Strings(columns)
	return columns
}

func quotedList(values []string) string {
	if len(values) == 0 {
		return "no columns"
	}
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, strconv.Quote(v))
	}
	return strings.Join(quoted, ", ")
}

// tracesDataSource evaluates conversations the agent already had.
//
// The service reads them from Application Insights, so the agent has to be
// emitting gen_ai.input.messages / gen_ai.output.messages for anything to be
// found. `agent_name` filters the traces; it is not a target, because a trace
// run invokes nothing.
func tracesDataSource(group *project.Eval) (*eval_api.EvalRunDataSource, error) {
	// runnableEval has already refused an empty one; read rather than assumed,
	// because the agent name is what the whole request is about.
	agent := project.TraceAgentName(group.Source, group.Target)
	if agent == "" {
		return nil, messages.InEval(group.Name, messages.TraceSourceNeedsAnAgent())
	}

	start, end, err := traceWindow(group.Name, group.Source)
	if err != nil {
		return nil, err
	}
	return eval_api.NewTracePreviewDataSource(
		agent,
		group.Source.AgentVersion,
		start,
		end,
		group.Source.MaxTraces,
	), nil
}

// traceWindow resolves the bounds of the span a trace run reads.
//
// The rules live in the project package, with the check the configuration runs,
// so a source is judged the same way whichever door the eval came through.
func traceWindow(evalName string, source *project.SourceDecl) (start, end time.Time, err error) {
	start, end, err = project.ValidateSource(source)
	if err != nil {
		return time.Time{}, time.Time{}, messages.InEval(evalName, err)
	}
	return start, end, nil
}

// responsesDataSource evaluates responses the project already stored.
func responsesDataSource(group *project.Eval) (*eval_api.EvalRunDataSource, error) {
	if len(group.Source.ResponseIDs) == 0 {
		return nil, messages.InEval(group.Name, messages.ResponsesSourceNeedsResponseIDs())
	}
	return eval_api.NewResponsesDataSource(group.Source.ResponseIDs, group.Source.MaxTurns), nil
}

// readRegisteredDataset fetches published rows for validation, not submission.
func (ec *evalContext) readRegisteredDataset(
	ctx context.Context,
	name string,
	pinned string,
) ([]map[string]any, string, error) {
	version, err := ec.resolveRunDatasetVersion(ctx, name, pinned, false)
	if err != nil {
		return nil, "", err
	}
	items, err := ec.readDatasetVersion(ctx, name, version)
	return items, version, err
}

// resolveRunDatasetVersion prefers the declaration, then the recorded publication,
// then the latest service version. Only confirmed absence permits local rows.
func (ec *evalContext) resolveRunDatasetVersion(
	ctx context.Context, name, pinned string, allowLocal bool,
) (string, error) {
	version := pinned
	if version == "" {
		version = ec.privateValue(ctx, versionKey("dataset", name))
		if ec.stateErr != nil && (ec.envName != "" || !isNoDefaultEnvironmentError(ec.stateErr)) {
			return "", messages.ReadingDataset(name, ec.stateErr)
		}
	}
	if version != "" {
		return version, nil
	}
	version, err := ec.lookupRunDatasetVersion(ctx, name)
	if _, absent := errors.AsType[*unregisteredDatasetError](err); absent && allowLocal {
		return "", nil
	}
	return version, err
}

// unregisteredDatasetError distinguishes verified absence from an unreadable
// registry. It does not manufacture an HTTP error for a successful empty listing.
type unregisteredDatasetError struct{ name string }

func (e *unregisteredDatasetError) Error() string {
	return messages.DatasetHasNoVersionsToRead(e.name).Error()
}

func (ec *evalContext) lookupRunDatasetVersion(ctx context.Context, name string) (string, error) {
	if ec.datasetClient == nil {
		return "", messages.ReadingDataset(name, errors.New("dataset client is unavailable"))
	}
	versions, err := ec.datasetClient.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
	if err != nil && !dataset_api.IsNotFound(err) {
		return "", messages.ReadingDataset(name, err)
	}
	if versions != nil && len(versions.Value) > 0 {
		return dataset_api.LatestVersion(versions.Value), nil
	}
	// Foundry returns a successful empty list for unknown datasets. Probe the
	// first publish versions as well, because the listing can lag publication.
	for _, first := range firstDatasetVersions {
		_, getErr := ec.datasetClient.GetDataset(ctx, name, first, ProjectEndpointAPIVersion)
		if getErr == nil {
			return first, nil
		}
		if !dataset_api.IsNotFound(getErr) {
			return "", messages.ReadingDatasetVersion(name, first, getErr)
		}
	}
	return "", &unregisteredDatasetError{name: name}
}

func (ec *evalContext) readDatasetVersion(
	ctx context.Context, name, version string,
) ([]map[string]any, error) {
	body, err := ec.datasetClient.OpenDatasetContent(
		ctx, name, version, ProjectEndpointAPIVersion)
	if err != nil {
		return nil, messages.ReadingDatasetVersion(name, version, err)
	}
	defer body.Close()

	items, err := scanJSONL(body, 0)
	if err != nil {
		return nil, messages.ReadingDatasetVersion(name, version, err)
	}
	if len(items) == 0 {
		return nil, messages.DatasetVersionEmpty(name, version)
	}
	return items, nil
}

// datasetResourceID requires the identity issued by the service for this exact version.
func (ec *evalContext) datasetResourceID(ctx context.Context, name, version string) (string, error) {
	if name == "" || version == "" || ec.datasetClient == nil {
		return "", messages.ReadingDatasetVersion(name, version, errors.New("dataset identity cannot be resolved"))
	}
	registered, err := ec.datasetClient.GetDataset(ctx, name, version, ProjectEndpointAPIVersion)
	if err != nil {
		return "", messages.ReadingDatasetVersion(name, version, err)
	}
	if registered == nil || strings.TrimSpace(registered.ID) == "" {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("dataset %q version %q could not be resolved to a registered id", name, version),
			"Publish the dataset with `azd ai eval create` or `azd up`, then retry. "+
				"A run requires the service-issued version id and cannot fall back to inline rows.",
		)
	}
	if registered.Version != "" && registered.Version != version {
		return "", messages.ReadingDatasetVersion(name, version,
			fmt.Errorf("service returned version %q instead of the requested version", registered.Version))
	}
	return registered.ID, nil
}

// datasetColumnsFromPath reads one row to learn the dataset's shape. An empty
// path, or an unreadable file, yields nil.
func datasetColumnsFromPath(localPath string) map[string]bool {
	if localPath == "" {
		return nil
	}
	// One row is enough to learn the shape.
	items, err := readJSONL(localPath, 1)
	if err != nil || len(items) == 0 {
		return nil
	}
	columns := make(map[string]bool, len(items[0]))
	for name := range items[0] {
		columns[name] = true
	}
	return columns
}

// localDatasetPath resolves a declared file. Its presence does not imply the
// dataset is unregistered: publication deliberately retains the file declaration.
func localDatasetPath(configPath string, group *project.Eval) string {
	cfg, err := project.LoadEvalConfig(configPath)
	if err != nil || group == nil {
		return ""
	}
	decl, ok := cfg.DatasetDeclaration(group.Dataset)
	if !ok || decl.File == "" {
		return ""
	}
	if filepath.IsAbs(decl.File) {
		return decl.File
	}
	return filepath.Join(filepath.Dir(configPath), decl.File)
}

// declaredDatasetVersion is the `version:` the catalog pins this dataset to.
//
// A pin is the author saying which rows to score, and it is read from the
// declaration rather than from the environment: the recorded version means the
// one the file's content published, which is what the deploy's drift check
// compares against, and overwriting it with a pin made removing the pin later
// read as somebody publishing behind the configuration's back.
func declaredDatasetVersion(configPath string, group *project.Eval) string {
	if group == nil || configPath == "" {
		return ""
	}
	cfg, err := project.LoadEvalConfig(configPath)
	if err != nil || cfg == nil {
		return ""
	}
	decl, ok := cfg.DatasetDeclaration(group.Dataset)
	if !ok {
		return ""
	}
	return decl.Version
}

// readJSONL reads newline-delimited JSON, optionally truncating to limit rows.
func readJSONL(path string, limit int) ([]map[string]any, error) {
	// #nosec G304 -- path is the dataset file the eval config declares.
	f, err := os.Open(path)
	if err != nil {
		return nil, messages.ReadingDataset(path, err)
	}
	defer f.Close()

	items, err := scanJSONL(f, limit)
	if err != nil {
		return nil, messages.ReadingDataset(path, err)
	}
	return items, nil
}

// scanJSONL reads rows until the limit is reached, so a subset costs only the
// rows it needs to parse -- and, when the reader is a response body, only the
// bytes it needs to transfer.
func scanJSONL(r io.Reader, limit int) ([]map[string]any, error) {
	var items []map[string]any
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		if line == 1 {
			// Windows editors and PowerShell's Set-Content write a mark ahead
			// of the first row. The upload path strips it, so without this the
			// same file deploys and then fails to score.
			text = project.TrimBOM(text)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(text), &row); err != nil {
			return nil, messages.JSONLLineInvalid(line, err)
		}
		items = append(items, row)
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// resolveLevel prefers the flag, then the eval's own declaration.
// resolveLevel is the eval's declared scoring granularity.
//
// There is no per-run override: the level decides the row mapping, so two
// levels under one eval would put incomparable result sets in the same history,
// and it would bypass the supported_evaluation_levels check `azd up` does
// against the declared level. A second level is a second eval.
func resolveLevel(group *project.Eval) string {
	if group != nil {
		return group.EvaluationLevel
	}
	return ""
}

// resolveMaxSamples prefers the flag, then the eval's own declaration, matching
// how the evaluation level resolves.
//
// Without this, max_samples parsed and did nothing: an eval that caps its
// sample count in config would send the whole dataset, and only a flag on every
// invocation would honour the cap.
func resolveMaxSamples(flag int, group *project.Eval) int {
	if flag > 0 {
		return flag
	}
	if group != nil && group.MaxSamples > 0 {
		return group.MaxSamples
	}
	return 0
}

func runMaxSamples(cmd *cobra.Command, flag int, group *project.Eval) (int, error) {
	if cmd.Flags().Changed("max-samples") {
		if group == nil {
			return 0, exterrors.Validation(exterrors.CodeConflictingArguments,
				"--max-samples cannot change a data source reused by eval id",
				"Run a declared eval by name to select its dataset and cap, "+
					"or omit --max-samples to repeat the previous source.")
		}
		if group.Source != nil {
			return 0, sourceSampleConflict()
		}
		return flag, nil
	}
	return resolveMaxSamples(flag, group), nil
}

func sourceSampleConflict() error {
	return exterrors.Validation(exterrors.CodeConflictingArguments,
		"--max-samples or max_samples cannot cap a source-backed run",
		"Remove the dataset cap. For traces, use source.max_traces; for responses, select source.response_ids.")
}

// errWaitBudgetSpent says the run outlived the wait, not that anything failed.
//
// It is handled where --no-wait is: the run is still going server-side, so the
// caller is handed the same reattach line and the same exit code.
var errWaitBudgetSpent = errors.New("wait budget spent")

// waitBudget bounds a foreground wait.
//
// Not a policy about how long an evaluation may take -- it is a guard against
// waiting on a run that will never reach a terminal state. Runs are scored
// sequentially at roughly 40s a sample, so this clears a few hundred samples
// before it ever fires.
const waitBudget = 2 * time.Hour

// pollRun waits for the run to reach a terminal state, reporting status changes.
func (ec *evalContext) pollRun(
	ctx context.Context,
	evalID, runID string,
	out interface{ Write([]byte) (int, error) },
	jsonMode bool,
) (*eval_api.OpenAIEvalRun, error) {
	const interval = 5 * time.Second
	lastStatus := ""

	// The budget has to bound the requests as well as the gaps between them. It
	// was checked only between polls, and the data-plane client carries no
	// deadline of its own, so a single stalled response held `--wait` open with
	// nothing left to stop it -- which is the one thing a bounded wait promises
	// not to do.
	waitCtx, cancel := context.WithTimeout(ctx, waitBudget)
	defer cancel()

	for {
		run, err := ec.evalClient.GetOpenAIEvalRun(waitCtx, evalID, runID)
		if err != nil {
			if stopped := waitStopped(ctx, waitCtx, runID); stopped != nil {
				return nil, stopped
			}
			return nil, messages.PollingRun(runID, err)
		}
		if run.Status != lastStatus {
			lastStatus = run.Status
			if !jsonMode {
				fmt.Fprint(out, messages.RunStatusLine(run.Status))
			}
		}
		// Before the budget, so a run that reached its end as the budget ran out
		// is reported as finished rather than as abandoned.
		if terminalRunStates[strings.ToLower(run.Status)] {
			return run, nil
		}
		select {
		case <-waitCtx.Done():
			if stopped := waitStopped(ctx, waitCtx, runID); stopped != nil {
				return nil, stopped
			}
			return nil, messages.WaitInterrupted(runID, waitCtx.Err())
		case <-time.After(interval):
		}
	}
}

// waitStopped tells the budget running out from the caller giving up, or
// reports that neither happened.
//
// The caller is asked first: cancelling during the last seconds of the budget
// is an interruption, and calling it a spent budget would hand back a reattach
// line for a wait the reader had already abandoned.
func waitStopped(caller, wait context.Context, runID string) error {
	if caller.Err() != nil {
		return messages.WaitInterrupted(runID, caller.Err())
	}
	if errors.Is(wait.Err(), context.DeadlineExceeded) {
		return errWaitBudgetSpent
	}
	return nil
}

// startedRunHandoff is what `run start --no-wait -o json` returns.
//
// It is a handoff rather than a dump of the service object. The pipeline that
// started the run has to come back for it later, and doing that needs exactly
// three things: the run, the eval it belongs to, and a name a human can read
// in the log that reports it. The service object carries none of the third and
// buries the first two under the data source, the metadata and every field the
// API happens to return, so a script reading it would depend on a shape this
// extension does not control.
type startedRunHandoff struct {
	RunID    string `json:"run_id"`
	EvalID   string `json:"eval_id"`
	EvalName string `json:"eval_name,omitempty"`
	// Which rows the run scored. A pipeline that records only a pass rate
	// cannot say later what the rate was measured against.
	Dataset        string `json:"dataset,omitempty"`
	DatasetVersion string `json:"dataset_version,omitempty"`
	Status         string `json:"status,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
}

// startedRun builds the handoff.
func startedRun(
	run *eval_api.OpenAIEvalRun,
	evalID string,
	group *project.Eval,
) startedRunHandoff {
	handoff := startedRunHandoff{
		RunID:     run.ID,
		EvalID:    evalID,
		Status:    run.Status,
		CreatedAt: timestampString(run.CreatedAt),
	}
	// Read back from the run rather than the configuration, so the handoff
	// names what this run scored and not what the file says today. The create
	// response does not always echo metadata, so the declaration is the
	// fallback for the name.
	handoff.Dataset = run.Metadata[metaDataset]
	handoff.DatasetVersion = run.Metadata[metaDatasetVersion]
	// Absent with --eval-id, where there is no config to take a name from.
	if group != nil {
		handoff.EvalName = group.Name
		if handoff.Dataset == "" {
			handoff.Dataset = group.Dataset
		}
	}
	return handoff
}

// timestampString renders a service timestamp as RFC 3339.
//
// The field arrives as epoch seconds on a run and as a formatted string
// elsewhere, so passing it through would hand a script a value whose type
// depends on which route produced it.
func timestampString(value any) string {
	switch t := value.(type) {
	case nil:
		return ""
	case string:
		// Normalized, not passed through: the service returns sub-second
		// precision and an offset here and epoch seconds elsewhere, so two
		// listings would otherwise spell the same instant differently.
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
		return t
	case float64:
		return time.Unix(int64(t), 0).UTC().Format(time.RFC3339)
	case int64:
		return time.Unix(t, 0).UTC().Format(time.RFC3339)
	case json.Number:
		if seconds, err := t.Int64(); err == nil {
			return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
		}
		return t.String()
	default:
		return fmt.Sprint(value)
	}
}

type runOutputSummary struct {
	means         map[string]float64
	conversations *conversationOutputSummary
}

// runOutputSummary uses a complete row listing for mean scores and observed
// conversation output. Neither is a projection of a single page.
//
// Best effort: the summary is worth printing without the column, and a run
// that scored nothing has no rows to read.
func (ec *evalContext) runOutputSummary(
	ctx context.Context,
	evalID string,
	run *eval_api.OpenAIEvalRun,
) *runOutputSummary {
	if run == nil || run.ResultCounts == nil || run.ResultCounts.Total == 0 {
		return nil
	}
	items, err := ec.evalClient.ListOutputItems(ctx, evalID, run.ID, 0)
	if err != nil || items == nil {
		if isSimulationRun(run) {
			return &runOutputSummary{conversations: &conversationOutputSummary{}}
		}
		return nil
	}
	summary := &runOutputSummary{means: criteriaMeans(items.Data)}
	if isSimulationRun(run) {
		summary.conversations = summarizeConversationOutput(items.Data)
	}
	return summary
}

// timestampTime reads a service timestamp, which arrives as epoch seconds on a
// run and as a formatted string elsewhere.
func timestampTime(value any) time.Time {
	switch t := value.(type) {
	case float64:
		return time.Unix(int64(t), 0).UTC()
	case int64:
		return time.Unix(t, 0).UTC()
	case string:
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

// renderRun prints what a person needs after waiting for a run.
//
// rows carries statistics from the complete output listing; it is nil when the
// rows were not fetched. Service generation counters remain separate.
func renderRun(
	out interface{ Write([]byte) (int, error) },
	run *eval_api.OpenAIEvalRun,
	rows *runOutputSummary,
) error {
	fmt.Fprintln(out)
	renderRunHeader(out, run)
	renderSimulationSettings(out, run)
	if isSimulationRun(run) && rows != nil && rows.conversations != nil {
		renderConversationOutput(out, rows.conversations)
	}

	// A run that failed carries why, and it is usually the only actionable
	// thing in the response — dropping it leaves the caller with just the word
	// "failed".
	renderRunFailure(out, run)

	// Counted over test cases, not over verdicts: a sample that failed two
	// evaluators is one sample to go and look at, and reporting it as two
	// overstates how much is wrong. The per-evaluator table below counts the
	// verdicts, and the two are labelled so they cannot be read as the same
	// number disagreeing with itself.
	if isSimulationRun(run) {
		renderConversationResults(out, run)
	} else if c := run.ResultCounts; c != nil && c.Total > 0 {
		errored, skipped := unscoredSplit(c, c.Passed+c.Failed)
		rate, _, scored := scoredPassRate(c)
		fmt.Fprint(out, messages.TestCaseResults(
			c.Total, c.Passed, c.Failed, errored, skipped,
			passRateText(rate, scored)))
	}

	var means map[string]float64
	if rows != nil {
		means = rows.means
	}
	renderCriteriaTable(out, run.PerTestingCriteria, means)

	renderRunFollowUp(out, run)

	writePortalLink(out, runLink(run.ReportURL, run.PortalURL))
	return nil
}

// runForDisplay fills identities from the successful lookup without changing
// the service object emitted under --output json.
func runForDisplay(run *eval_api.OpenAIEvalRun, evalID, runID string) *eval_api.OpenAIEvalRun {
	display := *run
	if display.EvalID == "" {
		display.EvalID = evalID
	}
	if display.ID == "" {
		display.ID = runID
	}
	return &display
}

func runFailureMessage(run *eval_api.OpenAIEvalRun) string {
	if why := run.Failure(); why != "" {
		return why
	}
	if run.Error != nil {
		return strings.TrimSpace(run.Error.Code)
	}
	return ""
}

func renderRunFailure(out io.Writer, run *eval_api.OpenAIEvalRun) {
	if why := runFailureMessage(run); why != "" {
		fmt.Fprintf(out, "\n%s\n", urlsafe.Text(why))
	}
}

func renderRunFollowUp(out io.Writer, run *eval_api.OpenAIEvalRun) {
	status := strings.ToLower(run.Status)
	operationalFailure := status == "failed" || status == "error" || runFailureMessage(run) != ""
	failed, errored := false, false
	if counts := run.ResultCounts; counts != nil {
		failed = counts.Failed > 0
		unscored, _ := unscoredSplit(counts, counts.Passed+counts.Failed)
		errored = counts.Errored > 0 || unscored > 0
	}
	if !terminalRunStates[status] && !operationalFailure && !failed && !errored {
		return
	}
	eval := followUpEvalRef(run)
	if eval == "" || run.ID == "" {
		fmt.Fprint(out, messages.RunFollowUpMissingIDs())
		return
	}
	if operationalFailure {
		fmt.Fprint(out, messages.FailedRunFollowUp(eval, run.ID, failed, errored))
		return
	}
	fmt.Fprint(out, messages.RunFollowUp(eval, run.ID, failed, errored))
}

// followUpEvalRef names the eval in the commands a finished run suggests.
//
// The immutable ID wins because a declared name can resolve to another eval
// after a redeploy. Friendly names remain in the header, and are a fallback
// only when neither the service nor the successful lookup provided an ID.
func followUpEvalRef(run *eval_api.OpenAIEvalRun) string {
	if run.EvalID != "" {
		return run.EvalID
	}
	return run.Metadata[metaEvalName]
}

// passRateText is the rate, or a dash where nothing was scored. A rate over no
// rows is not zero, it is absent.
func passRateText(rate float64, scored bool) string {
	if !scored {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", rate*100)
}

// renderRunHeader prints the run's identity above the per-evaluator table.
//
// The eval is named from the metadata the extension wrote at create time,
// because the run carries only an id and the id is not what anyone declared.
func renderRunHeader(out interface{ Write([]byte) (int, error) }, run *eval_api.OpenAIEvalRun) {
	fmt.Fprintf(out, "%-10s %s\n", "Run", run.ID)
	if name := run.Metadata[metaEvalName]; name != "" {
		fmt.Fprintf(out, "%-10s %s\n", "Eval", name)
	} else if run.EvalID != "" {
		fmt.Fprintf(out, "%-10s %s\n", "Eval", run.EvalID)
	}
	if ds := runDatasetLine(run.Metadata); ds != "" {
		fmt.Fprintf(out, "%-10s %s\n", "Dataset", ds)
	}
	if isSimulationRun(run) {
		fmt.Fprintf(out, "%-10s %s\n", "Mode", "conversation simulation")
		if run.Name != "" {
			fmt.Fprintf(out, "%-10s %s\n", "Name", run.Name)
		}
	}
	status := run.Status
	if isSimulationRun(run) {
		status = reportedStatus(status)
	}
	fmt.Fprintf(out, "%-10s %s\n", "Status", status)
	if d := runDuration(run); d != "" {
		fmt.Fprintf(out, "%-10s %s\n", "Duration", d)
	}
}

// runDuration reports how long the run took, or "" when either end is missing.
func runDuration(run *eval_api.OpenAIEvalRun) string {
	start, end := timestampTime(run.CreatedAt), timestampTime(run.ModifiedAt)
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return ""
	}
	d := end.Sub(start).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// renderCriteriaTable prints one row per evaluator.
//
// Sorted by name so two runs of the same eval read the same way; the service
// returns the criteria in whatever order it evaluated them.
func renderCriteriaTable(
	out interface{ Write([]byte) (int, error) },
	results []eval_api.EvalRunCriteriaResult,
	means map[string]float64,
) {
	if len(results) == 0 {
		return
	}

	sorted := append([]eval_api.EvalRunCriteriaResult(nil), results...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].TestingCriteria < sorted[j].TestingCriteria
	})

	width := len("EVALUATOR")
	for _, r := range sorted {
		if n := len(r.TestingCriteria); n > width {
			width = n
		}
	}

	fmt.Fprint(out, messages.EvaluatorResultsHeading())
	fmt.Fprintf(out, "%-*s  %4s  %4s  %4s  %5s  %7s  %9s",
		width, "EVALUATOR", "PASS", "FAIL", "SKIP", "ERROR", "SCORED", "PASS RATE")
	fmt.Fprintf(out, "%s\n", meanHeader(means))
	fmt.Fprintf(out, "%s  %s  %s  %s  %s  %s  %s%s\n",
		strings.Repeat("-", width), "----", "----", "----", "-----", "-------",
		"---------", meanRule(means))

	for _, r := range sorted {
		scored := r.Passed + r.Failed
		// Errors are not failures -- the evaluator never reached a verdict --
		// and a skip is not an error. Each has its own column, because folding
		// either into FAIL reports a service problem as a quality problem.
		fmt.Fprintf(out, "%-*s  %4d  %4d  %4d  %5d  %7s  %9s",
			width, r.TestingCriteria, r.Passed, r.Failed, r.Skipped, r.Errored,
			fmt.Sprintf("%d/%d", scored, scored+r.Skipped+r.Errored),
			formatRate(r.Passed, scored))
		if means != nil {
			if mean, ok := means[r.TestingCriteria]; ok {
				fmt.Fprintf(out, "  %10.1f", mean)
			} else {
				fmt.Fprintf(out, "  %10s", "-")
			}
		}
		fmt.Fprintln(out)
	}
}

// meanHeader and meanRule add the score column only when there are scores.
func meanHeader(means map[string]float64) string {
	if means == nil {
		return ""
	}
	return fmt.Sprintf("  %10s", "MEAN SCORE")
}

func meanRule(means map[string]float64) string {
	if means == nil {
		return ""
	}
	return "  " + strings.Repeat("-", 10)
}

// criteriaMeans averages each evaluator's score over the rows it scored.
//
// The run summary reports pass and fail counts but no score, so a table that
// shows how close a passing evaluator came to failing has to read the rows.
// Errored and unscored rows are left out rather than counted as zero, which
// would drag the average toward a number no evaluator produced.
func criteriaMeans(items []eval_api.OutputItem) map[string]float64 {
	sums := map[string]float64{}
	counts := map[string]int{}
	for _, item := range items {
		for _, r := range item.Results {
			if !r.Score.Defined() {
				continue
			}
			name := r.Name
			if name == "" {
				name = r.Metric
			}
			sums[name] += float64(r.Score)
			counts[name]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	means := make(map[string]float64, len(counts))
	for name, n := range counts {
		means[name] = sums[name] / float64(n)
	}
	return means
}

// formatRate renders a share as a percentage, and a rate over nothing as a
// dash: 0.0%% would read as a total failure rather than as no data.
func formatRate(part, whole int) string {
	if whole <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", float64(part)/float64(whole)*100)
}
