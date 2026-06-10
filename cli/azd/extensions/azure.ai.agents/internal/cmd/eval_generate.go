// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// eval_generate.go implements the "eval generate" command, which generates a local
// eval suite (eval.yaml) for a deployed agent. It resolves context, submits
// dataset and evaluator generation jobs, polls for completion (unless
// --no-wait), downloads review artifacts, and writes the eval config.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opt_eval"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// DataGenerationAPIVersion is the API version used for data generation jobs.
const DataGenerationAPIVersion = "v1"

// evalGenerateFlags holds CLI flags and interactive prompt state for eval generate.
type evalGenerateFlags struct {
	// CLI flags.
	envName         string   // explicit environment name (from -e flag)
	name            string   // eval suite name
	agent           string   // target agent name
	projectEndpoint string   // Foundry project endpoint
	instruction     string   // inline agent instruction
	instructionFile string   // path to agent instruction file
	configFile      string   // agent config metadata path
	evalModel       string   // model for evaluation and generation
	dataset         string   // existing dataset file or name
	output          string   // eval config output path
	maxSamples      int      // number of samples to generate
	evaluators      []string // built-in or custom evaluator names
	noWait          bool     // submit and return immediately
	resetDefaults   bool     // overwrite existing eval config
	evalModelSet    bool     // true if --eval-model was explicitly set
	maxSamplesSet   bool     // true if --max-samples was explicitly set
	traceDays       int      // include traces from last N days

	// Internal state set during interactive prompts.
	regenerateDataset   bool
	regenerateEvaluator bool
}

func newEvalGenerateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &evalGenerateFlags{maxSamples: defaultEvalSamples, output: defaultEvalConfigName}
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a local eval suite for a deployed agent.",
		Long: `Generate a local eval suite for a deployed agent.

By default, this command submits dataset and evaluator generation jobs, waits for
completion, downloads review artifacts, and writes eval.yaml at
the agent project root. Use --no-wait to write pending operation IDs and return.`,
		Example: `  azd ai agent eval generate
  azd ai agent eval generate --gen-instruction "This agent handles restaurant reservations." --eval-model gpt-4o --max-samples 50
  azd ai agent eval generate --gen-instruction-file ./instructions.md --eval-model gpt-4o
  azd ai agent eval generate --dataset ./tests/golden.jsonl --evaluator builtin.intent_resolution`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			logCleanup := setupDebugLogging(cmd.Flags())
			defer logCleanup()
			flags.evalModelSet = cmd.Flags().Changed("eval-model")
			flags.maxSamplesSet = cmd.Flags().Changed("max-samples")
			flags.envName = extCtx.Environment
			return runEvalGenerate(ctx, flags, extCtx.NoPrompt)
		},
	}

	cmd.Flags().StringVar(&flags.name, "name", "", "Name for the eval suite")
	cmd.Flags().BoolVar(&flags.noWait, "no-wait", false, "Submit generation jobs and return immediately")
	cmd.Flags().StringVar(&flags.agent, "agent", "", "Target agent name")
	cmd.Flags().StringVarP(&flags.projectEndpoint, "project-endpoint", "p", "", "Microsoft Foundry project endpoint URL")
	cmd.Flags().StringVarP(&flags.instruction, "gen-instruction", "g", "", "Agent instruction used for dataset and evaluator generation")
	cmd.Flags().StringVarP(&flags.instructionFile, "gen-instruction-file", "", "", "Path to a file containing the agent instruction")
	cmd.Flags().StringVar(&flags.evalModel, "eval-model", "", "Model used for evaluation and generation")
	cmd.Flags().StringVar(&flags.dataset, "dataset", "", "Existing local file or registered dataset name to use for evaluation (instead of generating a new dataset)")
	cmd.Flags().IntVar(&flags.maxSamples, "max-samples", defaultEvalSamples, "Number of samples to generate (15-1000)")
	cmd.Flags().StringArrayVar(&flags.evaluators, "evaluator", nil, "Built-in or custom evaluator name")
	cmd.Flags().StringVar(&flags.output, "out-file", defaultEvalConfigName, "Eval config path")
	cmd.Flags().IntVar(&flags.traceDays, "trace-days", 0, "Include agent traces from the last N days for evaluator generation (0 = no traces)")
	cmd.Flags().BoolVar(&flags.resetDefaults, "reset-defaults", false, "Overwrite an existing eval config")

	return cmd
}

