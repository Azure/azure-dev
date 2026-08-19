// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

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
func buildRunCommand(use, short string) *cobra.Command {
	var (
		groupName   string
		datasetName string
		runName     string
		maxSamples  int
		wait        bool
		failOn      string
		endpointFlg string
		evalPath    string
	)

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			// Parsed before any network work, so a malformed threshold costs
			// nothing to find out about.
			threshold, err := parseGate(failOn)
			if err != nil {
				return err
			}
			// A gate is a verdict on a result. Returning before there is one
			// used to drop the gate silently, so `--no-wait --fail-on ...`
			// exited 0 however the run turned out -- a pipeline that believes
			// it is gated and is not.
			if !wait && threshold.set {
				return messages.GateNeedsTheWait()
			}
			// resolveMaxSamples reads anything not above zero as "no cap", so a
			// negative one sent the whole dataset to a billed run.
			if maxSamples < 0 {
				return messages.NegativeMaxSamplesFlag(maxSamples)
			}

			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			// One flag takes a name or an id. A declared name also brings the
			// declaration, which is what says where rows come from; a bare id
			// has none, so the pairing comes from the eval's previous run.
			evalDir, err := ec.evalDir(ctx, evalPath)
			if err != nil {
				return err
			}
			ref, err := ec.resolveEvalRef(ctx, evalDir, chooseEvalIn(cmd, evalDir, groupName))
			if err != nil {
				return err
			}
			evalID := ref.ID
			group := ref.Eval
			configPath := ref.ConfigPath

			if datasetName != "" {
				if !ref.Declared() {
					return messages.DatasetOverrideNeedsDeclaredEval()
				}
				if _, ok := ref.Config.DatasetDeclaration(datasetName); !ok {
					return messages.DatasetNotInCatalog(
						datasetName, filepath.ToSlash(configPath))
				}
				// The eval keeps its own declaration; only this run reads elsewhere.
				overridden := *group
				overridden.Dataset = datasetName
				overridden.Source = nil
				group = &overridden
			}

			if ref.Declared() {
				if err := ec.checkDatasetRegistered(ctx, ref.Config, group, configPath); err != nil {
					return err
				}
			}

			var dataSource *eval_api.EvalRunDataSource
			switch {
			case group == nil:
				dataSource, err = ec.reuseDataSourceFromLastRun(ctx, evalID)
			default:
				dataSource, err = ec.buildRunDataSource(
					ctx, group, configPath, resolveMaxSamples(maxSamples, group))
			}
			if err != nil {
				return err
			}

			if runName == "" {
				base := "eval"
				if group != nil {
					base = group.Name
				}
				runName = fmt.Sprintf("%s-%s", base, time.Now().UTC().Format("20060102-150405"))
			}

			metadata := map[string]string{}
			if lvl := resolveLevel(group); lvl != "" {
				metadata["evaluation_level"] = lvl
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
				if v := ec.scoredDatasetVersion(ctx, group, configPath); v != "" {
					metadata[metaDatasetVersion] = v
				}
			}

			run, err := ec.evalClient.CreateOpenAIEvalRun(ctx, evalID, &eval_api.CreateOpenAIEvalRunRequest{
				Name:       runName,
				DataSource: dataSource,
				Metadata:   metadata,
			})
			if err != nil {
				return messages.StartingRun(err)
			}

			// Remembered per group as well as globally: a single shared key
			// belongs to whichever group ran last, so another group asking for
			// "the last run" would be handed one that is not its own.
			ec.remember(ctx, idKey("evalrun", evalID), run.ID)
			if err := ec.setEnvValue(ctx, envKeyEvalRunID, run.ID); err != nil {
				// Persisting the run id is a convenience for later commands.
				// Reported on stdout because azd does not surface an
				// extension's stderr under `azd up`, which is where a deploy
				// would lose it. Skipped outside a project.
				if !errors.Is(err, errNoAzdEnvironment) && !isJSON(cmd) {
					fmt.Fprint(out, messages.Warning(err))
				}
			}

			if !wait {
				if isJSON(cmd) {
					return emitJSON(out, startedRun(run, evalID, group))
				}
				fmt.Fprint(out, messages.RunStarted(run.ID, run.Status))
				fmt.Fprint(out, messages.ReattachToRun(run.ID, evalID))
				return nil
			}

			final, err := ec.pollRun(ctx, evalID, run.ID, out, isJSON(cmd))
			if errors.Is(err, errWaitBudgetSpent) {
				// A gate asked for a verdict that never arrived. Exiting 0 here
				// would tell a pipeline the gate passed, which is the silent
				// drop --no-wait is refused for, reached by running long.
				if threshold.set {
					return messages.GateOutlivedTheWait(run.ID, waitBudget)
				}
				// The run did not fail, the wait ran out. Same contract as
				// --no-wait: exit 0 and say how to pick it back up.
				if isJSON(cmd) {
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

			if isJSON(cmd) {
				if err := emitJSON(out, final); err != nil {
					return err
				}
			} else if err := renderRun(out, final, ec.runMeans(ctx, evalID, final)); err != nil {
				return err
			}

			// Last, so that the results are reported whether or not the gate
			// holds: a pipeline that only learns it failed is worse off than
			// one that can see by how much.
			if err := runCompleted(final); err != nil {
				return err
			}
			applyGate(cmd, threshold, final)
			return nil
		},
	}

	cmd.Flags().StringVar(&groupName, "eval", "",
		"Name of the eval to run, or its id. Defaults to the only one declared.")
	cmd.Flags().StringVar(&datasetName, "dataset", "",
		"Catalog dataset to read instead of the one the eval declares. "+
			"Must satisfy the eval's column schema.")
	cmd.Flags().StringVar(&runName, "name", "", "Name for this run. Defaults to the eval name plus a timestamp.")
	cmd.Flags().IntVar(&maxSamples, "max-samples", 0,
		"Cap the rows sent from the dataset.")
	cmd.Flags().BoolVar(&wait, "wait", true, "Block until the run reaches a terminal state.")
	addFailOnFlag(cmd, &failOn)
	// The spec documents --no-wait, and cobra does not derive it from a bool.
	var noWait bool
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Submit the run and return immediately.")
	cmd.PreRun = func(*cobra.Command, []string) {
		if noWait {
			wait = false
		}
	}
	cmd.MarkFlagsMutuallyExclusive("wait", "no-wait")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	addEvalPathFlag(cmd, &evalPath)

	return cmd
}

