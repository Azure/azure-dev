// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"cmp"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/spf13/cobra"
)

// One verb with a selector, per the spec.
//
// The two artifacts are separate long-running service resources, but a
// developer starting out wants both and should not have to know that. Omitting
// both flags generates both; passing one narrows generation to it, which is
// also how you regenerate one after the other has been hand-edited.
//
// The jobs are submitted together because neither is an input to the other.
// Their output is buffered and replayed in a fixed order rather than written as
// it arrives: two generations reporting progress into the same terminal
// interleave into nonsense. The catalog is written after both have finished,
// on this goroutine, because both entries land in the same file.

// generateCommandFlags carries what `generate` was asked for: the flags every
// generating command shares, and the ones only this one registers.
type generateCommandFlags struct {
	shared          generateFlags
	maxSamples      int
	from            []string
	traceDays       int
	wantDataset     bool
	wantEvaluator   bool
	datasetName     string
	evaluatorName   string
	evaluationLevel string
}

// generateAction generates a dataset and a rubric evaluator together.
type generateAction struct {
	cmd   *cobra.Command
	flags *generateCommandFlags
	// resolved holds what only the service can supply, kept so a second pass
	// through the confirmation does not read the agent again.
	resolved generationPlan
}

func newGenerateCommand() *cobra.Command {
	flags := &generateCommandFlags{}

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a dataset and a rubric evaluator, and download them.",
		Long: "Generate a dataset and a rubric evaluator, and download them.\n\n" +
			"Both are produced unless --dataset or --evaluator narrows it to one. " +
			"Neither is an input to the other, so the jobs run together and each " +
			"reports its own outcome; the command fails if either did.\n\n" +
			"--from selects one or more of the sources the service generates the " +
			"dataset from, and is repeatable.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&generateAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().BoolVar(&flags.wantDataset, "dataset", false,
		"Generate only the dataset. Omit both flags to generate both.")
	cmd.Flags().BoolVar(&flags.wantEvaluator, "evaluator", false,
		"Generate only the evaluator. Omit both flags to generate both.")
	cmd.Flags().StringVar(&flags.datasetName, "dataset-name", "",
		"Name for the generated dataset. Defaults to <target>-turn-tests or "+
			"<target>-conversation-tests, following --evaluation-level.")
	cmd.Flags().StringVar(&flags.evaluatorName, "evaluator-name", "",
		"Name for the generated evaluator. Defaults to <target>-evaluator.")
	cmd.Flags().StringVar(&flags.evaluationLevel, "evaluation-level", "",
		fmt.Sprintf("What one generated row is: %s. Defaults to %s. Dataset only.",
			strings.Join(evaluationLevels, " or "), project.EvaluationLevelTurn))
	cmd.Flags().IntVar(&flags.maxSamples, "max-samples", 0,
		fmt.Sprintf("Rows to synthesize (%d-%d). Defaults to %d. Dataset only.",
			project.MinSampleSize, project.MaxSampleSize, project.DefaultSampleSize))
	cmd.Flags().StringSliceVar(&flags.from, "from", nil,
		fmt.Sprintf("Where the dataset's rows come from: %s. Repeatable, and the "+
			"service accepts more than one. Defaults to %s when the project has "+
			"Application Insights connected, otherwise %s. Dataset only.",
			strings.Join(project.GenerateSources, ", "),
			project.GenerateFromTraces, project.GenerateFromAgent))
	cmd.Flags().IntVar(&flags.traceDays, "trace-days", 0,
		"Days of traces to seed the evaluator's rubric. 0 disables.")
	addGenerateFlags(cmd, &flags.shared)
	return cmd
}

