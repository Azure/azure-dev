// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opteval"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// DataGenerationAPIVersion is the API version used for data generation jobs.
const DataGenerationAPIVersion = "v1"

// EvalInitFlags defines the customized flags for the eval init command.
type evalInitFlags struct {
	name            string
	agent           string
	projectEndpoint string
	instruction     string
	instructionFile string
	configFile      string
	skillDir        string
	toolsFile       string
	evalModel       string
	dataset         string
	output          string
	maxSamples      int
	evaluators      []string
	noWait          bool
	resetDefaults   bool
	evalModelSet    bool
	maxSamplesSet   bool
	traceDays       int
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
completion, downloads review artifacts, and writes eval.yaml at
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
	cmd.Flags().StringVarP(&flags.instruction, "gen-instruction", "g", "", "Agent instruction used for dataset and evaluator generation")
	cmd.Flags().StringVarP(&flags.instructionFile, "gen-instruction-file", "G", "", "Path to a file containing the agent instruction")
	cmd.Flags().StringVar(&flags.evalModel, "eval-model", defaultEvalModel, "Model used for evaluation and generation, and also as the default model for evaluation")
	cmd.Flags().StringVar(&flags.dataset, "dataset", "", "Existing local file or registered dataset name to use for evaluation (instead of generating a new dataset)")
	cmd.Flags().IntVar(&flags.maxSamples, "max-samples", defaultEvalSamples, "Number of samples to generate (15-1000)")
	cmd.Flags().StringArrayVar(&flags.evaluators, "evaluator", nil, "Built-in or custom evaluator name")
	cmd.Flags().StringVarP(&flags.output, "out-file", "O", defaultEvalConfigName, "Eval config path")
	cmd.Flags().IntVar(&flags.traceDays, "trace-days", 0, "Include agent traces from the last N days for evaluator generation (0 = no traces)")
	cmd.Flags().BoolVar(&flags.resetDefaults, "reset-defaults", false, "Overwrite an existing eval config")

	return cmd
}

