// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"path/filepath"

	"azureaieval/internal/project"

	"github.com/spf13/cobra"
)

// Generation is split per artifact because the service splits it: datasets and
// evaluators are separate long-running resources. One composite verb leaves
// partial failure undefined, cannot regenerate one artifact after the other has
// been hand-edited, and gives --no-wait nothing to reattach to.
//
// Neither command edits azure.yaml. `init` declares where the artifacts live;
// these fill them in, so a generation run produces a data-file-only diff.

// generateFlags are the settings both generate commands share.
type generateFlags struct {
	configPath      string
	evalName        string
	target          string
	instruction     string
	instructionFile string
	model           string
	outputDir       string
	noWait          bool
	endpoint        string
}

func addGenerateFlags(cmd *cobra.Command, f *generateFlags) {
	cmd.Flags().StringVar(&f.configPath, "config", project.DefaultGenerateConfig,
		"Path to the generation spec. Optional; flags alone are sufficient.")
	addEvalFlag(cmd, &f.evalName)
	cmd.Flags().StringVar(&f.target, "target", "", "Agent whose context seeds generation.")
	cmd.Flags().StringVar(&f.instruction, "agent-instruction", "",
		"What the agent does and what to test.")
	cmd.Flags().StringVar(&f.instructionFile, "agent-instruction-file", "",
		"Read the agent instruction from this file. Mutually exclusive with --agent-instruction.")
	cmd.MarkFlagsMutuallyExclusive("agent-instruction", "agent-instruction-file")
	cmd.Flags().StringVar(&f.model, "generation-model", "",
		"Model deployment that generates the artifact.")
	cmd.Flags().StringVar(&f.outputDir, "output-dir", "",
		"Directory the generated artifact is written to. Overrides the generation spec.")
	cmd.Flags().BoolVar(&f.noWait, "no-wait", false,
		"Submit the job and return its id without polling.")
	cmd.Flags().StringVar(&f.endpoint, "project-endpoint", "", "Foundry project endpoint.")
}

// prepareGeneration builds the client and settles the one input that needs it.
//
// Everything decidable offline is already on the plan by this point, so a
// mistake in the flags has been reported without an authentication round trip.
// What is left is the generation instruction's last fallback: the agent's
// published instructions, which only the service can supply.
func prepareGeneration(
	cmd *cobra.Command,
	f *generateFlags,
	plan generationPlan,
	declared genEntry,
) (*evalContext, generationPlan, error) {
	ctx := cmd.Context()
	ec, err := newEvalContext(ctx, f.endpoint)
	if err != nil {
		return nil, plan, err
	}

	plan.Instruction, err = ec.resolveGenerationInstruction(
		ctx, plan.Instruction, declared.instructions, f.configPath, plan.Agent,
		cmd.OutOrStdout(), isJSON(cmd),
	)
	if err != nil {
		ec.Close()
		return nil, plan, err
	}
	return ec, plan, nil
}

// resolvePlan settles every input that does not need the network.
//
// Resolution order is the one the spec fixes for every input: flags, then the
// generation spec, then what can be detected from the eval configuration.
// Doing it before the client is built means a missing model or an out-of-range
// sample count is refused without an authentication round trip.
//
// The instruction file is read here rather than later so that an input the
// caller named and got wrong is reported ahead of one they simply left out.
func resolvePlan(
	f *generateFlags,
	cfg *project.GenerateConfig,
	name string,
	declared genEntry,
) (generationPlan, error) {
	instruction, err := resolveInstruction(f.instruction, f.instructionFile)
	if err != nil {
		return generationPlan{}, err
	}

	plan := generationPlan{
		Name:        name,
		Agent:       firstNonEmpty(f.target, declared.deriveFrom, evalTarget(f)),
		Model:       firstNonEmpty(f.model, cfg.GenerationModel),
		Instruction: instruction,
		BaseDir:     filepath.Dir(f.configPath),
		OutputDir:   firstNonEmpty(f.outputDir, declared.outputDir),
		SampleSize:  declared.sampleSize,
		TraceDays:   declared.traceDays,
	}
	if plan.Model == "" {
		return plan, fmt.Errorf(
			"a model deployment is required to generate: pass --generation-model, " +
				"or set `generationModel` in the generation spec")
	}
	return plan, nil
}

// genEntry is the subset of a generation spec entry both commands share, so
// resolvePlan does not need to know which one it is serving.
type genEntry struct {
	outputDir    string
	deriveFrom   string
	instructions string
	sampleSize   int
	traceDays    int
}