// runEvalGenerate executes the eval generate command logic. It resolves context,
// prompts for missing options, submits generation jobs, polls for completion
// (unless --no-wait), writes the eval config, and prints next steps.
func runEvalGenerate(ctx context.Context, flags *evalGenerateFlags, noPrompt bool) error {
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
		envName:         flags.envName,
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

	configPath := eval_api.ResolveRelPath(flags.output, resolved.agentProject)
	printEvalDetectedContext(resolved, configPath)

	// Load existing eval.yaml and resolve agent config.
	existingCfg, hasExisting := tryLoadExistingEvalConfig(configPath)
	isRegenerate := false

	// Resolve agent config: eval.yaml config → default baseline → nothing.
	if flags.instruction == "" && flags.instructionFile == "" && resolved.hasProject {
		var existing *opt_eval.Config
		if hasExisting && !flags.resetDefaults {
			existing = &existingCfg.Config
		}
		if agentCfg := resolveAgentConfig(existing, resolved.agentProject); agentCfg != nil {
			flags.configFile = agentCfg.ConfigFile
			flags.instructionFile = agentCfg.InstructionFile
			fmt.Printf("   Agent Config:     %s\n", filepath.Join(resolved.agentProject, agentCfg.ConfigFile))
		}
	}

	// If --reset-defaults is set, clear existing state so the user can start fresh.
	if flags.resetDefaults && resolved.envName != "" {
		if err := opt_eval.ClearEvalState(ctx, resolved.azdClient, resolved.envName); err != nil {
			log.Printf("warning: clearing eval state: %v", err)
		}
	}

	// Handle existing eval.yaml: prompt for regeneration, carry forward options.
	if hasExisting && !flags.resetDefaults {
		var keepExisting bool
		keepExisting, err = handleExistingEvalConfig(ctx, resolved, existingCfg, flags, noPrompt)
		if err != nil {
			return err
		}
		if keepExisting {
			fmt.Println("Keeping existing eval config unchanged.")
			return nil
		}
		isRegenerate = true
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

	if err := promptEvalGenerateOptions(ctx, resolved, flags, noPrompt); err != nil {
		return err
	}

	// Write baseline config if none was resolved but we have an instruction.
	if flags.configFile == "" && resolved.hasProject {
		instruction := resolvedInstruction(flags)
		if cfgFile := writeBaselineIfNeeded(resolved.agentProject, instruction); cfgFile != "" {
			flags.configFile = cfgFile
		}
	}

	if !isRegenerate {
		flags.name = resolveEvalName(flags)
	}

	if flags.instruction == "" && flags.instructionFile == "" && flags.configFile == "" &&
		(flags.dataset == "" || len(flags.evaluators) == 0) {
		return fmt.Errorf(
			"one of --gen-instruction, --gen-instruction-file, --config, or both --dataset and --evaluators is required" +
				" when generating eval assets for a hosted agent")
	}
	if flags.maxSamples < 15 || flags.maxSamples > 1000 {
		return fmt.Errorf("--max-samples must be between 15 and 1000")
	}

	// Build config and submit generation jobs.
	evalCfg := newEvalConfig(flags, resolved)
	var extraEvals opt_eval.EvaluatorList
	if !isRegenerate && len(flags.evaluators) > 0 {
		extraEvals = evaluatorsFromFlags(flags.evaluators)
	}

	state, err := submitEvalJobs(ctx, resolved, flags, evalCfg, existingCfg, isRegenerate)
	if err != nil {
		return err
	}

	if flags.noWait {
		if state.DatasetGenOpID != "" || state.EvalGenOpID != "" {
			state.InitStatus = opt_eval.InitStatusPending
		}
		return writePendingEvalGenerate(ctx, resolved, configPath, evalCfg, state)
	}

	pollRes, err := pollAndFinalizeJobs(ctx, resolved, evalCfg, state, extraEvals)
	if err != nil {
		if _, ok := errors.AsType[*initTimeoutError](err); ok {
			return writeTimedOutEvalGenerate(ctx, resolved, configPath, evalCfg, state)
		}
		return err
	}

	state.InitStatus = opt_eval.InitStatusCompleted
	if err := opt_eval.ClearEvalState(ctx, resolved.azdClient, resolved.envName); err != nil {
		log.Printf("warning: clearing eval state: %v", err)
	}
	return writeAndPrintEvalResult(ctx, resolved, evalCfg, pollRes, configPath, isRegenerate)
}

// handleExistingEvalConfig processes an existing eval.yaml by prompting for
// regeneration choices and carrying forward options that weren't overridden.
// Returns keepExisting=true if the user chose not to regenerate anything.
func handleExistingEvalConfig(
	ctx context.Context,
	resolved *evalResolvedContext,
	existingCfg *evalConfig,
	flags *evalGenerateFlags,
	noPrompt bool,
) (keepExisting bool, err error) {
	if noPrompt {
		// --no-prompt: keep existing config unchanged by default.
		return true, nil
	}

	if err := promptRegenerateChoices(ctx, resolved, existingCfg, flags); err != nil {
		return false, err
	}
	if !flags.regenerateDataset && !flags.regenerateEvaluator {
		return true, nil
	}

	// Carry forward existing options when not explicitly overridden.
	if flags.name == "" && existingCfg.Name != "" {
		flags.name = existingCfg.Name
	}
	if existingCfg.Options != nil && !flags.evalModelSet {
		flags.evalModel = existingCfg.Options.EvalModel
	}
	if !flags.maxSamplesSet && existingCfg.MaxSamples > 0 {
		flags.maxSamples = existingCfg.MaxSamples
	}
	if flags.traceDays == 0 && existingCfg.TraceDays > 0 {
		flags.traceDays = existingCfg.TraceDays
	}
	return false, nil
}

// submitEvalJobs determines which generation jobs are needed and submits them.
// It preserves existing config fields when regenerating only a subset.
func submitEvalJobs(
	ctx context.Context,
	resolved *evalResolvedContext,
	flags *evalGenerateFlags,
	evalCfg *evalConfig,
	existingCfg *evalConfig,
	isRegenerate bool,
) (*opt_eval.EvalState, error) {
	state := &opt_eval.EvalState{}

	var needDatasetGen, needEvalGen bool
	if isRegenerate {
		needDatasetGen = flags.regenerateDataset
		needEvalGen = flags.regenerateEvaluator
		if !needDatasetGen {
			evalCfg.DatasetFile = existingCfg.DatasetFile
			evalCfg.Config.Dataset = existingCfg.Config.Dataset
		}
		if !needEvalGen {
			evalCfg.Evaluators = existingCfg.Evaluators
		}
	} else {
		needDatasetGen = flags.dataset == ""
		needEvalGen = true
		if !needDatasetGen {
			datasetPath, err := resolveLocalDatasetFile(resolveCwdRelative(flags.dataset), resolved.agentProject)
			if err != nil {
				return nil, err
			}
			evalCfg.Dataset = &opt_eval.DatasetRef{
				LocalURI: datasetPath,
			}
		}
	}

	if needDatasetGen {
		job, err := submitDatasetGeneration(ctx, resolved, flags)
		if err != nil {
			return nil, err
		}
		state.DatasetGenOpID = job.OperationID()
		state.DatasetGenStatus = job.NormalizedStatus()
	}
	if needEvalGen {
		job, err := submitEvaluatorGeneration(ctx, resolved, flags)
		if err != nil {
			return nil, err
		}
		state.EvalGenOpID = job.OperationID()
		state.EvalGenStatus = job.NormalizedStatus()
	}

	return state, nil
}

// writeAndPrintEvalResult writes the eval config and review artifacts, then
// prints a summary of the generated assets along with portal links and
// next-step instructions.
func writeAndPrintEvalResult(
	ctx context.Context,
	resolved *evalResolvedContext,
	evalCfg *evalConfig,
	pollRes *pollResults,
	configPath string,
	isRegenerate bool,
) error {
	if err := eval_api.WriteEvalConfig(configPath, evalCfg); err != nil {
		return err
	}

	if resolved.hasProject {
		if err := eval_api.WriteEvalReviewArtifacts(resolved.agentProject, evalCfg); err != nil {
			log.Printf("warning: writing eval review artifacts: %v", err)
		}
	}
	if isRegenerate {
		fmt.Println(color.GreenString("\nEval suite regenerated"))
	} else {
		fmt.Println(color.GreenString("\nEval suite created"))
	}
	fmt.Printf("   Config:     %s\n", configPath)
	if localPath := evalCfg.LocalDatasetPath(); localPath != "" {
		fmt.Printf("   Dataset:    %s\n", localPath)
	} else if ref := evalCfg.RemoteDatasetReference(); ref != nil {
		ds := ref.Name
		if ref.Version != "" {
			ds += " (" + ref.Version + ")"
		}
		fmt.Printf("   Dataset:    %s\n", ds)
		if resolved.hasProject {
			fmt.Printf("               %s\n", eval_api.DatasetArtifactPath(resolved.agentProject, ref))
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

	printEvalDimensions(pollRes)
	printEvalPortalLinks(ctx, resolved, evalCfg)

	fmt.Println("\n   Next steps:")
	fmt.Printf("     %s\n", color.CyanString("azd ai agent eval run"))
	fmt.Printf("       Run the eval suite against your agent.\n")
	fmt.Printf("     %s\n", color.CyanString("azd ai agent eval update"))
	fmt.Printf("       Edit the generated dataset or evaluator locally, then upload changes.\n")
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
	if ref := evalCfg.RemoteDatasetReference(); ref != nil {
		fmt.Printf("\n   "+color.HiBlackString("Portal:")+"\n     Dataset:   %s\n",
			color.CyanString(prefix.DatasetURL(ref.Name, ref.Version)))
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
