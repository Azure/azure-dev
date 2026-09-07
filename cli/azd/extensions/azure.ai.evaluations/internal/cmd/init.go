// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
)

// Data sources `init` can point an eval at.
const (
	initSourceDataset = "dataset"
	initSourceTraces  = "traces"
)

// newInitCommand scaffolds the eval configuration. It makes no service calls at
// all, so it works offline and unauthenticated.
//
// It only ever adds. A name already declared is refused rather than
// overwritten, because the settings a reader tunes by hand — thresholds, judge
// model, data mapping — live nowhere but that entry and `init` cannot
// reproduce them. Editing an eval is a file edit.
// initFlags carries what `init` was asked for.
type initFlags struct {
	evalName        string
	target          string
	source          string
	dataset         string
	maxTraces       int
	traceDays       int
	evaluationLevel string
	evaluators      []string
	judgeModel      string
	path            string
}

// initAction scaffolds the eval configuration.
//
// Much of what it needs is settled rather than supplied -- a target is
// detected, a judge model is read off the project, evaluators are chosen from
// a list -- so Run derives its own locals from the flags. The flags stay what
// the caller typed.
type initAction struct {
	cmd   *cobra.Command
	flags *initFlags
}

func newInitCommand() *cobra.Command {
	flags := &initFlags{}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold evaluation config for an agent. Makes no service calls.",
		// Everything init takes is a flag; a positional would be ignored.
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&initAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.evalName, "name", "",
		"Name of the eval. Defaults to <target>-dataset-eval, or <target>-trace-eval "+
			"under --source traces, numbered when that name is taken.")
	cmd.Flags().StringVar(&flags.target, "target", "",
		"Name of the agent to evaluate. Detected when the project has one agent; prompts when it has several.")
	cmd.Flags().StringVar(&flags.source, "source", "",
		"Where rows come from: dataset or traces. Defaults to traces when the azd "+
			"environment records an Application Insights connection, otherwise dataset.")
	cmd.Flags().StringVar(&flags.dataset, "dataset", "",
		"Path to a local .jsonl, or the name of a registered dataset.")
	cmd.Flags().IntVar(&flags.maxTraces, "max-traces", project.DefaultScaffoldMaxTraces,
		"Cap on traces read by a --source traces eval. Delete max_traces from the "+
			"file to take the service default instead.")
	cmd.Flags().IntVar(&flags.traceDays, "trace-days", defaultTraceWindowDays,
		"How far back a --source traces eval reads: 1, 7, or 30 days. Set "+
			"lookback_hours in the file for any other window.")
	cmd.Flags().StringVar(&flags.evaluationLevel, "evaluation-level", "",
		"What one evaluated sample is: turn for a single request and response, "+
			"conversation for the whole multi-turn interaction. Defaults to turn.")
	cmd.Flags().StringSliceVar(&flags.evaluators, "evaluator", nil,
		"Evaluator reference, repeatable and comma-separated. Use builtin.<name> for a "+
			"built-in. Passing this replaces the defaults, so it also opts out of rubric generation.")
	cmd.Flags().StringVar(&flags.judgeModel, "judge-model", "",
		"Model deployment the graders judge with. Detected from the project when omitted.")
	cmd.Flags().StringVar(&flags.path, "path", "",
		"Directory to write the configuration into. Used verbatim, never re-rooted. "+
			"Defaults to the directory an earlier `init` scaffolded, otherwise ./evals.")
	return cmd
}

