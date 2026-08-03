// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

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
	cmd.Flags().StringVar(&f.target, "target", "", "Agent whose context seeds generation.")
	cmd.Flags().StringVar(&f.instruction, "agent-instruction", "",
		"What the agent does and what to test.")
	cmd.Flags().StringVar(&f.instructionFile, "agent-instruction-file", "",
		"Read the agent instruction from this file. Mutually exclusive with --agent-instruction.")
	cmd.MarkFlagsMutuallyExclusive("agent-instruction", "agent-instruction-file")
	cmd.Flags().StringVar(&f.model, "generation-model", "",
		"Model deployment that generates the artifact.")
	cmd.Flags().StringVar(&f.outputDir, "output-dir", project.DefaultEvalDir,
		"Directory the generated artifact is written under.")
	cmd.Flags().BoolVar(&f.noWait, "no-wait", false,
		"Submit the job and return its id without polling.")
	cmd.Flags().StringVar(&f.endpoint, "project-endpoint", "", "Foundry project endpoint.")
}

// prepareGeneration resolves everything both commands need before they diverge.
//
// The model check happens here rather than at the service, because a generation
// job is billed against a deployment and a rejection partway through the
// command says less than a refusal at the flag that caused it.
func prepareGeneration(
	cmd *cobra.Command,
	f *generateFlags,
	maxSamples, traceDays int,
) (*evalContext, *project.GenerateConfig, string, error) {
	instruction, err := resolveInstruction(f.instruction, f.instructionFile)
	if err != nil {
		return nil, nil, "", err
	}

	cfg, err := resolveGenerateConfig(
		f.configPath, f.target, f.model, "", maxSamples, traceDays,
	)
	if err != nil {
		return nil, nil, "", err
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, "", err
	}
	if !isJSON(cmd) {
		warnIgnoredTraceFields(cfg, cmd.OutOrStdout())
	}
	if generationModel(cfg) == "" {
		return nil, nil, "", fmt.Errorf(
			"a model deployment is required to generate: pass --generation-model, " +
				"or set it in the generation spec")
	}

	ctx := cmd.Context()
	ec, err := newEvalContext(ctx, f.endpoint)
	if err != nil {
		return nil, nil, "", err
	}

	instruction, err = ec.resolveGenerationInstruction(
		ctx, cfg, instruction, f.configPath, cmd.OutOrStdout(), isJSON(cmd),
	)
	if err != nil {
		ec.Close()
		return nil, nil, "", err
	}
	return ec, cfg, instruction, nil
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

			ec, cfg, instruction, err := prepareGeneration(cmd, &flags, maxSamples, 0)
			if err != nil {
				return err
			}
			defer ec.Close()

			if cfg.Generate.Dataset == nil {
				return fmt.Errorf("the generation spec declares no dataset to generate")
			}
			cfg.Generate.Dataset.Name = name

			ref, err := ec.generateDataset(
				cmd.Context(), cfg, instruction, flags.outputDir, cmd.OutOrStdout(), flags.noWait)
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

			ec, cfg, instruction, err := prepareGeneration(cmd, &flags, 0, traceDays)
			if err != nil {
				return err
			}
			defer ec.Close()

			if cfg.Generate.Rubric == nil {
				return fmt.Errorf("the generation spec declares no rubric to generate")
			}
			cfg.Generate.Rubric.Name = name

			ref, err := ec.generateRubric(
				cmd.Context(), cfg, instruction, flags.outputDir, cmd.OutOrStdout(), flags.noWait)
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