func (a *generateAction) Run() error {
	dataset, evaluator := selectedArtifacts(a.flags.wantDataset, a.flags.wantEvaluator)
	// Checked before any network work, so a flag that cannot apply
	// costs nothing to find out about. Changed() rather than the value,
	// so a zero the caller actually typed is still caught and an
	// untouched default is not.
	if err := refuseInapplicableFlags(a.cmd, dataset, evaluator); err != nil {
		return err
	}
	if a.flags.traceDays < 0 {
		return messages.NegativeTraceDays(a.flags.traceDays)
	}
	if a.flags.shared.noWait && a.cmd.Flags().Changed("output-dir") {
		return messages.OutputDirNeedsTheWait()
	}
	// Same reason: --force asks for a local file to be replaced, and --no-wait
	// returns before there is one to replace.
	if a.flags.shared.noWait && a.cmd.Flags().Changed("force") {
		return messages.ForceNeedsTheWait()
	}
	// One command builds two artifacts. --output-dir naming a file
	// gives both of them the same path -- the extension is recognized
	// for either kind -- and they are written concurrently, so two
	// billed jobs would leave one file and a configuration claiming a
	// dataset and an evaluator that are the same bytes.
	if dataset && evaluator && project.OutputDirNamesAFile(a.flags.shared.outputDir) {
		return messages.OutputFileCannotHoldBothArtifacts(a.flags.shared.outputDir)
	}

	if dataset {
		for _, src := range a.flags.from {
			if err := project.ValidateGenerateSource(src); err != nil {
				return err
			}
		}
		if err := project.ValidateSampleSize(a.flags.maxSamples); err != nil {
			return err
		}
	}

	// Settled before anything reads or writes the configuration, so the
	// catalog entry lands next to the eval `init` scaffolded rather than
	// in a second configuration under ./evals that nothing else reads.
	resolvedPath, err := resolveEvalDir(a.cmd.Context(), a.flags.shared.path)
	if err != nil {
		return err
	}
	a.flags.shared.path = resolvedPath

	// Only consulted when --target was not given, so a configuration declaring
	// evals for several agents is refused rather than silently taking the first.
	target := a.flags.shared.target
	if target == "" {
		if target, err = declaredTarget(a.flags.shared.path); err != nil {
			return err
		}
	}
	// Everything up to the confirmation is read-only. Generation calls a model
	// and registers artifacts in a shared project, and none of that was
	// confirmed: the first thing a reader saw was a job id for work already
	// submitted, which is why Cancel has to come before any of it.
	//
	// Context is resolved before the artifacts are named, because the name of
	// a generated dataset depends on an answer that block precedes. Naming is
	// what makes the order matter: building plans first would run the
	// already-exists check against a name for a level the reader had not been
	// offered yet, and refuse a collision they never chose.
	contextPlan, err := resolvePlan(&a.flags.shared, "", project.DefaultDatasetsDir)
	if err != nil {
		return err
	}
	ec, resolved, err := prepareGeneration(a.cmd, &a.flags.shared, contextPlan)
	if err != nil {
		return err
	}
	defer ec.Close()
	a.resolved = resolved

	projectName := projectNameOf(ec.endpoint)
	if !noPrompt(a.cmd) && !isJSON(a.cmd) {
		configExists, evals := evalConfigState(a.flags.shared.path)
		writeGenerateContext(a.cmd.OutOrStdout(), generateContext{
			agent:        resolved.Agent,
			model:        resolved.Model,
			projectName:  projectName,
			configPath:   a.flags.shared.path,
			configExists: configExists,
			evals:        evals,
		})
		fmt.Fprint(a.cmd.OutOrStdout(),
			messages.AgentInstructionsSource(resolved.InstructionSource))
	}

	// Asked here, not only on the Change path below. Defaulting to both and
	// waiting for the reader to say otherwise meant the question was never put:
	// a bare `generate` submitted two billed jobs having offered the choice
	// nowhere. askGenerateArtifacts still answers from the flags when either
	// was given, and still returns both under --no-prompt.
	choices, err := a.askGenerateArtifacts()
	if err != nil {
		return err
	}
	var plans []generationPlan
	level := ""
	levelSettled := false
	for {
		// Asked before the dataset is named, because the name says which level
		// its rows hold. Asked at most once: a second pass through the
		// confirmation is about scope, and re-asking would turn Change into a
		// restart of everything already answered.
		if choices.dataset && !levelSettled {
			if level, err = resolveGenerationLevel(a.cmd, a.flags.evaluationLevel); err != nil {
				return err
			}
			levelSettled = true
		}

		plans, err = buildGeneratePlans(generateRequest{
			flags:           &a.flags.shared,
			target:          target,
			dataset:         choices.dataset,
			evaluator:       choices.evaluator,
			datasetName:     a.flags.datasetName,
			evaluatorName:   a.flags.evaluatorName,
			evaluationLevel: level,
			maxSamples:      a.flags.maxSamples,
			from:            a.flags.from,
			traceDays:       a.flags.traceDays,
		})
		if err != nil {
			return err
		}

		// prepareGeneration settled the inputs only the service can supply.
		// They are the same for both artifacts, so they are read once.
		for i := range plans {
			plans[i].Instruction = a.resolved.Instruction
			plans[i].InstructionSource = a.resolved.InstructionSource
			plans[i].Model = a.resolved.Model
		}
		if choices.dataset && len(plans[0].From) == 0 {
			plans[0].From = defaultGenerationSource(
				ec.getEnvValue(a.cmd.Context(), appInsightsEnvKey),
			)
		}

		decision, err := confirmGeneration(a.cmd, a.cmd.OutOrStdout(), generationSummary{
			plans:       plans,
			model:       a.resolved.Model,
			instructed:  a.resolved.InstructionSource,
			projectName: projectName,
			configPath:  a.flags.shared.path,
			noWait:      a.flags.shared.noWait,
		})
		if err != nil {
			return err
		}
		if decision == generateCancel {
			fmt.Fprint(a.cmd.OutOrStdout(), messages.GenerationCancelled())
			return nil
		}
		if decision == generateProceed {
			break
		}
		if choices, err = a.askGenerateArtifacts(); err != nil {
			return err
		}
	}

	return ec.runGenerations(a.cmd, plans, a.flags.shared)
}