func (a *initAction) Run() error {
	out := a.cmd.OutOrStdout()

	source := a.flags.source
	switch source {
	case "", initSourceDataset, initSourceTraces:
	default:
		return messages.SourceNotADataSource(
			source, initSourceDataset, initSourceTraces)
	}
	if source == initSourceTraces && a.flags.dataset != "" {
		return messages.TracesTakesNoDataset()
	}
	if a.flags.maxTraces < 0 {
		return messages.MaxTracesMustBePositive()
	}
	// Checked with the other flag-only rules, before anything is asked
	// or read: a reference that cannot name an evaluator is otherwise
	// written by a command that exits 0, and only fails two commands
	// later. Answering two prompts first to be told a flag was wrong is
	// the same defect one step removed.
	if err := validateEvaluatorRefs(a.flags.evaluators); err != nil {
		return err
	}
	// The same cascade every other command reads the configuration
	// through. init merges into the configuration it finds, so a second
	// `init` in a project scaffolded at ./quality has to find that one --
	// otherwise it writes a second configuration under ./evals and
	// declares a second service pointing at it.
	path, err := resolveEvalDir(a.cmd.Context(), a.flags.path)
	if err != nil {
		return err
	}

	// Asked before anything is written: the project is the one thing
	// init cannot supply for itself, and failing after creating
	// directories leaves a half-scaffolded tree behind.
	azdProject, err := readAzdProject(a.cmd.Context())
	if err != nil {
		return err
	}

	configPath, err := project.ResolveEvalConfigPath(path)
	if err != nil {
		return err
	}
	// Captured before the write: init merges into an existing config, so
	// reporting it as created would claim a file it only added to.
	_, configExistedErr := os.Stat(configPath)
	configExisted := configExistedErr == nil
	authored, err := project.ReadAuthoredConfig(path)
	if err != nil {
		return err
	}
	cfg := declaredSoFar(authored)
	evalDir := project.EvalDirOf(path)

	// From here to the lock the questions run in the order the spec asks
	// them, because each one changes what the next is about: the source
	// decides whether a dataset or a trace window is even a question, and
	// the target and the source together are what the name is derived from.
	//
	// Every one of them is a human pause, so all of them are outside the
	// lock. What they produce is a proposal; the configuration is read again
	// under the lock and the proposal validated against it.

	// Asked twice -- once to pick the default source, once to say so --
	// and each call opens an azd connection. The answer cannot change
	// mid-command, and a run that never asks never connects.
	tracesWired := sync.OnceValue(func() bool {
		return tracesConnected(commandContext(a.cmd))
	})
	ctx := initContext{
		cfg:           cfg,
		azdProject:    azdProject,
		evalDir:       evalDir,
		configPath:    configPath,
		configExisted: configExisted,
		tracesWired:   tracesWired,
	}

	answers, err := a.ask(ctx)
	if err != nil {
		return err
	}
	serviceName := answers.target + "-evals"
	wiring, err := planRootEvalService(a.cmd.Context(), serviceName, configPath)
	if err != nil {
		return err
	}
	for {
		decision, err := confirmScaffold(a.cmd, out, scaffoldSummary{
			answers:    answers,
			configPath: configPath,
			wiring:     wiring,
			maxTraces:  a.flags.maxTraces,
		})
		if err != nil {
			return err
		}
		if decision == scaffoldCancel {
			// Nothing has been written yet, which is the whole point of
			// asking here rather than after: cancelling leaves both files
			// exactly as they were.
			fmt.Fprint(out, messages.ScaffoldCancelled())
			return nil
		}
		if decision == scaffoldAdd {
			break
		}
		if answers, err = a.ask(ctx); err != nil {
			return err
		}
		serviceName = answers.target + "-evals"
	}

	source = answers.source
	target := answers.target
	judgeModel := answers.judgeModel
	evalName := answers.evalName
	datasetRef := answers.datasetRef
	evaluationLevel := answers.evaluationLevel
	evaluators := answers.evaluators
	evaluatorsWereChosen := answers.evaluatorsChosen
	lookbackHours := answers.lookbackHours

	// The read-modify-write starts here, and nothing inside it waits on
	// a person. The configuration is read again because the copy above
	// was taken before the prompt, and a `generate` may well have
	// finished writing to it since.
	unlockConfig, err := project.LockEvalConfig(a.cmd.Context(), path)
	if err != nil {
		return err
	}
	defer unlockConfig()

	authored, err = project.ReadAuthoredConfig(path)
	if err != nil {
		return err
	}
	cfg = declaredSoFar(authored)
	// Re-checked under the lock, because the name was settled against a copy
	// taken before the prompts and a `generate` may have written since.
	if cfg.HasEval(evalName) {
		return messages.EvalAlreadyDeclared(evalName, filepath.ToSlash(configPath))
	}

	// What the file already declares, so the write can be limited to what
	// planScaffold adds to it.
	declaredDatasets := len(cfg.Datasets)
	declaredEvaluators := len(cfg.Evaluators)
	declaredEvals := len(cfg.Evals)

	// The location may be the file azure.yaml names rather than the
	// directory holding it, and artifacts sit beside the configuration.
	if err := os.MkdirAll(filepath.Join(evalDir, project.DefaultDatasetsDir), 0o750); err != nil {
		return messages.CreatingDatasetsDir(err)
	}
	if err := os.MkdirAll(filepath.Join(evalDir, project.DefaultEvaluatorsDir), 0o750); err != nil {
		return messages.CreatingEvaluatorsDir(err)
	}

	plan, err := planScaffold(scaffoldInput{
		evalName:        evalName,
		target:          target,
		source:          source,
		dataset:         datasetRef,
		maxTraces:       a.flags.maxTraces,
		lookbackHours:   lookbackHours,
		evaluationLevel: evaluationLevel,
		evaluators:      evaluators,
		judgeModel:      judgeModel,
		evalDir:         evalDir,
		cfg:             cfg,
	})
	if err != nil {
		return err
	}

	if err := refuseDuplicateEval(path, plan.eval); err != nil {
		return err
	}

	if err := project.ApplyScaffold(path, project.ScaffoldWrite{
		Datasets:   cfg.Datasets[declaredDatasets:],
		Evaluators: cfg.Evaluators[declaredEvaluators:],
		Evals:      cfg.Evals[declaredEvals:],
	}); err != nil {
		return err
	}

	// Scaffolding a config azd cannot see is half a step: the eval
	// service has to be referenced from the root config before any of
	// `azd up`, `azd deploy` or `azd ai eval run` will act on it.
	rootWiring, err := ensureRootEvalService(a.cmd.Context(), serviceName, target, configPath)
	if err != nil {
		return err
	}

	if isJSON(a.cmd) {
		return emitJSON(out, map[string]any{
			"eval":          evalName,
			"evalConfig":    configPath,
			"service":       serviceName,
			"datasetsDir":   filepath.Join(evalDir, project.DefaultDatasetsDir),
			"evaluatorsDir": filepath.Join(evalDir, project.DefaultEvaluatorsDir),
			"rootConfig":    rootWiring,
			"target":        target,
			"source":        source,
			"judgeModel":    judgeModel,
			"evaluators":    plan.evaluatorNames(),
		})
	}

	fmt.Fprint(out, messages.DetectedTarget(target))
	if source == initSourceTraces {
		// Claiming the connection is only honest when it was found. init
		// makes no service calls, so it cannot verify one it did not see.
		fmt.Fprint(out, messages.UsingTraceSource(tracesWired()))
	}
	// Only what was settled without asking: a reader who just picked
	// from a list does not need it read back to them.
	if names := plan.evaluatorNames(); len(names) > 0 && !evaluatorsWereChosen {
		fmt.Fprint(out, messages.GradingWith(names))
	}
	if judgeModel != "" {
		fmt.Fprint(out, messages.JudgeModelDeployment(judgeModel))
	}

	fmt.Fprint(out, messages.ScaffoldHeading(configExisted))
	fmt.Fprint(out, messages.ScaffoldConfigLine(filepath.ToSlash(configPath), configExisted))
	switch rootWiring {
	case wiringAdded:
		fmt.Fprint(out, messages.AddedServiceLine(rootConfigName, serviceName))
	case wiringPresent:
		fmt.Fprint(out, messages.AlreadyDeclaresServiceLine(rootConfigName, serviceName))
	}

	// Only what was actually scheduled is offered. Suggesting
	// `dataset generate` for a dataset the caller supplied sends them
	// to submit a billed job for an artifact they already have.
	next := plan.nextSteps(deployCommandName(azdProject))
	fmt.Fprint(out, messages.FirstNextStep(next[0]))
	for _, step := range next[1:] {
		fmt.Fprint(out, messages.FurtherNextStep(step))
	}
	return nil
}

