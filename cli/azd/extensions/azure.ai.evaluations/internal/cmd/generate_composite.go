// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"fmt"
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

func newGenerateCommand() *cobra.Command {
	var (
		flags         generateFlags
		maxSamples    int
		from          []string
		traceDays     int
		wantDataset   bool
		wantEvaluator bool
		datasetName   string
		evaluatorName string
	)

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
			dataset, evaluator := selectedArtifacts(wantDataset, wantEvaluator)

			if dataset {
				for _, src := range from {
					if err := project.ValidateGenerateSource(src); err != nil {
						return err
					}
				}
				if err := project.ValidateSampleSize(maxSamples); err != nil {
					return err
				}
			}

			target := firstNonEmpty(flags.target, declaredTarget(flags.path))
			plans, err := buildGeneratePlans(generateRequest{
				flags:         &flags,
				target:        target,
				dataset:       dataset,
				evaluator:     evaluator,
				datasetName:   datasetName,
				evaluatorName: evaluatorName,
				maxSamples:    maxSamples,
				from:          from,
				traceDays:     traceDays,
			})
			if err != nil {
				return err
			}

			ec, resolved, err := prepareGeneration(cmd, &flags, plans[0])
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
					ec.getEnvValue(cmd.Context(), appInsightsEnvKey),
				)
			}

			return ec.runGenerations(cmd, plans, flags)
		},
	}

	cmd.Flags().BoolVar(&wantDataset, "dataset", false,
		"Generate only the dataset. Omit both flags to generate both.")
	cmd.Flags().BoolVar(&wantEvaluator, "evaluator", false,
		"Generate only the evaluator. Omit both flags to generate both.")
	cmd.Flags().StringVar(&datasetName, "dataset-name", "",
		"Name for the generated dataset. Defaults to <target>-dataset.")
	cmd.Flags().StringVar(&evaluatorName, "evaluator-name", "",
		"Name for the generated evaluator. Defaults to <target>-evaluator.")
	cmd.Flags().IntVar(&maxSamples, "max-samples", 0,
		fmt.Sprintf("Rows to synthesize (%d-%d). Defaults to %d. Dataset only.",
			project.MinSampleSize, project.MaxSampleSize, project.DefaultSampleSize))
	cmd.Flags().StringSliceVar(&from, "from", nil,
		fmt.Sprintf("Where the dataset's rows come from: %s. Repeatable, and the "+
			"service accepts more than one. Defaults to %s when the project has "+
			"Application Insights connected, otherwise %s. Dataset only.",
			strings.Join(project.GenerateSources, ", "),
			project.GenerateFromTraces, project.GenerateFromAgent))
	cmd.Flags().IntVar(&traceDays, "trace-days", 0,
		"Days of traces to seed the evaluator's rubric. 0 disables.")
	addGenerateFlags(cmd, &flags)
	return cmd
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
		plans = append(plans, plan)
	}

	return plans, nil
}

// generatedName is the explicit name, or one derived from the target.
func generatedName(explicit, target, suffix string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if target == "" {
		return "", messages.GeneratedNameNeedsATarget(suffix)
	}
	return target + "-" + suffix, nil
}

type generationOutcome struct {
	plan   generationPlan
	ref    *project.ArtifactRef
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
		wg.Add(1)
		go func(o *generationOutcome) {
			defer wg.Done()
			switch o.plan.Kind {
			case generateKindDataset:
				o.ref, o.err = ec.generateDataset(
					cmd.Context(), o.plan, &o.output, flags.noWait)
			default:
				o.ref, o.err = ec.generateRubric(
					cmd.Context(), o.plan, &o.output, flags.noWait)
			}
		}(&outcomes[i])
	}
	wg.Wait()

	if !isJSON(cmd) {
		for i := range outcomes {
			if _, err := out.Write(outcomes[i].output.Bytes()); err != nil {
				return err
			}
		}
	}

	// Catalog entries land in one file, so they are written here rather than
	// from the goroutines that produced them.
	var failures []error
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

	if len(failures) > 0 {
		return messages.SomeGenerationsFailed(failures)
	}

	// One document, keyed by artifact: two bare objects on stdout is not
	// something a caller can parse.
	if isJSON(cmd) {
		produced := map[string]*project.ArtifactRef{}
		for i := range outcomes {
			produced[string(outcomes[i].plan.Kind)] = outcomes[i].ref
		}
		return emitJSON(out, produced)
	}
	return nil
}