// selectedArtifacts reads the pair of narrowing flags. Neither set means both,
// which is the zero-to-first-eval path the composite exists for.
func selectedArtifacts(dataset, evaluator bool) (bool, bool) {
	if !dataset && !evaluator {
		return true, true
	}
	return dataset, evaluator
}

type generateRequest struct {
	flags           *generateFlags
	target          string
	dataset         bool
	evaluator       bool
	datasetName     string
	evaluatorName   string
	evaluationLevel string
	maxSamples      int
	from            []string
	traceDays       int
}

// artifactScopedFlags are the flags buildGeneratePlans reads only while
// building one kind of artifact. Given for the other kind they were accepted
// and dropped, so `--dataset --trace-days 7` produced a dataset and said
// nothing about the seven days.
var artifactScopedFlags = []struct {
	name     string
	forEval  bool // read under req.evaluator rather than req.dataset
	otherFor string
}{
	{name: "from", otherFor: "--evaluator"},
	{name: "max-samples", otherFor: "--evaluator"},
	{name: "dataset-name", otherFor: "--evaluator"},
	{name: "evaluation-level", otherFor: "--evaluator"},
	{name: "trace-days", forEval: true, otherFor: "--dataset"},
	{name: "evaluator-name", forEval: true, otherFor: "--dataset"},
}

func refuseInapplicableFlags(cmd *cobra.Command, dataset, evaluator bool) error {
	for _, f := range artifactScopedFlags {
		applies := dataset
		if f.forEval {
			applies = evaluator
		}
		if !applies && cmd.Flags().Changed(f.name) {
			return messages.FlagDoesNotApply(f.name, f.otherFor)
		}
	}
	return nil
}