// settleInitSource returns the data source the eval will read, and refuses
// --max-traces when that source will not be traces.
//
// The defaulting and the rule live together because they were once apart, and
// disagreed: the rule ran on the flag as typed, so `init --max-traces 50` with
// no --source was refused for "not a trace source" even in a project wired for
// traces, where the very next line was about to choose traces. Reading the flag
// as its own request for traces would be the other way to fix it, but that
// silently overrides a project that has no traces to read; refusing after the
// source is known says the true thing.
//
// tracesWired is a function, not a value, so a run that was told its source
// never opens an azd connection to answer a question nobody asked.
// initSourceInput is what settling the data source depends on.
type initSourceInput struct {
	explicit       string
	maxTracesGiven bool
	traceDaysGiven bool
	// usableDatasets counts declarations this scaffold could point at, which
	// is what makes "dataset" a defensible default rather than a coin toss.
	usableDatasets int
	tracesWired    func() bool
}

// settleInitSource returns the data source the eval will read, and refuses the
// trace-only flags when that source will not be traces.
//
// The defaulting and the rules live together because they were once apart, and
// disagreed: the rule ran on the flag as typed, so `init --max-traces 50` with
// no --source was refused for "not a trace source" even in a project wired for
// traces, where the very next line was about to choose traces. Reading the flag
// as its own request for traces would be the other way to fix it, but that
// silently overrides a project that has no traces to read; refusing after the
// source is known says the true thing.
//
// Nothing here proves what exists remotely. A project with no telemetry marker
// may still have traces, and a declared dataset may have been deleted, so the
// two signals order the prompt rather than answer it.
//
// tracesWired is a function, not a value, so a run that was told its source
// never opens an azd connection to answer a question nobody asked.
func settleInitSource(cmd *cobra.Command, in initSourceInput) (string, error) {
	source := in.explicit
	if source == "" {
		var err error
		if source, err = chooseInitSource(cmd, in); err != nil {
			return "", err
		}
	}
	if in.maxTracesGiven && source != initSourceTraces {
		return "", messages.MaxTracesNeedsTraceSource()
	}
	if in.traceDaysGiven && source != initSourceTraces {
		return "", messages.TraceDaysNeedsTraceSource()
	}
	return source, nil
}

