// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
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
	evalName   string
	target     string
	source     string
	dataset    string
	maxTraces  int
	evaluators []string
	judgeModel string
	path       string
	force      bool
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
		"Name of the eval. Defaults to <target>-eval, or <target>-trace-eval under --source traces.")
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
	cmd.Flags().StringSliceVar(&flags.evaluators, "evaluator", nil,
		"Evaluator reference, repeatable and comma-separated. Use builtin.<name> for a "+
			"built-in. Passing this replaces the defaults, so it also opts out of rubric generation.")
	cmd.Flags().StringVar(&flags.judgeModel, "judge-model", "",
		"Model deployment the graders judge with. Detected from the project when omitted.")
	cmd.Flags().StringVar(&flags.path, "path", "",
		"Directory to write the configuration into. Used verbatim, never re-rooted. "+
			"Defaults to the directory an earlier `init` scaffolded, otherwise ./evals.")
	cmd.Flags().BoolVar(&flags.force, "force", false,
		"Replace an eval of the same name instead of failing.")
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
	// Asked twice -- once to pick the default source, once to say so --
	// and each call opens an azd connection. The answer cannot change
	// mid-command, and a run that never asks never connects.
	tracesWired := sync.OnceValue(func() bool {
		return tracesConnected(commandContext(a.cmd))
	})
	settled, err := settleInitSource(
		source, a.cmd.Flags().Changed("max-traces"), tracesWired)
	if err != nil {
		return err
	}
	source = settled
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

	// The target is what the whole scaffold is named and shaped around,
	// so it is settled before anything derived from it.
	target := a.flags.target
	if target == "" {
		target, err = resolveAgentTarget(a.cmd, azdProject)
		if err != nil {
			return err
		}
	}
	evalName := a.flags.evalName
	if evalName == "" {
		evalName = defaultEvalName(target, source)
	}
	judgeModel := a.flags.judgeModel
	if judgeModel == "" {
		judgeModel, err = resolveJudgeModel(a.cmd, azdProject)
		if err != nil {
			return err
		}
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
	// Checked before the prompt as well as after it, so a name that is
	// already taken is reported without asking a question first.
	if cfg.HasEval(evalName) && !a.flags.force {
		return messages.EvalAlreadyDeclared(
			evalName, filepath.ToSlash(configPath))
	}

	// Asked, not detected: an eval grades on a set, so there is no
	// "the only one" to settle on, and which criteria define quality
	// is the substantive decision in the configuration.
	//
	// Deliberately outside the lock below. This is an unbounded human
	// pause, and a lock held across it would either block a concurrent
	// `generate` for as long as someone leaves the terminal, or -- once
	// that side gave up waiting -- protect nothing at all. The listing
	// it offers is only a menu; the authoritative read is taken after.
	evaluators := a.flags.evaluators
	evaluatorsWereChosen := len(evaluators) > 0
	if len(evaluators) == 0 {
		var asked bool
		evaluators, asked, err = resolveEvaluators(
			a.cmd, cfg, target+"-quality", source == initSourceTraces)
		if err != nil {
			return err
		}
		evaluatorsWereChosen = asked
	}

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
	// Recorded rather than applied here: the write below edits the file,
	// so the replacement has to be expressed as a removal it can make.
	replacedEval := ""
	if cfg.HasEval(evalName) {
		if !a.flags.force {
			return messages.EvalAlreadyDeclared(
				evalName, filepath.ToSlash(configPath))
		}
		replacedEval = evalName
		cfg.RemoveEval(evalName)
	}

	// What the file already declares, so the write can be limited to what
	// planScaffold adds to it.
	declaredDatasets := len(cfg.Datasets)
	declaredEvaluators := len(cfg.Evaluators)
	declaredEvals := len(cfg.Evals)

	// The location may be the file azure.yaml names rather than the
	// directory holding it, and artifacts sit beside the configuration.
	evalDir := project.EvalDirOf(path)
	if err := os.MkdirAll(filepath.Join(evalDir, project.DefaultDatasetsDir), 0o750); err != nil {
		return messages.CreatingDatasetsDir(err)
	}
	if err := os.MkdirAll(filepath.Join(evalDir, project.DefaultEvaluatorsDir), 0o750); err != nil {
		return messages.CreatingEvaluatorsDir(err)
	}

	plan := planScaffold(scaffoldInput{
		evalName:   evalName,
		target:     target,
		source:     source,
		dataset:    a.flags.dataset,
		maxTraces:  a.flags.maxTraces,
		evaluators: evaluators,
		judgeModel: judgeModel,
		rubricName: target + "-quality",
		evalDir:    evalDir,
		cfg:        cfg,
	})

	if err := refuseDuplicateEval(path, plan.eval); err != nil {
		return err
	}

	if err := project.ApplyScaffold(path, project.ScaffoldWrite{
		RemoveEval: replacedEval,
		Datasets:   cfg.Datasets[declaredDatasets:],
		Evaluators: cfg.Evaluators[declaredEvaluators:],
		Evals:      cfg.Evals[declaredEvals:],
	}); err != nil {
		return err
	}

	// Scaffolding a config azd cannot see is half a step: the eval
	// service has to be referenced from the root config before any of
	// `azd up`, `azd deploy` or `azd ai eval run` will act on it.
	serviceName := target + "-evals"
	rootWiring, err := ensureRootEvalService(a.cmd.Context(), serviceName, target, configPath)
	if err != nil {
		return err
	}

	recordEvalPath(a.cmd.Context(), path)

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
func settleInitSource(
	explicit string,
	maxTracesGiven bool,
	tracesWired func() bool,
) (string, error) {
	source := explicit
	if source == "" {
		// The same signal `generate --from` defaults on, read from the azd
		// environment rather than the service, so init still makes no service
		// calls. Traces are real conversations; a project wired to collect them
		// should not have to ask for them by flag.
		if tracesWired() {
			source = initSourceTraces
		} else {
			source = initSourceDataset
		}
	}
	if maxTracesGiven && source != initSourceTraces {
		return "", messages.MaxTracesNeedsTraceSource()
	}
	return source, nil
}

// refTo is the `$ref` value for a configuration at path.
//
// Relative paths get `./` so the directive reads as a path rather than a
// registry name. An absolute one already is a path, and prefixing it produced
// `.//tmp/evals/azure.eval.yaml`: `init` wrote the configuration where it was
// asked, and `azd up` then resolved something else under the project.
func refTo(configPath string) string {
	slashed := filepath.ToSlash(configPath)
	if filepath.IsAbs(configPath) {
		return slashed
	}
	return "./" + slashed
}

// defaultEvalName names an eval after what it evaluates and what it reads.
func defaultEvalName(target, source string) string {
	if source == initSourceTraces {
		return target + "-trace-eval"
	}
	return target + "-eval"
}

// scaffoldInput is everything planScaffold needs, gathered so the signature
// does not grow a seventh positional string.
type scaffoldInput struct {
	evalName   string
	target     string
	source     string
	dataset    string
	maxTraces  int
	evaluators []string
	judgeModel string
	rubricName string
	evalDir    string
	cfg        *project.EvalConfig
}

// scaffold is what `init` added, and what it should suggest doing next.
type scaffold struct {
	eval        *project.Eval
	datasetName string
	rubricName  string
	target      string
	judgeModel  string
	// evalDir is where the configuration was written, so the next steps can
	// name it when it is not the default.
	evalDir         string
	generateDataset bool
	generateRubric  bool
}

// planScaffold appends one eval to the configuration, adding any catalog
// entries it needs.
//
// The default evaluator set is a built-in plus a generated rubric: the built-in
// alone would be generic, and the rubric is what makes the baseline about this
// agent. Passing --evaluator replaces both, which is how a caller opts out of
// rubric generation.
func planScaffold(in scaffoldInput) scaffold {
	cfg := in.cfg
	out := scaffold{
		rubricName: in.rubricName,
		target:     in.target,
		judgeModel: in.judgeModel,
		evalDir:    in.evalDir,
	}

	eval := project.Eval{
		Name:            in.evalName,
		Description:     fmt.Sprintf("Basic quality evaluation for %s", in.target),
		EvaluationLevel: project.EvaluationLevelTurn,
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
			Type:      project.SourceTypeTraces,
			AgentName: in.target,
			MaxTraces: in.maxTraces,
		}
	} else {
		datasetName := in.evalName
		datasetSource := ""
		out.generateDataset = true
		if in.dataset != "" {
			out.generateDataset = false
			if looksLikeLocalDataset(in.dataset) {
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
		} else {
			datasetSource = fmt.Sprintf("./%s/%s.jsonl", project.DefaultDatasetsDir, datasetName)
		}
		eval.Dataset = datasetName
		out.datasetName = datasetName
		addDatasetDecl(cfg, project.DatasetDecl{Name: datasetName, File: datasetSource})
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
		refs = append(refs,
			withModel(evalcore.EvaluatorRef{
				Evaluator: evalcore.BuiltinPrefix + "task_adherence",
			}))
		if in.source != initSourceTraces {
			refs = append(refs, withModel(evalcore.EvaluatorRef{Evaluator: in.rubricName}))
			addEvaluatorDecl(cfg, project.EvaluatorDecl{
				Name:   in.rubricName,
				Source: fmt.Sprintf("./%s/%s.json", project.DefaultEvaluatorsDir, in.rubricName),
			})
			out.generateRubric = true
		}
	} else {
		for _, e := range in.evaluators {
			ref := evalcore.EvaluatorRef{Evaluator: e}
			refs = append(refs, withModel(ref))
			if ref.IsBuiltin() {
				continue
			}
			addEvaluatorDecl(cfg, project.EvaluatorDecl{
				Name:   e,
				Source: fmt.Sprintf("./%s/%s.json", project.DefaultEvaluatorsDir, e),
			})
			// Chosen, not defaulted, but it is still the rubric init offers to
			// write, so it still has to be generated. Without this the config
			// declares a file that nothing produces and `create` fails looking
			// for it.
			if e == in.rubricName {
				out.generateRubric = true
			}
		}
	}
	eval.Evaluators = refs

	cfg.Evals = append(cfg.Evals, eval)
	out.eval = &cfg.Evals[len(cfg.Evals)-1]
	return out
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

// addEvaluatorDecl adds a catalog entry unless the name is already declared.
func addEvaluatorDecl(cfg *project.EvalConfig, decl project.EvaluatorDecl) {
	if _, ok := cfg.EvaluatorDeclaration(decl.Name); ok {
		return
	}
	cfg.Evaluators = append(cfg.Evaluators, decl)
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
	var steps []string
	switch {
	case s.generateDataset && s.generateRubric:
		// One command produces both, which is the whole point of the composite.
		// It still has to be told the names this scaffold just declared: bare
		// `generate` derives its own from the target, so the printed step
		// published `<target>-dataset` while the configuration was waiting for
		// the name recorded here, and the deploy that followed could not find
		// either source.
		steps = append(steps, s.generateCommand(
			"--dataset-name "+quoteForShell(s.datasetName)+
				" --evaluator-name "+quoteForShell(s.rubricName)))
	case s.generateDataset:
		steps = append(steps, s.generateCommand("--dataset --dataset-name "+quoteForShell(s.datasetName)))
	case s.generateRubric:
		steps = append(steps, s.generateCommand("--evaluator --evaluator-name "+quoteForShell(s.rubricName)))
	}
	if len(steps) == 0 {
		// `azd up` reads azure.yaml, which already $refs the configuration
		// wherever it was written, so it is the one step --path must not join.
		deploy := deployCmd
		if deploy != azdUpCommand {
			deploy = s.withPath(deploy)
		}
		steps = append(steps, deploy, s.withPath("azd ai eval run start"))
	}
	return steps
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

// generateCommand builds a `generate` invocation that runs as printed.
//
// Every interpolated value is quoted, not just the path: --name is free-form
// and becomes the dataset name, and a target or a model deployment can carry a
// space too. Unquoted, `--dataset-name my eval` passed `my` and left `eval` as
// a positional argument that `generate` refuses without naming the cause.
func (s scaffold) generateCommand(what string) string {
	cmd := "azd ai eval generate"
	if what != "" {
		cmd += " " + what
	}
	if s.target != "" {
		cmd += " --target " + quoteForShell(s.target)
	}
	if s.judgeModel != "" {
		cmd += " --generation-model " + quoteForShell(s.judgeModel)
	}
	return s.withPath(cmd)
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

// detectModelDeployment finds the deployment the graders judge with, from what
// the project already declares.
//
// `init` makes no service calls, so detection is limited to the project file.
// Coming back empty leaves it to resolveJudgeModel, which reads the Foundry
// project's deployments: and then asks or names --judge-model.
func detectModelDeployment(proj *azdext.ProjectConfig) string {
	// Sorted, because GetServices is a map: without an order, a project with
	// two model services judged with a different deployment run to run.
	services := proj.GetServices()
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		svc := services[name]
		if svc.GetHost() != aiModelHost {
			continue
		}
		if props := svc.GetAdditionalProperties().AsMap(); props != nil {
			for _, key := range []string{"deployment", "deploymentName", "name", "model"} {
				if v, ok := props[key].(string); ok && v != "" {
					return v
				}
			}
		}
		return name
	}
	return ""
}

// recordEvalPath remembers where the configuration was written, so the commands
// that read it afterwards do not need --path repeated.
//
// Best effort: `init` works outside an azd environment, and a path that could
// not be recorded only costs the caller a flag later. It is never a reason to
// fail a scaffold that already succeeded.
func recordEvalPath(ctx context.Context, path string) {
	if path == "" || path == project.DefaultEvalDir {
		return
	}
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return
	}
	defer azdClient.Close()

	envName := azdEnvironmentName(ctx, azdClient)
	if envName == "" {
		return
	}
	_, _ = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     envKeyEvalPath,
		Value:   filepath.ToSlash(path),
	})
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

	// A service already pointing at this configuration is left alone:
	// re-adding it would deploy the same evals twice.
	//
	// Pointing at a different one is not the same thing. Matching on name and
	// host alone reported the wiring present after `init --path` moved the
	// configuration, and `azd up` went on deploying the file that was left
	// behind -- the scaffold the reader was looking at was never deployed.
	wantRef := refTo(configPath)
	if svc, ok := resp.GetProject().GetServices()[serviceName]; ok && svc.GetHost() == project.EvalHost {
		if have := serviceConfigRef(svc); have != "" && !sameRefTarget(have, wantRef) {
			return "", messages.ServiceRefPointsElsewhere(serviceName, have, wantRef)
		}
		return wiringPresent, nil
	}

	props, err := structpb.NewStruct(map[string]any{
		"$ref": wantRef,
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
