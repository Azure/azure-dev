// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/eval_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// DataGenerationAPIVersion is the API version used for data generation jobs.
const DataGenerationAPIVersion = "v1"

// EvalInitFlags defines the customized flags for the eval init command.
type evalInitFlags struct {
	name               string
	agent              string
	projectEndpoint    string
	genInstruction     string
	genInstructionFile string
	evalModel          string
	dataset            string
	output             string
	maxSamples         int
	evaluators         []string
	noWait             bool
	resetDefaults      bool
	evalModelSet       bool
	maxSamplesSet      bool
	traceDays          int
	// Internal flags set during interactive prompts.
	regenerateDataset   bool
	regenerateEvaluator bool
}

func newEvalInitCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &evalInitFlags{evalModel: defaultEvalModel, maxSamples: defaultEvalSamples, output: defaultEvalConfigName}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate a local eval suite for a deployed agent.",
		Long: `Generate a local eval suite for a deployed agent.

By default, this command submits dataset and evaluator generation jobs, waits for
completion, downloads review artifacts under .azure/.foundry, and writes eval.yaml at
the agent project root. Use --no-wait to write pending operation IDs and return.`,
		Example: `  azd ai agent eval init
  azd ai agent eval init --gen-instruction "This agent handles restaurant reservations." --eval-model gpt-4o --max-samples 50
  azd ai agent eval init --gen-instruction-file ./instructions.md --eval-model gpt-4o
  azd ai agent eval init --dataset ./tests/golden.jsonl --evaluator builtin.intent_resolution`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			logCleanup := setupDebugLogging(cmd.Flags())
			defer logCleanup()
			flags.evalModelSet = cmd.Flags().Changed("eval-model")
			flags.maxSamplesSet = cmd.Flags().Changed("max-samples")
			return runEvalInit(ctx, flags, extCtx.NoPrompt)
		},
	}

	cmd.Flags().StringVar(&flags.name, "name", "", "Name for the eval suite")
	cmd.Flags().BoolVar(&flags.noWait, "no-wait", false, "Submit generation jobs and return immediately")
	cmd.Flags().StringVar(&flags.agent, "agent", "", "Target agent name")
	cmd.Flags().StringVarP(&flags.projectEndpoint, "project-endpoint", "p", "", "Microsoft Foundry project endpoint URL")
	cmd.Flags().StringVarP(&flags.genInstruction, "gen-instruction", "g", "", "Inline instruction for dataset and evaluator generation")
	cmd.Flags().StringVarP(&flags.genInstructionFile, "gen-instruction-file", "G", "", "Path to a file containing the generation instruction")
	cmd.Flags().StringVar(&flags.evalModel, "eval-model", defaultEvalModel, "Model used for evaluation and generation, and also as the default model for evaluation")
	cmd.Flags().StringVar(&flags.dataset, "dataset", "", "Existing local file or registered dataset name to use for evaluation (instead of generating a new dataset)")
	cmd.Flags().IntVar(&flags.maxSamples, "max-samples", defaultEvalSamples, "Maximum number of samples to generate")
	cmd.Flags().StringArrayVar(&flags.evaluators, "evaluator", nil, "Built-in or custom evaluator name")
	cmd.Flags().StringVarP(&flags.output, "out-file", "O", defaultEvalConfigName, "Eval config path")
	cmd.Flags().IntVar(&flags.traceDays, "trace-days", 0, "Include agent traces from the last N days for evaluator generation (0 = no traces)")
	cmd.Flags().BoolVar(&flags.resetDefaults, "reset-defaults", false, "Overwrite an existing eval config")

	return cmd
}