// chooseInitSource settles an unstated source, asking where it can.
func chooseInitSource(cmd *cobra.Command, in initSourceInput) (string, error) {
	// The same signal `generate --from` defaults on, read from the azd
	// environment rather than the service, so init still makes no service
	// calls. Traces are real conversations; a project wired to collect them
	// should not have to ask for them by flag.
	preferred := ""
	switch {
	case in.tracesWired():
		preferred = initSourceTraces
	case in.usableDatasets == 1:
		preferred = initSourceDataset
	}
	if noPrompt(cmd) {
		// With neither signal there is still nobody to ask, and dataset is
		// what the flag's own documentation promises. A dataset-backed
		// scaffold with nothing to point at then fails naming --dataset,
		// which is the actionable half of the same answer.
		return cmp.Or(preferred, initSourceDataset), nil
	}
	return promptInitSource(cmd, preferred)
}

// promptInitSource asks which rows the eval grades.
func promptInitSource(cmd *cobra.Command, preferred string) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	offered := []string{initSourceTraces, initSourceDataset}
	choices := make([]*azdext.SelectChoice, 0, len(offered))
	for _, name := range offered {
		choices = append(choices, &azdext.SelectChoice{
			Label: messages.DataSourceChoice(name),
			Value: name,
		})
	}

	options := &azdext.SelectOptions{
		Message: messages.SelectDataSourcePrompt(),
		Choices: choices,
	}
	// Left unset where neither signal fired: highlighting one of two sources
	// on no evidence is a recommendation the command cannot support.
	if i := slices.Index(offered, preferred); i >= 0 {
		options.SelectedIndex = preselect(i)
	}

	resp, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{Options: options})
	if err != nil {
		return "", messages.SelectingDataSource(err)
	}
	// Value is optional on the wire, so an unset one arrives as 0 from GetValue
	// and would read as a deliberate answer of "traces".
	if resp == nil || resp.Value == nil {
		return cmp.Or(preferred, initSourceDataset), nil
	}
	index := int(resp.GetValue())
	if index < 0 || index >= len(offered) {
		return cmp.Or(preferred, initSourceDataset), nil
	}
	return offered[index], nil
}

// usableDatasetCount counts declarations this scaffold could point at.
//
// A declaration whose local file is gone is not one of them: offering it as
// the reason to default to a dataset source would recommend the eval that
// cannot run.
func usableDatasetCount(cfg *project.EvalConfig, evalDir string) int {
	usable := 0
	for _, decl := range cfg.Datasets {
		if decl.File != "" {
			if _, err := os.Stat(filepath.Join(evalDir, filepath.FromSlash(decl.File))); err != nil {
				continue
			}
		}
		usable++
	}
	return usable
}

// refTo is the `$ref` value for a configuration at path.
//
// Relative paths get `./` so the directive reads as a path rather than a
// registry name. An absolute one already is a path, and prefixing it produced
// `.//tmp/evals/azure.eval.yaml`: `init` wrote the configuration where it was
// asked, and `azd up` then resolved something else under the project.
//
// configPath is relative to where the caller stood; a `$ref` is read relative
// to the directory holding azure.yaml. Written as the one and read as the
// other, `init` run from a subdirectory recorded `./evals/...` for a scaffold
// it had just written under that subdirectory, and `azd up` deployed a file
// that was never there.
func refTo(projectRoot, configPath string) string {
	if filepath.IsAbs(configPath) {
		return filepath.ToSlash(configPath)
	}
	rebased := filepath.ToSlash(relativeToRoot(projectRoot, configPath))
	if rebased == ".." || strings.HasPrefix(rebased, "../") {
		// Outside the project, but still resolved against the root, so `./`
		// would only be noise in front of it.
		return rebased
	}
	return "./" + rebased
}

// relativeToRoot restates a path given relative to the caller's directory as
// one relative to the project root.
//
// Falls back to the path as given when there is no root to rebase onto or the
// two share no common base, which is what this did before and is better than
// resolving against nothing.
func relativeToRoot(projectRoot, configPath string) string {
	if projectRoot == "" {
		return configPath
	}
	absolute, err := filepath.Abs(configPath)
	if err != nil {
		return configPath
	}
	relative, err := filepath.Rel(projectRoot, absolute)
	if err != nil {
		return configPath
	}
	return relative
}

// defaultEvalName names an eval after what it evaluates and what it reads.
func defaultEvalName(target, source string) string {
	if source == initSourceTraces {
		return target + "-trace-eval"
	}
	return target + "-dataset-eval"
}

// uniqueEvalName is the suggested name, made one the file can still accept.
//
// init suggested a name and then refused it when the file already had one, so
// a second `azd ai eval init` in the same project failed on the command's own
// proposal and left the developer to invent a name. A name someone typed is
// still refused: that one is theirs, and silently grading something else under
// a nearby name is worse than saying no.
func uniqueEvalName(cfg *project.EvalConfig, base string) string {
	if name := trimEvalName(base, 0); !cfg.HasEval(name) {
		return name
	}
	// Terminates: each attempt is a distinct name and the file declares
	// finitely many.
	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		candidate := trimEvalName(base, len(suffix)) + suffix
		if !cfg.HasEval(candidate) {
			return candidate
		}
	}
}

