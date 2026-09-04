// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"fmt"
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
	shared        generateFlags
	maxSamples    int
	from          []string
	traceDays     int
	wantDataset   bool
	wantEvaluator bool
	datasetName   string
	evaluatorName string
}

// generateAction generates a dataset and a rubric evaluator together.
type generateAction struct {
	cmd   *cobra.Command
	flags *generateCommandFlags
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
		"Name for the generated dataset. Defaults to <target>-dataset.")
	cmd.Flags().StringVar(&flags.evaluatorName, "evaluator-name", "",
		"Name for the generated evaluator. Defaults to <target>-evaluator.")
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

	target := firstNonEmpty(a.flags.shared.target, declaredTarget(a.flags.shared.path))
	plans, err := buildGeneratePlans(generateRequest{
		flags:         &a.flags.shared,
		target:        target,
		dataset:       dataset,
		evaluator:     evaluator,
		datasetName:   a.flags.datasetName,
		evaluatorName: a.flags.evaluatorName,
		maxSamples:    a.flags.maxSamples,
		from:          a.flags.from,
		traceDays:     a.flags.traceDays,
	})
	if err != nil {
		return err
	}

	ec, resolved, err := prepareGeneration(a.cmd, &a.flags.shared, plans[0])
	if err != nil {
		return err
	}
	defer ec.Close()

	// prepareGeneration settles the inputs only the service can supply.
	// They are the same for both artifacts, so they are read once.
	for i := range plans {
		plans[i].Instruction = resolved.Instruction
		plans[i].Model = resolved.Model
	}
	if dataset && len(plans[0].From) == 0 {
		plans[0].From = defaultGenerationSource(
			ec.getEnvValue(a.cmd.Context(), appInsightsEnvKey),
		)
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
	flags         *generateFlags
	target        string
	dataset       bool
	evaluator     bool
	datasetName   string
	evaluatorName string
	maxSamples    int
	from          []string
	traceDays     int
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
		name, err := generatedName(req.datasetName, req.target, "dataset")
		if err != nil {
			return nil, err
		}
		plan, err := resolvePlan(req.flags, name, project.DefaultDatasetsDir)
		if err != nil {
			return nil, err
		}
		plan.Kind = generateKindDataset
		plan.From = req.from
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
		name, err := generatedName(req.evaluatorName, req.target, "evaluator")
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
// The name becomes a filename as well as a service asset name, so it is
// checked here: `--dataset-name ../../x` would otherwise write outside the
// directory the caller pointed generation at, and `--force` would overwrite
// whatever is there.
func generatedName(explicit, target, suffix string) (string, error) {
	name := explicit
	if name == "" {
		if target == "" {
			return "", messages.GeneratedNameNeedsATarget(suffix)
		}
		name = target + "-" + suffix
	}
	if !nameIsAPathComponent(name) {
		return "", messages.GeneratedNameNotAFileName(suffix, name)
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
	jobID  string
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
					cmd.Context(), o.plan, &o.output, flags.noWait, &o.jobID)
			default:
				o.ref, o.err = ec.generateRubric(
					cmd.Context(), o.plan, &o.output, flags.noWait, &o.jobID)
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
	return nil
}

// generationDocument keys each outcome by the artifact it was for, so a caller
// reads the two generations apart rather than by position.
func generationDocument(outcomes []generationOutcome) map[string]any {
	produced := map[string]any{}
	for i := range outcomes {
		o := &outcomes[i]
		switch {
		case o.ref != nil:
			produced[string(o.plan.Kind)] = o.ref
		case o.jobID != "":
			produced[string(o.plan.Kind)] = map[string]string{"job_id": o.jobID}
		default:
			produced[string(o.plan.Kind)] = nil
		}
	}
	return produced
}