// checkDatasetRegistered fails when the group's local dataset has edits that
// were never deployed.
//
// A run sends a local dataset inline, so without this the run would evaluate
// content that no registered version corresponds to: the results are attributed
// to the eval but cannot be traced back to a dataset version, which
// makes them impossible to reproduce or compare.
//
// The check only applies once a deploy has recorded a fingerprint. Before that
// there is nothing to have drifted from, and running is how a group first comes
// into existence.
func (ec *evalContext) checkDatasetRegistered(
	ctx context.Context,
	cfg *project.EvalConfig,
	group *project.Eval,
	configPath string,
) error {
	localPath := localDatasetPath(configPath, group)
	if localPath == "" {
		return nil
	}

	decl, ok := cfg.DatasetDeclaration(group.Dataset)
	if !ok {
		return nil
	}

	recorded := ec.getEnvValue(ctx, project.FingerprintKey("dataset", decl.Name))
	if recorded == "" {
		return nil
	}

	digest, err := project.Fingerprint(localPath)
	if err != nil {
		// Reading the file is the run's problem to report, not this check's.
		return nil
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
func (ec *evalContext) reuseDataSourceFromLastRun(
	ctx context.Context,
	evalID string,
) (*eval_api.EvalRunDataSource, error) {
	list, err := ec.evalClient.ListOpenAIEvalRuns(ctx, evalID, 1)
	if err != nil {
		if eval_api.IsNotFound(err) {
			// The eval itself is missing, which is worth saying plainly rather
			// than as forty lines of the 404 that discovered it.
			return nil, messages.EvalNotFound(evalID)
		}
		return nil, messages.ReadingPreviousRuns(evalID, err)
	}
	if list == nil || len(list.Data) == 0 || list.Data[0].DataSource == nil {
		return nil, messages.EvalHasNoPreviousRun(evalID)
	}
	return pinReusedTraceWindow(list.Data[0].DataSource), nil
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

// buildRunDataSource binds the eval's rows to the run.
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
) (*eval_api.EvalRunDataSource, error) {
	if group == nil {
		return nil, messages.NoEvalToRun()
	}
	if err := runnableEval(group); err != nil {
		return nil, err
	}
	if group.Dataset != "" && configPath != "" && !datasetIsDeclared(configPath, group) {
		return nil, messages.InEval(group.Name, messages.DatasetNotDeclared(group.Dataset))
	}

	if group.Source != nil {
		switch group.Source.Type {
		case project.SourceTypeTraces:
			return tracesDataSource(group)
		default:
			return responsesDataSource(group)
		}
	}

	var ds *eval_api.EvalRunDataSource
	switch {
	case group.Target == nil || group.Target.Name == "":
		// Nothing to invoke: the dataset is scored as it stands.
		ds = eval_api.NewDatasetOnlyDataSource()
	case group.Target.Type == project.TargetTypeModel:
		ds = eval_api.NewModelTargetDataSource(group.Target.Name)
	default:
		ds = eval_api.NewAgentTargetDataSource(group.Target.Name, nil)
	}

	if group.Dataset == "" {
		return nil, messages.EvalHasNoDataset(group.Name)
	}

	// A local source is read from disk; anything else is already registered and
	// has to be fetched. Either way the rows are sent inline, because a run's
	// file_id means an uploaded file and a dataset name is not one: sending the
	// name is rejected with "invalid data source file ids".
	localPath := localDatasetPath(configPath, group)
	if localPath == "" {
		items, err := ec.readRegisteredDataset(
			ctx, group.Dataset, declaredDatasetVersion(configPath, group), maxSamples)
		if err != nil {
			return nil, err
		}
		ds.SetFileContent(items)
		return ds, nil
	}

	items, err := readJSONL(localPath, maxSamples)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, messages.DatasetFileEmpty(localPath)
	}
	ds.SetFileContent(items)
	return ds, nil
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

// readRegisteredDataset fetches a published dataset's rows, optionally keeping
// only the first n.
//
// The rows have to be fetched because a run cannot reference a dataset by
// name: `file_id` means an uploaded file, and passing a dataset name there is
// rejected. Fetching also makes --max-samples mean the same thing whether the
// dataset is local or published, which a file reference could not — that
// source carries no row limit.
func (ec *evalContext) readRegisteredDataset(
	ctx context.Context,
	name string,
	pinned string,
	maxSamples int,
) ([]map[string]any, error) {
	// The declaration wins: it is the author saying which rows to score, and it
	// is right whether or not there is an azd environment to have recorded one.
	version := pinned
	if version == "" {
		version = ec.getEnvValue(ctx, versionKey("dataset", name))
	}
	if version == "" {
		versions, err := ec.datasetClient.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
		if err != nil {
			return nil, messages.ReadingDataset(name, err)
		}
		if versions != nil {
			version = dataset_api.LatestVersion(versions.Value)
		}
	}
	if version == "" {
		return nil, messages.DatasetHasNoVersionsToRead(name)
	}

	content, err := ec.datasetClient.DownloadDatasetContent(
		ctx, name, version, ProjectEndpointAPIVersion)
	if err != nil {
		return nil, messages.ReadingDatasetVersion(name, version, err)
	}

	items, err := readJSONLBytes(content, maxSamples)
	if err != nil {
		return nil, messages.ReadingDatasetVersion(name, version, err)
	}
	if len(items) == 0 {
		return nil, messages.DatasetVersionEmpty(name, version)
	}
	return items, nil
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

// localDatasetPath resolves the dataset's local source relative to the config
// file, returning empty when the dataset is registered rather than local.
func localDatasetPath(configPath string, group *project.Eval) string {
	cfg, err := project.LoadEvalConfig(configPath)
	if err != nil || group == nil {
		return ""
	}
	decl, ok := cfg.DatasetDeclaration(group.Dataset)
	if !ok || decl.Source == "" {
		return ""
	}
	if filepath.IsAbs(decl.Source) {
		return decl.Source
	}
	return filepath.Join(filepath.Dir(configPath), decl.Source)
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

// scoredDatasetVersion labels a run with the version its rows actually came
// from, or with nothing when that cannot be said.
//
// It has to follow the same branch the rows did. A declaration carrying both
// `source:` and `version:` reads the file from disk, so the pin says nothing
// about what was scored. The recorded version is the honest label there only
// once a fingerprint exists to tie the file to it -- that is what
// checkDatasetRegistered confirms, and it also declines when there is none. A
// dataset that was registered and has since gained a `source:` has a recorded
// version and no fingerprint, and stamping the run with it would assert a
// provenance the rows no longer have.
func (ec *evalContext) scoredDatasetVersion(
	ctx context.Context,
	group *project.Eval,
	configPath string,
) string {
	if localDatasetPath(configPath, group) != "" {
		if ec.getEnvValue(ctx, project.FingerprintKey("dataset", group.Dataset)) == "" {
			return ""
		}
		return ec.getEnvValue(ctx, versionKey("dataset", group.Dataset))
	}
	if pinned := declaredDatasetVersion(configPath, group); pinned != "" {
		return pinned
	}
	return ec.getEnvValue(ctx, versionKey("dataset", group.Dataset))
}

// datasetIsDeclared says whether the configuration's catalog holds the dataset
// this eval names.
//
// Without it a mistyped name falls through to a registry read and comes back as
// a 404 for a dataset nobody ever registered, which sends the reader to the
// service rather than to the line they mistyped. Answered yes when there is no
// configuration to ask: an eval reached by id has no catalog.
func datasetIsDeclared(configPath string, group *project.Eval) bool {
	cfg, err := project.LoadEvalConfig(configPath)
	if err != nil || cfg == nil {
		return true
	}
	_, ok := cfg.DatasetDeclaration(group.Dataset)
	return ok
}

// readJSONL reads newline-delimited JSON, optionally truncating to limit rows.
func readJSONL(path string, limit int) ([]map[string]any, error) {
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

// readJSONLBytes parses JSONL already in memory, which is how a registered
// dataset arrives.
func readJSONLBytes(content []byte, limit int) ([]map[string]any, error) {
	return scanJSONL(bytes.NewReader(content), limit)
}

// scanJSONL reads rows until the limit is reached, so a subset costs only the
// rows it needs to parse.
func scanJSONL(r io.Reader, limit int) ([]map[string]any, error) {
	var items []map[string]any
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
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
	deadline := time.Now().Add(waitBudget)

	for {
		run, err := ec.evalClient.GetOpenAIEvalRun(ctx, evalID, runID)
		if err != nil {
			return nil, messages.PollingRun(runID, err)
		}
		if run.Status != lastStatus {
			lastStatus = run.Status
			if !jsonMode {
				fmt.Fprint(out, messages.RunStatusLine(run.Status))
			}
		}
		if terminalRunStates[strings.ToLower(run.Status)] {
			return run, nil
		}
		if time.Now().After(deadline) {
			return nil, errWaitBudgetSpent
		}
		select {
		case <-ctx.Done():
			// Name what is still running, or the run is lost to whoever
			// interrupted the wait.
			return nil, messages.WaitInterrupted(runID, ctx.Err())
		case <-time.After(interval):
		}
	}
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

// runMeans reads the run's rows to average each evaluator's score.
//
// Best effort: the summary is worth printing without the column, and a run
// that scored nothing has no rows to read.
func (ec *evalContext) runMeans(
	ctx context.Context,
	evalID string,
	run *eval_api.OpenAIEvalRun,
) map[string]float64 {
	if run == nil || run.ResultCounts == nil || run.ResultCounts.Total == 0 {
		return nil
	}
	items, err := ec.evalClient.ListOutputItems(ctx, evalID, run.ID, 0)
	if err != nil || items == nil {
		return nil
	}
	return criteriaMeans(items.Data)
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
// means carries each criterion's average score, which the run summary does not
// return; it is nil when the rows were not fetched, and the column is dropped.
func renderRun(
	out interface{ Write([]byte) (int, error) },
	run *eval_api.OpenAIEvalRun,
	means map[string]float64,
) error {
	fmt.Fprintln(out)
	renderRunHeader(out, run)

	// A run that failed carries why, and it is usually the only actionable
	// thing in the response — dropping it leaves the caller with just the word
	// "failed".
	if why := run.Failure(); why != "" {
		fmt.Fprintf(out, "\n%s\n", why)
	}

	renderCriteriaTable(out, run.PerTestingCriteria, means)

	// Counted over samples, not over verdicts: a sample that failed two
	// evaluators is one sample to go and look at, and reporting it as two
	// overstates how much is wrong.
	if c := run.ResultCounts; c != nil && c.Total > 0 {
		if rate, scored, ok := scoredPassRate(c); ok {
			fmt.Fprint(out, messages.OverallPassRate(
				fmt.Sprintf("%.1f%%", rate*100), c.Passed, scored, c.Total-scored))
		}
		if c.Errored > 0 {
			fmt.Fprint(out, messages.SamplesErrored(c.Errored))
		}
		if c.Failed > 0 {
			fmt.Fprint(out, messages.ViewFailingSamples())
		}
	}

	writePortalLink(out, runLink(run.ReportURL, run.PortalURL))
	return nil
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
	fmt.Fprintf(out, "%-10s %s\n", "Status", run.Status)
	if c := run.ResultCounts; c != nil && c.Total > 0 {
		fmt.Fprintf(out, "%-10s %d\n", "Samples", c.Total)
	}
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

	fmt.Fprintf(out, "\n%-*s  %4s  %4s  %9s", width, "EVALUATOR", "PASS", "FAIL", "PASS RATE")
	fmt.Fprintf(out, "%s\n", meanHeader(means))
	fmt.Fprintf(out, "%s  %s  %s  %s%s\n",
		strings.Repeat("-", width), "----", "----", "---------", meanRule(means))

	for _, r := range sorted {
		scored := r.Passed + r.Failed
		fmt.Fprintf(out, "%-*s  %4d  %4d  %9s",
			width, r.TestingCriteria, r.Passed, r.Failed, formatRate(r.Passed, scored))
		if means != nil {
			if mean, ok := means[r.TestingCriteria]; ok {
				fmt.Fprintf(out, "  %10.1f", mean)
			} else {
				fmt.Fprintf(out, "  %10s", "-")
			}
		}
		fmt.Fprintln(out)
		// Errors are not failures — the evaluator never reached a verdict —
		// so they are named rather than folded into the fail column, where
		// they would look like a quality problem.
		if r.Errored > 0 {
			fmt.Fprintf(out, "%-*s  %s\n", width, "", errorNote(r.Errored))
		}
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

// errorNote describes rows an evaluator could not score.
func errorNote(errored int) string {
	return messages.ErroredNotScored(errored)
}

// formatRate renders a share as a percentage, and a rate over nothing as a
// dash: 0.0%% would read as a total failure rather than as no data.
func formatRate(part, whole int) string {
	if whole <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", float64(part)/float64(whole)*100)
}