// trimEvalName shortens the stem so that reserve more characters still fit.
func trimEvalName(base string, reserve int) string {
	limit := assetNameMaxLength - reserve
	if limit < 1 || len(base) <= limit {
		return base
	}
	return base[:limit]
}

// scaffoldInput is everything planScaffold needs, gathered so the signature
// does not grow a seventh positional string.
type scaffoldInput struct {
	evalName        string
	target          string
	source          string
	dataset         string
	maxTraces       int
	lookbackHours   int
	evaluationLevel string
	evaluators      []string
	judgeModel      string
	evalDir         string
	cfg             *project.EvalConfig
}

// scaffold is what `init` added, and what it should suggest doing next.
type scaffold struct {
	eval        *project.Eval
	datasetName string
	target      string
	judgeModel  string
	// evalDir is where the configuration was written, so the next steps can
	// name it when it is not the default.
	evalDir string
}

// planScaffold appends one eval to the configuration, adding any catalog
// entries it needs.
//
// Every reference it writes has to resolve to something that exists: a
// declaration already in the file, a local file it validated, or a registered
// name the caller supplied. Declaring an artifact that generation would produce
// later wrote an eval nothing satisfied -- `azd up` then failed on rows that
// "have not been generated yet", after deploying everything else.
func planScaffold(in scaffoldInput) (scaffold, error) {
	cfg := in.cfg
	out := scaffold{
		target:     in.target,
		judgeModel: in.judgeModel,
		evalDir:    in.evalDir,
	}

	eval := project.Eval{
		Name:            in.evalName,
		Description:     fmt.Sprintf("Basic quality evaluation for %s", in.target),
		EvaluationLevel: cmp.Or(in.evaluationLevel, project.EvaluationLevelTurn),
		Target: &project.Target{
			Type: project.TargetTypeAgent,
			Name: in.target,
		},
	}

	if in.source == initSourceTraces {
		// A trace-backed eval filters by agent rather than invoking one: the
		// conversations already happened.
		eval.Target = nil
		eval.Source = &project.SourceDecl{
			Type:          project.SourceTypeTraces,
			AgentName:     in.target,
			MaxTraces:     in.maxTraces,
			LookbackHours: in.lookbackHours,
		}
	} else {
		datasetName := ""
		datasetSource := ""
		switch {
		case in.dataset != "":
			if looksLikeLocalDataset(in.dataset) {
				// A path that names nothing is the same broken reference a
				// generated declaration used to leave behind: the config passes
				// validation and the deploy fails on a file that never existed.
				if _, err := os.Stat(in.dataset); err != nil {
					return scaffold{}, messages.DatasetFileNotFound(in.dataset, err)
				}
				// --dataset is given relative to where the user is standing,
				// but source: resolves relative to the config, so the path has
				// to be rebased or the deploy looks for it inside evals/.
				datasetSource = relativeToConfig(in.dataset, in.evalDir)
				datasetName = strings.TrimSuffix(
					filepath.Base(in.dataset), filepath.Ext(in.dataset))
			} else {
				// A bare name references an already-registered dataset.
				datasetName = in.dataset
			}
		case len(cfg.Datasets) == 1:
			// The one declaration in the file is not a guess.
			datasetName = cfg.Datasets[0].Name
		case len(cfg.Datasets) > 1:
			return scaffold{}, messages.AmbiguousDeclaredDataset(datasetNames(cfg))
		default:
			return scaffold{}, messages.DatasetSourceNeedsADataset()
		}
		eval.Dataset = datasetName
		out.datasetName = datasetName
		if datasetSource != "" || !declaresDataset(cfg, datasetName) {
			addDatasetDecl(cfg, project.DatasetDecl{Name: datasetName, File: datasetSource})
		}
	}

	// Every evaluator carries the judge deployment, because that is where the
	// service reads it from: judging built-ins declare it as required, so an
	// eval that leaves it off is rejected before it runs. The binding step
	// drops it again for a rule-based evaluator that declares no judge.
	initParams := map[string]any{}
	if in.judgeModel != "" {
		initParams["model"] = in.judgeModel
	}
	withModel := func(ref evalcore.EvaluatorRef) evalcore.EvaluatorRef {
		if len(initParams) == 0 {
			return ref
		}
		params := make(map[string]any, len(initParams))
		maps.Copy(params, initParams)
		ref.InitializationParameters = params
		return ref
	}

	refs := evalcore.EvaluatorList{}
	if len(in.evaluators) == 0 {
		for _, ref := range defaultEvaluators() {
			refs = append(refs, withModel(evalcore.EvaluatorRef{Evaluator: ref}))
		}
	} else {
		for _, e := range in.evaluators {
			ref := evalcore.EvaluatorRef{Evaluator: e}
			refs = append(refs, withModel(ref))
			if ref.IsBuiltin() {
				continue
			}
			// Only a declaration already in the file. A custom evaluator init
			// has not seen is a file nothing has produced, and declaring it
			// leaves `create` looking for a rubric that does not exist.
			if _, ok := cfg.EvaluatorDeclaration(e); !ok {
				return scaffold{}, messages.EvaluatorNotDeclared(e)
			}
		}
	}
	eval.Evaluators = refs

	cfg.Evals = append(cfg.Evals, eval)
	out.eval = &cfg.Evals[len(cfg.Evals)-1]
	return out, nil
}