// runEvalInit executes the eval init command logic. It resolves context, prompts for missing options, submits generation jobs, polls for completion (unless --no-wait), writes the eval config, and prints next steps.
func runEvalInit(ctx context.Context, flags *evalInitFlags, noPrompt bool) error {
	if flags.instruction != "" && flags.instructionFile != "" {
		return fmt.Errorf("cannot use both --gen-instruction and --gen-instruction-file; provide one or the other")
	}

	// Validate instruction file early when the path won't be resolved relative to a project.
	if flags.instructionFile != "" {
		if _, err := os.Stat(flags.instructionFile); err != nil && filepath.IsAbs(flags.instructionFile) {
			return fmt.Errorf("instruction file %q is not accessible: %w", flags.instructionFile, err)
		}
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

	// Resolve relative instruction file paths against the agent project directory.
	if flags.instructionFile != "" && !filepath.IsAbs(flags.instructionFile) {
		if resolved.projectRoot != "" {
			flags.instructionFile = filepath.Join(resolved.projectRoot, flags.instructionFile)
		}
		if _, err := os.Stat(flags.instructionFile); err != nil {
			return fmt.Errorf("instruction file %q is not accessible: %w", flags.instructionFile, err)
		}
	}

	configPath := eval_api.ResolveEvalOutputPath(flags.output, resolved.agentProject)
	printEvalDetectedContext(resolved, configPath)

	// Auto-detect agent config metadata if no instruction was provided.
	// This looks for .agent_configs/baseline/metadata.yaml and resolves
	// instruction and skill_dir from it.
	if flags.instruction == "" && flags.instructionFile == "" && resolved.hasProject {
		defaultConfigFile := filepath.Join(agentConfigsDir, "baseline", "metadata.yaml")
		absConfigFile := filepath.Join(resolved.agentProject, defaultConfigFile)
		if _, err := os.Stat(absConfigFile); err == nil {
			// Found a default config — resolve all fields from it.
			var agent opteval.AgentRef
			agent.ConfigFile = defaultConfigFile
			agent.ResolveFromConfig(resolved.agentProject)
			flags.configFile = defaultConfigFile
			flags.instructionFile = agent.Instruction.File
			flags.instruction = agent.Instruction.Value
			flags.skillDir = agent.SkillDir
			flags.toolsFile = agent.ToolsFile
			fmt.Printf("   Config:     %s\n", absConfigFile)
		}
	}

	// When eval.yaml exists, decide whether to regenerate or create fresh.
	existingCfg, hasExisting := tryLoadExistingEvalConfig(configPath)
	isRegenerate := false
	var builtinEvals opteval.EvaluatorList

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
		if flags.configFile == "" && existingCfg.Agent.ConfigFile != "" {
			flags.configFile = existingCfg.Agent.ConfigFile
			// Resolve all fields from the config for generation API calls.
			var agentRef opteval.AgentRef
			agentRef.ConfigFile = flags.configFile
			agentRef.ResolveFromConfig(resolved.agentProject)
			if flags.instruction == "" && flags.instructionFile == "" {
				flags.instructionFile = agentRef.Instruction.File
				flags.instruction = agentRef.Instruction.Value
			}
			if flags.skillDir == "" {
				flags.skillDir = agentRef.SkillDir
			}
			if flags.toolsFile == "" {
				flags.toolsFile = agentRef.ToolsFile
			}
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

	// If no baseline config exists yet and we have an instruction, write it
	// so that optimize can use it later.
	if flags.configFile == "" && resolved.hasProject &&
		(flags.instruction != "" || flags.instructionFile != "") {
		defaultConfigFile := filepath.Join(agentConfigsDir, "baseline", "metadata.yaml")
		absConfigFile := filepath.Join(resolved.agentProject, defaultConfigFile)
		if _, err := os.Stat(absConfigFile); err != nil {
			// Baseline doesn't exist — create it.
			instruction := resolvedInstruction(flags)
			if writeErr := writeBaselineFromEvalInit(
				resolved.agentProject, resolved.agentName, instruction,
			); writeErr != nil {
				fmt.Printf("   warning: failed to write baseline config: %s\n", writeErr)
			} else {
				flags.configFile = defaultConfigFile
				fmt.Printf("   Baseline:   %s\n", absConfigFile)
			}
		}
	}

	// Finalize the eval suite name. On fresh init, add a random suffix to
	// avoid collisions. On regeneration, keep the existing name.
	if !isRegenerate {
		flags.name = resolveEvalName(flags) + "-" + randomSuffix()
	}

	// Prompt agents use the agent source directly; hosted agents require an instruction.
	if resolved.agentKind != agent_yaml.AgentKindPrompt &&
		flags.instruction == "" && flags.instructionFile == "" && flags.configFile == "" &&
		(flags.dataset == "" || len(flags.evaluators) == 0) {
		return fmt.Errorf("--gen-instruction is required when generating eval assets for a hosted agent")
	}
	if flags.maxSamples < 15 || flags.maxSamples > 1000 {
		return fmt.Errorf("--max-samples must be between 15 and 1000")
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
		needEvalGen = true // always generate adaptive evaluator
		if !needDatasetGen {
			// User provided a local dataset file — use it directly.
			datasetPath, err := resolveLocalDatasetFile(flags.dataset, resolved.agentProject)
			if err != nil {
				return err
			}
			evalCfg.DatasetFile = datasetPath
		}
		// --evaluator values are merged with the generated adaptive evaluator.
		if len(flags.evaluators) > 0 {
			builtinEvals = evaluatorsFromFlags(flags.evaluators)
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

	pollRes, err := pollAndFinalizeJobs(ctx, resolved, evalCfg, state, builtinEvals)
	if err != nil {
		if _, ok := errors.AsType[*initTimeoutError](err); ok {
			return writeTimedOutEvalInit(ctx, resolved, configPath, evalCfg, state)
		}
		return err
	}

	state.InitStatus = "completed"
	clearEvalState(ctx, resolved.azdClient, resolved.envName)
	if err := eval_api.WriteEvalConfig(configPath, evalCfg); err != nil {
		return err
	}

	if resolved.hasProject {
		eval_api.WriteEvalReviewArtifacts(resolved.agentProject, evalCfg)
	}
	if isRegenerate {
		fmt.Println(color.GreenString("\nEval suite regenerated"))
	} else {
		fmt.Println(color.GreenString("\nEval suite created"))
	}
	fmt.Printf("   Config:     %s\n", configPath)
	if evalCfg.DatasetFile != "" {
		fmt.Printf("   Dataset:    %s\n", evalCfg.DatasetFile)
	} else if evalCfg.DatasetReference != nil && evalCfg.DatasetReference.Name != "" {
		ds := evalCfg.DatasetReference.Name
		if evalCfg.DatasetReference.Version != "" {
			ds += " (" + evalCfg.DatasetReference.Version + ")"
		}
		fmt.Printf("   Dataset:    %s\n", ds)
		if resolved.hasProject {
			fmt.Printf("               %s\n", eval_api.DatasetArtifactPath(resolved.agentProject, evalCfg.DatasetReference))
		}
	}
	for _, evaluator := range evalCfg.Evaluators {
		if evaluator.Name != "" {
			ev := evaluator.Name
			if evaluator.Version != "" {
				ev += " (" + evaluator.Version + ")"
			}
			fmt.Printf("   Evaluator:  %s\n", ev)
			if resolved.hasProject && !eval_api.IsBuiltinEvaluator(evaluator.Name) {
				fmt.Printf("               %s\n",
					filepath.Join(resolved.agentProject, eval_api.EvaluatorLocalURI(evaluator.Name)))
			}
		}
	}

	// Print evaluator rubric dimensions if available.
	printEvalDimensions(pollRes)

	// Print portal links.
	printEvalPortalLinks(ctx, resolved, evalCfg)

	fmt.Printf("\n   Review the generated assets, then run:\n     %s\n", color.CyanString("azd ai agent eval run"))
	return nil
}

// printEvalDimensions prints rubric dimensions from the poll results if available.
func printEvalDimensions(results *pollResults) {
	if results == nil || results.EvaluatorResult == nil {
		return
	}
	if len(results.EvaluatorResult.Definition.Dimensions) == 0 {
		return
	}
	eval_api.PrintEvaluatorDimensions(results.EvaluatorResult)
}

// printEvalPortalLinks prints Foundry portal links for the generated dataset and evaluator.
func printEvalPortalLinks(ctx context.Context, resolved *evalResolvedContext, evalCfg *evalConfig) {
	prefix := resolvePortalPrefix(ctx, resolved.azdClient, resolved.envName)
	if prefix == nil {
		return
	}
	hasLink := false
	if evalCfg.DatasetReference != nil && evalCfg.DatasetReference.Name != "" {
		fmt.Printf("\n   "+color.HiBlackString("Portal:")+"\n     Dataset:   %s\n",
			color.CyanString(prefix.DatasetURL(evalCfg.DatasetReference.Name, evalCfg.DatasetReference.Version)))
		hasLink = true
	}
	for _, evaluator := range evalCfg.Evaluators {
		if evaluator.Name != "" && !eval_api.IsBuiltinEvaluator(evaluator.Name) {
			if !hasLink {
				fmt.Println("\n   " + color.HiBlackString("Portal:"))
				hasLink = true
			}
			fmt.Printf("     Evaluator: %s\n",
				color.CyanString(prefix.EvaluatorURL(evaluator.Name, evaluator.Version)))
		}
	}
}