// buildGeneratePlans settles everything that does not need the network, for
// each artifact asked for. Ordered dataset first, which is the order their
// progress is replayed in.
func buildGeneratePlans(req generateRequest) ([]generationPlan, error) {
	plans := make([]generationPlan, 0, 2)

	if req.dataset {
		name, err := generatedName(
			req.datasetName, req.target, "dataset", datasetNameSuffix(req.evaluationLevel))
		if err != nil {
			return nil, err
		}
		plan, err := resolvePlan(req.flags, name, project.DefaultDatasetsDir)
		if err != nil {
			return nil, err
		}
		plan.Kind = generateKindDataset
		plan.From = req.from
		plan.EvaluationLevel = req.evaluationLevel
		plan.SampleSize = req.maxSamples
		if plan.SampleSize == 0 {
			plan.SampleSize = project.DefaultSampleSize
		}
		if err := refuseExistingArtifact(
			project.ArtifactPath(plan.BaseDir, plan.OutputDir, name, ".jsonl"),
			req.flags.force,
		); err != nil {
			return nil, err
		}
		if err := refuseUneditableCatalogEntry(plan.BaseDir, "dataset", name); err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}

	if req.evaluator {
		name, err := generatedName(req.evaluatorName, req.target, "evaluator", "evaluator")
		if err != nil {
			return nil, err
		}
		plan, err := resolvePlan(req.flags, name, project.DefaultEvaluatorsDir)
		if err != nil {
			return nil, err
		}
		plan.Kind = generateKindEvaluator
		plan.TraceDays = req.traceDays
		if err := refuseExistingArtifact(
			project.ArtifactPath(plan.BaseDir, plan.OutputDir, name, ".json"),
			req.flags.force,
		); err != nil {
			return nil, err
		}
		if err := refuseUneditableCatalogEntry(plan.BaseDir, "evaluator", name); err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}

	return plans, nil
}

// generatedName is the explicit name, or one derived from the target.
//
// kind is the word the refusals use; suffix is what an derived name ends in.
// They differ for a dataset, whose name says which level its rows are at while
// the error still has to say which flag would fix it.
//
// The name becomes a filename as well as a service asset name, so it is
// checked here: `--dataset-name ../../x` would otherwise write outside the
// directory the caller pointed generation at, and `--force` would overwrite
// whatever is there.
func generatedName(explicit, target, kind, suffix string) (string, error) {
	name := explicit
	if name == "" {
		if target == "" {
			return "", messages.GeneratedNameNeedsATarget(kind)
		}
		name = target + "-" + suffix
	}
	if !nameIsAPathComponent(name) {
		return "", messages.GeneratedNameNotAFileName(kind, name)
	}
	return name, nil
}

// nameIsAPathComponent reports whether a name stays where it is put.
//
// Only the filesystem's objections are checked. The service enforces its own
// character set, and duplicating it here would refuse names it accepts.
func nameIsAPathComponent(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	// A leading dash writes a file whose name reads as a flag to whatever the
	// caller pipes the path into, which is the filesystem's objection rather
	// than the service's.
	if strings.HasPrefix(name, "-") {
		return false
	}
	if strings.ContainsAny(name, `/\:`) || filepath.IsAbs(name) {
		return false
	}
	return true
}

type generationOutcome struct {
	plan   generationPlan
	ref    *project.ArtifactRef
	report generationReport
	output bytes.Buffer
	err    error
}