// datasetNames lists the declared dataset names, for a refusal that has to name
// the choices.
func datasetNames(cfg *project.EvalConfig) []string {
	out := make([]string, 0, len(cfg.Datasets))
	for _, d := range cfg.Datasets {
		out = append(out, d.Name)
	}
	return out
}

// declaresDataset reports whether the configuration already names this dataset.
func declaresDataset(cfg *project.EvalConfig, name string) bool {
	_, ok := cfg.DatasetDeclaration(name)
	return ok
}

// addDatasetDecl adds a catalog entry unless the name is already declared.
func addDatasetDecl(cfg *project.EvalConfig, decl project.DatasetDecl) {
	if decl.Name == "" {
		return
	}
	// A source-less entry is still declared: it names a dataset already
	// registered on the project. Skipping it left the eval referencing a
	// dataset absent from the catalog, which its own validation rejects.
	if _, ok := cfg.DatasetDeclaration(decl.Name); ok {
		return
	}
	cfg.Datasets = append(cfg.Datasets, decl)
}

// refuseDuplicateEval stops `init` writing an eval that differs from one
// already declared only by name.
//
// Deploying refuses the pair, because the environment records an id against
// each eval's substance and a shared substance makes that lookup ambiguous.
// That refusal names the whole file, so the entry `init` had just written
// stayed there: `run start` went on offering it, and choosing it reported an
// eval that was declared but never deployed. Refusing before the write is what
// leaves nothing to clean up.
//
// Reads the configuration off disk and makes no service call. A configuration
// that cannot be read is not this check's business -- deploying will say so
// with better context -- so it is skipped rather than guessed at.
func refuseDuplicateEval(location string, planned *project.Eval) error {
	if planned == nil {
		return nil
	}
	cfg, err := project.OpenEvalConfig(location)
	if err != nil || cfg == nil {
		return nil
	}
	digest, err := project.FingerprintGroup(*planned)
	if err != nil {
		return nil
	}
	for _, existing := range cfg.Evals {
		if existing.Name == planned.Name {
			continue
		}
		other, err := project.FingerprintGroup(existing)
		if err != nil || other != digest {
			continue
		}
		return messages.EvalWouldDuplicate(planned.Name, existing.Name)
	}
	return nil
}

// declaredSoFar seeds the accumulator with the names the configuration already
// declares, so planScaffold can tell an addition from a duplicate.
//
// Names only. The write below appends to the document rather than saving this
// value, so nothing else about the existing entries is needed -- and reading
// more would mean decoding a configuration whose includes are deliberately left
// unresolved.
func declaredSoFar(authored *project.AuthoredConfig) *project.EvalConfig {
	cfg := &project.EvalConfig{}
	for _, name := range authored.Names(project.SectionDatasets) {
		cfg.Datasets = append(cfg.Datasets, project.DatasetDecl{Name: name})
	}
	for _, name := range authored.Names(project.SectionEvaluators) {
		cfg.Evaluators = append(cfg.Evaluators, project.EvaluatorDecl{Name: name})
	}
	for _, name := range authored.Names(project.SectionEvals) {
		cfg.Evals = append(cfg.Evals, project.Eval{Name: name})
	}
	return cfg
}

// evaluatorNames lists the evaluators the eval will run, in declaration order.
func (s scaffold) evaluatorNames() []string {
	names := make([]string, 0, len(s.eval.Evaluators))
	for _, ref := range s.eval.Evaluators {
		names = append(names, ref.Evaluator)
	}
	return names
}

// nextSteps are the commands to run after `init`, and only the ones that have
// something to do.
//
// A caller who supplied both a dataset and their evaluators has nothing left to
// generate, and pointing them at a generation command would submit a billed job
// for an artifact they already have.
//
// Every generate step carries --target and --generation-model, which `generate`
// requires and does not detect. Omitting them printed a next step that failed
// twice before it ran, each failure naming one more flag.
func (s scaffold) nextSteps(deployCmd string) []string {
	// `azd up` reads azure.yaml, which already $refs the configuration
	// wherever it was written, so it is the one step --path must not join.
	deploy := deployCmd
	if deploy != azdUpCommand {
		deploy = s.withPath(deploy)
	}
	return []string{deploy, s.withPath("azd ai eval run start")}
}