// evalTarget reads the agent from the eval configuration, which is where the
// target is already declared, so `generate` does not need it repeated.
//
// Best effort: generation runs from the instruction alone when there is no
// eval config to read, which is the case in a bare directory.
func evalTarget(f *generateFlags) string {
	path, err := project.ResolveEvalConfigPath(filepath.Dir(f.configPath), f.evalName)
	if err != nil {
		return ""
	}
	cfg, err := project.LoadEvalConfig(path)
	if err != nil || cfg.Target == nil {
		return ""
	}
	return cfg.Target.Name
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// datasetGenEntry reads one dataset's settings out of the generation spec,
// applying the flag override and the default row count.
func datasetGenEntry(cfg *project.GenerateConfig, name string, maxSamples int) genEntry {
	spec, _ := cfg.DatasetSpec(name)
	entry := genEntry{
		outputDir:    firstNonEmpty(spec.OutputDir, "./"+project.DefaultDatasetsDir),
		deriveFrom:   spec.DeriveFrom,
		instructions: spec.Instructions,
		sampleSize:   spec.SampleSize,
		traceDays:    spec.TraceDays,
	}
	if maxSamples > 0 {
		entry.sampleSize = maxSamples
	}
	if entry.sampleSize == 0 {
		entry.sampleSize = project.DefaultSampleSize
	}
	return entry
}

// evaluatorGenEntry reads one evaluator's settings out of the generation spec,
// applying the flag override.
func evaluatorGenEntry(cfg *project.GenerateConfig, name string, traceDays int) genEntry {
	spec, _ := cfg.EvaluatorSpec(name)
	entry := genEntry{
		outputDir:    firstNonEmpty(spec.OutputDir, "./"+project.DefaultEvaluatorsDir),
		deriveFrom:   spec.DeriveFrom,
		instructions: spec.Instructions,
		traceDays:    spec.TraceDays,
	}
	if traceDays > 0 {
		entry.traceDays = traceDays
	}
	return entry
}

func newDatasetGenerateCommand() *cobra.Command {
	var (
		flags      generateFlags
		maxSamples int
	)

	cmd := &cobra.Command{
		Use:   "generate <name>",
		Short: "Generate a dataset and download it.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			if err := project.ValidateSampleSize(maxSamples); err != nil {
				return err
			}

			cfg, err := project.LoadGenerateConfig(flags.configPath)
			if err != nil {
				return err
			}
			declared := datasetGenEntry(cfg, name, maxSamples)

			plan, err := resolvePlan(&flags, cfg, name, declared)
			if err != nil {
				return err
			}
			if err := project.ValidateSampleSize(plan.SampleSize); err != nil {
				return err
			}

			ec, plan, err := prepareGeneration(cmd, &flags, plan, declared)
			if err != nil {
				return err
			}
			defer ec.Close()

			ref, err := ec.generateDataset(cmd.Context(), plan, cmd.OutOrStdout(), flags.noWait)
			if err != nil {
				return err
			}
			return reportGenerated(cmd, ref, flags.noWait)
		},
	}

	cmd.Flags().IntVar(&maxSamples, "max-samples", 0,
		fmt.Sprintf("Rows to synthesize (%d-%d).", project.MinSampleSize, project.MaxSampleSize))
	addGenerateFlags(cmd, &flags)
	return cmd
}

func newEvaluatorGenerateCommand() *cobra.Command {
	var (
		flags     generateFlags
		traceDays int
	)

	cmd := &cobra.Command{
		Use:   "generate <name>",
		Short: "Generate a rubric evaluator and download it.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			cfg, err := project.LoadGenerateConfig(flags.configPath)
			if err != nil {
				return err
			}
			declared := evaluatorGenEntry(cfg, name, traceDays)

			plan, err := resolvePlan(&flags, cfg, name, declared)
			if err != nil {
				return err
			}

			ec, plan, err := prepareGeneration(cmd, &flags, plan, declared)
			if err != nil {
				return err
			}
			defer ec.Close()

			ref, err := ec.generateRubric(cmd.Context(), plan, cmd.OutOrStdout(), flags.noWait)
			if err != nil {
				return err
			}
			return reportGenerated(cmd, ref, flags.noWait)
		},
	}

	cmd.Flags().IntVar(&traceDays, "trace-days", 0,
		"Days of traces to seed generation. 0 disables.")
	addGenerateFlags(cmd, &flags)
	return cmd
}

// reportGenerated closes out either command.
//
// With --no-wait nothing was downloaded and there is no ref, which is success:
// the job id was printed and `job show` reattaches to it.
func reportGenerated(cmd *cobra.Command, ref *project.ArtifactRef, noWait bool) error {
	out := cmd.OutOrStdout()
	if ref == nil {
		if noWait {
			fmt.Fprintln(out,
				"\nSubmitted. `azd ai eval job show <job-id>` reports its progress.")
			return nil
		}
		fmt.Fprintln(out, "Nothing was generated.")
		return nil
	}
	if isJSON(cmd) {
		return emitJSON(out, ref)
	}
	fmt.Fprintf(out, "\nReference it from your eval config as: %s\n", ref.Source)
	return nil
}