// runGenerations submits every plan at once and settles them together.
func (ec *evalContext) runGenerations(
	cmd *cobra.Command,
	plans []generationPlan,
	flags generateFlags,
) error {
	outcomes := make([]generationOutcome, len(plans))
	var wg sync.WaitGroup

	// The announcements go out before the goroutines start, so a long
	// generation is not silent while it runs. Only the per-job progress is
	// buffered, which is what would interleave.
	out := cmd.OutOrStdout()
	if !isJSON(cmd) {
		for i := range plans {
			fmt.Fprint(out, messages.GenerationStarting(string(plans[i].Kind), plans[i].Name))
		}
	}

	for i := range plans {
		outcomes[i].plan = plans[i]
		o := &outcomes[i]
		wg.Go(func() {
			switch o.plan.Kind {
			case generateKindDataset:
				o.ref, o.err = ec.generateDataset(
					cmd.Context(), o.plan, &o.output, flags.noWait, &o.report,
					promptSourceRetryConsent(cmd))
			default:
				o.ref, o.err = ec.generateRubric(
					cmd.Context(), o.plan, &o.output, flags.noWait, &o.report)
			}
		})
	}
	wg.Wait()

	// A failed write must not cost the caller the catalog entries for work the
	// service already billed them for, so it is carried rather than returned.
	var failures []error
	if !isJSON(cmd) {
		for i := range outcomes {
			if _, err := out.Write(outcomes[i].output.Bytes()); err != nil {
				failures = append(failures, err)
				break
			}
		}
	}

	// Catalog entries land in one file, so they are written here rather than
	// from the goroutines that produced them.
	for i := range outcomes {
		o := &outcomes[i]
		if o.err != nil {
			failures = append(failures, messages.GenerationFailed(string(o.plan.Kind), o.err))
			continue
		}
		var err error
		switch o.plan.Kind {
		case generateKindDataset:
			err = addDatasetToCatalog(cmd, flags.path, o.ref)
		default:
			err = addEvaluatorToCatalog(cmd, flags.path, o.ref)
		}
		if err != nil {
			failures = append(failures, err)
		}
	}

	// One document, keyed by artifact: two bare objects on stdout is not
	// something a caller can parse. Under --no-wait there is no artifact yet,
	// so the job id is what the caller gets and what they reattach with.
	//
	// Emitted before a failure is returned, for the same reason the write
	// errors above are carried rather than returned: the two generations are
	// independent, so one can succeed while the other fails, and the service
	// has already billed the one that succeeded. Returning first left a JSON
	// caller with neither its reference nor its job id.
	if isJSON(cmd) {
		if err := emitJSON(out, generationDocument(outcomes)); err != nil {
			failures = append(failures, err)
		}
	}

	if len(failures) > 0 {
		return messages.SomeGenerationsFailed(failures)
	}
	// What was produced, what it was billed under, and the one command that
	// turns it into an eval. Generation used to end on the last download line,
	// so the job ids -- the only handle on a billed job -- scrolled past
	// unlabelled, and the caller was left to work out that `init` was next and
	// to retype every name it had just chosen for them.
	if !isJSON(cmd) && !flags.noWait {
		writeGenerationCompleted(out, outcomes)
	}
	return nil
}

// writeGenerationCompleted closes a successful generation.
func writeGenerationCompleted(out io.Writer, outcomes []generationOutcome) {
	fmt.Fprint(out, messages.GenerationCompleted())
	for i := range outcomes {
		if id := outcomes[i].report.jobID; id != "" {
			fmt.Fprint(out, messages.GenerationJobLine(string(outcomes[i].plan.Kind), id))
		}
	}
	if next := initHandoff(outcomes); next != "" {
		fmt.Fprint(out, messages.FirstNextStep(next))
	}
}

// initHandoff is the `eval init` that turns what was just generated into an
// eval, with every value it needs already filled in.
//
// --target is included even though `init` can detect it: the handoff is
// documented to run exactly as printed, and the detection depends on the
// project being readable at the time it is run rather than at the time it was
// printed.
func initHandoff(outcomes []generationOutcome) string {
	var agent, dataset, level, evaluator string
	for i := range outcomes {
		o := &outcomes[i]
		if o.ref == nil {
			continue
		}
		agent = cmp.Or(agent, o.plan.Agent)
		switch o.plan.Kind {
		case generateKindDataset:
			dataset = o.ref.Name
			level = o.plan.EvaluationLevel
		default:
			evaluator = o.ref.Name
		}
	}
	if dataset == "" && evaluator == "" {
		return ""
	}
	return messages.InitHandoffCommand(agent, dataset, level, evaluator)
}

// generationDocument keys each outcome by the artifact it was for, so a caller
// reads the two generations apart rather than by position.
//
// Warnings ride alongside the artifact rather than replacing it: a warned
// generation still produced something, and a caller that only looked at the
// reference would read it as clean.
func generationDocument(outcomes []generationOutcome) map[string]any {
	produced := map[string]any{}
	for i := range outcomes {
		o := &outcomes[i]
		var entry any
		switch {
		case o.ref != nil:
			entry = o.ref
		case o.report.jobID != "":
			entry = map[string]string{"job_id": o.report.jobID}
		}
		if len(o.report.warnings) > 0 {
			warned := map[string]any{"warnings": o.report.warnings}
			if entry != nil {
				warned["artifact"] = entry
			}
			if o.report.jobID != "" {
				warned["job_id"] = o.report.jobID
			}
			entry = warned
		}
		produced[string(o.plan.Kind)] = entry
	}
	return produced
}