// runEvalInit executes the eval init command logic. It resolves context, prompts for missing options, submits generation jobs, polls for completion (unless --no-wait), writes the eval config, and prints next steps.
func runEvalInit(ctx context.Context, flags *evalInitFlags, noPrompt bool) error {
	if flags.genInstruction != "" && flags.genInstructionFile != "" {
		return fmt.Errorf("cannot use both --gen-instruction and --gen-instruction-file; provide one or the other")
	}
	if flags.genInstructionFile != "" {
		data, err := os.ReadFile(flags.genInstructionFile) //nolint:gosec // user-provided instruction file path
		if err != nil {
			return fmt.Errorf("reading instruction file %q: %w", flags.genInstructionFile, err)
		}
		flags.genInstruction = strings.TrimSpace(string(data))
	}

	resolved, err := resolveEvalContext(ctx, evalContextOptions{
		agent:           flags.agent,
		projectEndpoint: flags.projectEndpoint,
		requireAgent:    true,
		noPrompt:        noPrompt,
	})
	if err != nil {
		return err
	}
	defer resolved.azdClient.Close()

	configPath := resolveEvalOutputPath(flags.output, resolved.agentProject)
	printEvalDetectedContext(resolved, configPath)

	// When eval.yaml exists, decide whether to regenerate or create fresh.
	existingCfg, hasExisting := tryLoadExistingEvalConfig(configPath)
	isRegenerate := false
	var builtinEvals []string

	if flags.resetDefaults && resolved.envName != "" {
		clearEvalState(ctx, resolved.azdClient, resolved.envName)
	}

	if hasExisting && !flags.resetDefaults {
		if noPrompt {
			// --no-prompt: treat as full regeneration.
			flags.regenerateDataset = true
			flags.regenerateEvaluator = true
		} else {
			if err := promptRegenerateChoices(ctx, resolved, existingCfg, flags); err != nil {
				return err
			}
			if !flags.regenerateDataset && !flags.regenerateEvaluator {
				fmt.Println("Keeping existing eval config unchanged.")
				return nil
			}
		}
		isRegenerate = true

		// Carry forward existing options when not explicitly overridden.
		if flags.name == "" && existingCfg.Name != "" {
			flags.name = existingCfg.Name
		}
		if existingCfg.Options != nil && !flags.evalModelSet {
			flags.evalModel = existingCfg.Options.EvalModel
		}
		if flags.genInstruction == "" {
			flags.genInstruction = existingCfg.GenerationInstruction
		}
		if !flags.maxSamplesSet && existingCfg.MaxSamples > 0 {
			flags.maxSamples = existingCfg.MaxSamples
		}
		if flags.traceDays == 0 && existingCfg.TraceDays > 0 {
			flags.traceDays = existingCfg.TraceDays
		}
		// Track builtin evaluators for preservation during evaluator regeneration.
		if flags.regenerateEvaluator {
			_, builtinEvals = eval_api.SplitEvaluators(existingCfg.Evaluators)
		}
	}

	// When the user hasn't explicitly set --eval-model, use the deployed model.
	if !flags.evalModelSet && resolved.envName != "" {
		if v, err := resolved.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: resolved.envName,
			Key:     "AZURE_AI_MODEL_DEPLOYMENT_NAME",
		}); err == nil && v.Value != "" {
			flags.evalModel = v.Value
		}
	}

	if err := promptEvalInitOptions(ctx, resolved, flags, noPrompt); err != nil {
		return err
	}

	// Finalize the eval suite name with a random suffix to avoid collisions.
	flags.name = resolveEvalName(flags) + "-" + randomSuffix()

	// Prompt agents use the agent source directly; hosted agents require a gen-instruction.
	if resolved.agentKind != agent_yaml.AgentKindPrompt &&
		flags.genInstruction == "" && (flags.dataset == "" || len(flags.evaluators) == 0) {
		return fmt.Errorf("--gen-instruction is required when generating eval assets for a hosted agent")
	}
	if flags.maxSamples <= 0 {
		return fmt.Errorf("--max-samples must be a positive integer")
	}

	if resolved.hasProject {
		if err := ensureFoundryDirs(resolved.projectRoot); err != nil {
			return err
		}
	}

	evalCfg := newEvalConfig(flags, resolved)
	state := &evalState{}

	// Determine which generation jobs to submit.
	var needDatasetGen, needEvalGen bool
	if isRegenerate {
		needDatasetGen = flags.regenerateDataset
		needEvalGen = flags.regenerateEvaluator
		// Preserve fields that are not being regenerated.
		if !needDatasetGen {
			evalCfg.DatasetFile = existingCfg.DatasetFile
			evalCfg.Config.DatasetReference = existingCfg.Config.DatasetReference
		}
		if !needEvalGen {
			evalCfg.Evaluators = existingCfg.Evaluators
		}
	} else {
		needDatasetGen = flags.dataset == ""
		needEvalGen = len(flags.evaluators) == 0
		if !needDatasetGen {
			// User provided a local dataset file — use it directly.
			datasetPath, err := resolveLocalDatasetFile(flags.dataset, resolved.agentProject)
			if err != nil {
				return err
			}
			evalCfg.DatasetFile = datasetPath
		}
		if !needEvalGen {
			evalCfg.Evaluators = evaluatorsFromFlags(flags.evaluators)
		}
	}

	// Submit generation jobs (fast API calls).
	if needDatasetGen {
		job, err := submitDatasetGeneration(ctx, resolved, flags)
		if err != nil {
			return err
		}
		state.DatasetGenOpID = job.OperationID()
		state.DatasetGenStatus = job.NormalizedStatus()
	}
	if needEvalGen {
		job, err := submitEvaluatorGeneration(ctx, resolved, flags)
		if err != nil {
			return err
		}
		state.EvalGenOpID = job.OperationID()
		state.EvalGenStatus = job.NormalizedStatus()
	}

	if flags.noWait {
		if needDatasetGen || needEvalGen {
			state.InitStatus = "pending"
		}
		return writePendingEvalInit(ctx, resolved, configPath, evalCfg, state)
	}

	if err := pollAndFinalizeJobs(ctx, resolved, evalCfg, state, builtinEvals); err != nil {
		if _, ok := errors.AsType[*initTimeoutError](err); ok {
			return writeTimedOutEvalInit(ctx, resolved, configPath, evalCfg, state)
		}
		return err
	}

	state.InitStatus = "completed"
	clearEvalState(ctx, resolved.azdClient, resolved.envName)
	if err := writeEvalConfig(configPath, evalCfg); err != nil {
		return err
	}

	if resolved.hasProject {
		writeEvalReviewArtifacts(resolved.projectRoot, evalCfg)
	}
	if isRegenerate {
		fmt.Println(color.GreenString("Eval suite regenerated"))
	} else {
		fmt.Println(color.GreenString("Eval suite created"))
	}
	fmt.Printf("   Config:     %s\n", configPath)
	if evalCfg.DatasetFile != "" {
		fmt.Printf("   Dataset:    %s\n", evalCfg.DatasetFile)
	}
	for _, evaluator := range evalCfg.Evaluators {
		if evaluator != "" {
			fmt.Printf("   Evaluator:  %s\n", evaluator)
		}
	}
	fmt.Printf("\n   Review the generated assets, then run:\n     %s\n", "azd ai agent eval run")
	return nil
}