// withPath appends --path to a step that needs it to run where init wrote.
//
// The recorded EVAL_CONFIG_PATH would usually supply this on its own, but
// recording it is best effort -- it needs an azd environment, and `init` works
// without one. Naming the directory makes the printed step run as printed
// either way, which is the claim these lines make.
func (s scaffold) withPath(step string) string {
	if s.evalDir == "" || s.evalDir == project.DefaultEvalDir {
		return step
	}
	return step + " --path " + quoteForShell(s.evalDir)
}

// quoteForShell wraps a value a shell would otherwise read as more than one
// argument.
//
// `--path "./team evals"` is the difference between a printed step that runs
// and one that resolves ./team and reports the configuration missing. The rule
// lives in messages, beside the suggested commands that need the same thing.
func quoteForShell(v string) string {
	return messages.ShellArg(v)
}

// relativeToConfig rewrites a path given relative to the working directory so
// it resolves from the directory holding the eval config.
func relativeToConfig(path, evalDir string) string {
	if filepath.IsAbs(path) {
		return path
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	// The location is the directory before anything is written and the
	// configuration file once it exists, so a second `init` rebased every path
	// against the file and put a `..` in front of it.
	absOut, err := filepath.Abs(project.EvalDirOf(evalDir))
	if err != nil {
		return path
	}

	rel, err := filepath.Rel(absOut, absPath)
	if err != nil {
		return path
	}

	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, ".") {
		rel = "./" + rel
	}
	return rel
}

// rootConfigName is azd's project file, which the eval service is declared in.
const rootConfigName = "azure.yaml"

// aiProjectHost is the Foundry project service other extensions declare. The
// eval service uses it for ordering when the repo has one.
const aiProjectHost = "azure.ai.project"

// How the root config ended up referencing the eval service.
const (
	wiringAdded   = "added"   // the service was added to the project
	wiringPresent = "present" // an eval service was already declared
)

// readAzdProject returns the project, without changing it.
func readAzdProject(ctx context.Context) (*azdext.ProjectConfig, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, messages.NoAzdProject()
	}
	defer azdClient.Close()

	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		return nil, messages.NoAzdProject()
	}
	return resp.GetProject(), nil
}

// azdDefaultInfraDir is where azd looks for infrastructure when the project
// does not name a directory itself.
const azdDefaultInfraDir = "infra"

// projectCanProvision reports whether `azd provision` has anything to compile.
//
// This mirrors the provider sniff in azd's detectProviderFromFiles: the
// provider is inferred from the files in the infra directory, and a missing
// directory leaves it unspecified, which falls back to Bicep and fails on the
// absent infra/main.bicep -- verified against azd 1.30.0, where `azd up` on an
// eval-only project exits 1 and `azd deploy` does not run either.
//
// It is deliberately only that sniff. azd's real decision, ProjectInfrastructure,
// is also satisfied by infra layers, a .NET Aspire AppHost, and a `resources:`
// block in azure.yaml, none of which look at this directory. Each makes this
// answer false where `azd up` would have worked, so the cost of being wrong is
// naming our own command in a project that could also have provisioned -- which
// still publishes the eval.
func projectCanProvision(proj *azdext.ProjectConfig) bool {
	if proj == nil {
		return false
	}

	dir := proj.GetInfra().GetPath()
	if dir == "" {
		dir = azdDefaultInfraDir
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(proj.GetPath(), dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch filepath.Ext(e.Name()) {
		case ".bicep", ".bicepparam", ".tf", ".tfvars":
			return true
		}
	}
	return false
}

// aiModelHost is the model-deployment service the sibling Foundry extensions
// declare, which is where a judge deployment can be read without a service
// call.
const aiModelHost = "azure.ai.model"

// detectModelDeployments finds the deployments the graders could judge with,
// from what the project already declares.
//
// `init` makes no service calls, so detection is limited to the project file.
// Coming back empty leaves it to resolveJudgeModel, which reads the Foundry
// project's deployments: and then asks or names --judge-model.
//
// Every match is returned rather than the first, because two declared model
// services is a choice for the author to make, not something to settle here.
func detectModelDeployments(proj *azdext.ProjectConfig) []string {
	// Sorted, because GetServices is a map: without an order, a project with
	// two model services judged with a different deployment run to run.
	services := proj.GetServices()
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)

	seen := map[string]bool{}
	found := make([]string, 0, len(names))
	for _, name := range names {
		svc := services[name]
		if svc.GetHost() != aiModelHost {
			continue
		}
		deployment := name
		if props := svc.GetAdditionalProperties().AsMap(); props != nil {
			for _, key := range []string{"deployment", "deploymentName", "name", "model"} {
				if v, ok := props[key].(string); ok && v != "" {
					deployment = v
					break
				}
			}
		}
		// Two services naming the same deployment are not a choice.
		if !seen[deployment] {
			seen[deployment] = true
			found = append(found, deployment)
		}
	}
	return found
}

// planRootEvalService reports the azure.yaml edit init would make, without
// making it.
//
// The confirmation has to state the change before it happens, and the only way
// to know whether the service is already there is to look. Every refusal
// ensureRootEvalService can raise is raised here too, so a scaffold that cannot
// be wired is refused before the reader is asked to approve it.
func planRootEvalService(ctx context.Context, serviceName, configPath string) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		return "", messages.NoAzdProject()
	}
	return rootEvalServiceAction(resp.GetProject(), serviceName, configPath)
}

// rootEvalServiceAction decides what the project file needs, or refuses.
//
// A service already pointing at this configuration is left alone: re-adding it
// would deploy the same evals twice.
//
// Pointing at a different one is not the same thing. Matching on name and host
// alone reported the wiring present after `init --path` moved the
// configuration, and `azd up` went on deploying the file that was left behind
// -- the scaffold the reader was looking at was never deployed.
func rootEvalServiceAction(
	proj *azdext.ProjectConfig,
	serviceName, configPath string,
) (string, error) {
	wantRef := refTo(proj.GetPath(), configPath)
	svc, ok := proj.GetServices()[serviceName]
	if !ok {
		return wiringAdded, nil
	}
	// AddService assigns into the services map by name, so a service this
	// extension does not own would be replaced rather than added to.
	if svc.GetHost() != project.EvalHost {
		return "", messages.ServiceNameTaken(serviceName, svc.GetHost())
	}
	if have := serviceConfigRef(svc); have != "" && !sameRefTarget(have, wantRef) {
		return "", messages.ServiceRefPointsElsewhere(serviceName, have, wantRef)
	}
	return wiringPresent, nil
}

// ensureRootEvalService declares the eval service in azd's project file.
//
// azd acts on nothing until the service exists, so the reference is made rather
// than described. It goes through azd's own Project().AddService, the same call
// the agents extension uses, so azd owns the edit and the project file keeps
// whatever shape azd gives it.
func ensureRootEvalService(
	ctx context.Context,
	serviceName, target, configPath string,
) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		return "", messages.NoAzdProject()
	}

	// Decided again rather than carried over from the confirmation: the
	// project file is a shared file, and the read that the reader approved
	// was taken before an unbounded human pause.
	action, err := rootEvalServiceAction(resp.GetProject(), serviceName, configPath)
	if err != nil {
		return "", err
	}
	if action == wiringPresent {
		return wiringPresent, nil
	}

	props, err := structpb.NewStruct(map[string]any{
		"$ref": refTo(resp.GetProject().GetPath(), configPath),
	})
	if err != nil {
		return "", messages.BuildingServiceEntry(err)
	}

	_, err = azdClient.Project().AddService(ctx, &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{
			Name:                 serviceName,
			Host:                 project.EvalHost,
			Uses:                 evalServiceUses(resp.GetProject(), target),
			AdditionalProperties: props,
		},
	})
	if err != nil {
		return "", messages.AddingServiceTo(rootConfigName, err)
	}
	return wiringAdded, nil
}

// serviceConfigRef reads the $ref a service entry was authored with, or empty
// when it holds its configuration inline.
func serviceConfigRef(svc *azdext.ServiceConfig) string {
	props := svc.GetAdditionalProperties()
	if props == nil {
		return ""
	}
	ref, _ := props.AsMap()["$ref"].(string)
	return ref
}

// sameRefTarget compares two $ref values as paths rather than as text, so
// `evals/azure.eval.yaml` and `./evals/azure.eval.yaml` are one answer.
func sameRefTarget(a, b string) bool {
	return filepath.Clean(filepath.FromSlash(a)) == filepath.Clean(filepath.FromSlash(b))
}

// evalServiceUses orders the eval after the things it reads.
//
// It is conditional for the same reason the agents extension makes it
// conditional: naming a service the project does not declare is a broken
// reference, and an eval config can perfectly well sit in a repo that reaches
// an existing Foundry project by endpoint and an agent deployed elsewhere.
//
// Catalog entries need no ordering of their own — datasets, evaluators and
// evals are reconciled in a fixed order inside one deploy, forced by the
// contract rather than chosen.
func evalServiceUses(proj *azdext.ProjectConfig, target string) []string {
	var uses []string
	// Sorted, because GetServices is a map and this writes the `uses` list into
	// azure.yaml: an unordered pick rewrites the file differently each run.
	services := proj.GetServices()
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if services[name].GetHost() == aiProjectHost {
			uses = append(uses, name)
			break
		}
	}
	if _, ok := services[target]; ok {
		uses = append(uses, target)
	}
	return uses
}

// looksLikeLocalDataset distinguishes a path from a registered dataset name.
func looksLikeLocalDataset(v string) bool {
	if strings.ContainsAny(v, `/\`) {
		return true
	}
	return strings.EqualFold(filepath.Ext(v), ".jsonl")
}
