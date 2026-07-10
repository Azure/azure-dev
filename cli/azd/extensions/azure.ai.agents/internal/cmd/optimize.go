// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// optimize.go implements the top-level "optimize" command, which submits
// agent optimization jobs. It resolves the agent, loads or builds a config,
// prompts for instruction/skills/model, and polls for results.
//
// Subcommands (status, list, cancel, apply, deploy) are registered here
// and implemented in their own files.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opt_eval"
	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// optimizeAgentContext holds the resolved agent name and project directory
// for an optimization operation.
type optimizeAgentContext struct {
	agentName    string // deployed agent name
	agentVersion string // deployed agent version (empty = latest)
	agentProject string // agent project directory (empty if not in an azd project)
	serviceName  string // azd service name (env key prefix source); empty for standalone --agent
}

// resolveOptimizeAgent resolves the agent name and project directory.
// Resolution order:
//  1. azd project context — if --agent is given it is matched as a service
//     name; otherwise the single agent service is auto-selected (or the user
//     is prompted). The Foundry agent name is read from the environment.
//  2. No azd project — --agent is treated as the Foundry agent name directly.
//  3. Error with guidance.
func resolveOptimizeAgent(ctx context.Context, flagValue, envName string, noPrompt bool) (*optimizeAgentContext, error) {
	// Try resolving from azd project first — --agent (if set) is interpreted
	// as a service name, consistent with show/delete/run.
	azdClient, err := azdext.NewAzdClient()
	if err == nil {
		defer azdClient.Close()

		svc, project, svcErr := resolveAgentService(ctx, azdClient, flagValue, noPrompt)
		if _, ok := errors.AsType[*azdext.LocalError](svcErr); ok {
			return nil, svcErr
		}
		if svcErr == nil && svc != nil && project != nil {
			agentProject := filepath.Join(project.Path, svc.RelativePath)
			serviceKey := toServiceKey(svc.Name)

			// Read agent name and version from azd environment.
			if env := getExistingEnvironment(ctx, envName, azdClient); env != nil {
				nameKey := fmt.Sprintf("AGENT_%s_NAME", serviceKey)
				if v, e := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
					EnvName: env.Name,
					Key:     nameKey,
				}); e == nil && v.Value != "" {
					version := ""
					versionKey := fmt.Sprintf("AGENT_%s_VERSION", serviceKey)
					if vv, ve := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
						EnvName: env.Name,
						Key:     versionKey,
					}); ve == nil {
						version = vv.Value
					}
					return &optimizeAgentContext{
						agentName:    v.Value,
						agentVersion: version,
						agentProject: agentProject,
						serviceName:  svc.Name,
					}, nil
				}
			}

			// Service resolved in the azd project, but no deployed agent name
			// was found in the environment. Return an explicit error so the
			// service name isn't silently reused as a Foundry agent name.
			return nil, fmt.Errorf(
				"service '%s' resolved in azd project but no deployed agent name found "+
					"(expected environment variable AGENT_%s_NAME) — run 'azd deploy' first",
				svc.Name, serviceKey,
			)
		}
	}

	// Outside an azd project (or project resolution failed): treat --agent as
	// the Foundry agent name directly, matching the eval without-project path.
	if flagValue != "" {
		return &optimizeAgentContext{agentName: flagValue}, nil
	}

	return nil, fmt.Errorf("agent name is required: use --agent <name>, or run from an azd project after 'azd deploy'")
}

// optimizeFlags holds CLI flags for the optimize (submit) command.
type optimizeFlags struct {
	configFile        string   // path to YAML config file
	agent             string   // agent name override
	dataset           string   // existing dataset file or registered dataset name
	evalModel         string   // model for evaluation
	optimizationModel string   // model for optimization reasoning (gpt-5 family)
	evaluators        []string // built-in or custom evaluator names
	maxCandidates     int      // max optimization candidates to generate
	noWait            bool     // return immediately after submission
	pollInterval      int      // polling interval in seconds
	optimizeConnectionFlags
}

// newOptimizeCommand creates the top-level "optimize" command and registers its subcommands.
func newOptimizeCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &optimizeFlags{}
	action := &OptimizeAction{flags: flags, envName: extCtx.Environment, noPrompt: extCtx.NoPrompt}

	cmd := &cobra.Command{
		Use:   "optimize [agent-name]",
		Short: "Evaluate and optimize AI agents.",
		Long: `Evaluate and optimize AI agents — baseline scoring and iterative improvement.

When run without a subcommand, submits an optimization job.
Use --config for a custom YAML spec, or just provide the agent name to use sensible defaults.`,
		Example: `  # Optimize (auto-detect agent from azd project)
  azd ai agent optimize

  # Optimize a specific agent
  azd ai agent optimize my-agent

  # Full control via config file
  azd ai agent optimize --config spec.yaml

  # Subcommands
  azd ai agent optimize status <id> --watch
  azd ai agent optimize list
  azd ai agent optimize cancel <id>
  azd ai agent optimize deploy --candidate <id>`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			// Positional arg fills in agent name
			if len(args) > 0 && flags.agent == "" {
				flags.agent = args[0]
			}

			// Read extCtx fields here (after PersistentPreRunE has populated them
			// from -e / AZD_ENVIRONMENT), not at command construction time.
			action.envName = extCtx.Environment
			action.noPrompt = extCtx.NoPrompt
			return action.Run(ctx, cmd)
		},
	}

	cmd.Flags().StringVarP(&flags.configFile, "config", "c", "", "Path to YAML config file (optional — values are prompted interactively if omitted)")
	cmd.Flags().StringVarP(&flags.agent, "agent", "a", "", "Agent service name from azure.yaml, or Foundry agent name outside a project")
	cmd.Flags().StringVarP(&flags.dataset, "dataset", "d", "", "Existing local file or registered dataset name")
	cmd.Flags().StringVarP(&flags.evalModel, "eval-model", "m", "", "Model for evaluation (required)")
	cmd.Flags().StringVar(&flags.optimizationModel, "optimize-model", "",
		"Model for optimization reasoning (gpt-5 family recommended; required)")
	cmd.Flags().StringArrayVar(&flags.evaluators, "evaluator", nil,
		"Built-in or custom evaluator name (repeatable; required when not set in config)")
	cmd.Flags().IntVar(&flags.maxCandidates, "max-candidates", 0, "Maximum number of optimization candidates to generate (must be >= 1; default: 5)")
	cmd.Flags().BoolVar(&flags.noWait, "no-wait", false, "Submit job and return immediately without waiting for completion")
	cmd.Flags().IntVar(&flags.pollInterval, "poll-interval", 10, "Polling interval in seconds")
	flags.optimizeConnectionFlags.register(cmd)

	cmd.AddCommand(newOptimizeStatusCommand(extCtx))
	cmd.AddCommand(newOptimizeListCommand(extCtx))
	cmd.AddCommand(newOptimizeCancelCommand(extCtx))
	cmd.AddCommand(newOptimizeApplyCommand(extCtx))
	cmd.AddCommand(newOptimizeDeployCommand(extCtx))

	return cmd
}

// OptimizeAction implements the optimize (submit job) command.
type OptimizeAction struct {
	flags       *optimizeFlags
	envName     string
	noPrompt    bool
	serviceName string // azd service name for per-agent env key derivation
}

// Run executes the optimize command: resolves the agent, loads/builds the config, applies overrides, submits the job, and optionally polls for results.
func (a *OptimizeAction) Run(ctx context.Context, cmd *cobra.Command) error {
	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "\n  %s Optimization creates candidate agents as draft versions.\n"+
		"  Your live agent versions are not affected until you explicitly deploy a candidate.\n\n",
		color.CyanString("Note:"))

	endpoint, err := a.flags.resolve(ctx, a.envName)
	if err != nil {
		return err
	}

	cfg, configSource, agentProject, err := a.resolveConfig(ctx)
	if err != nil {
		return err
	}
	hasProject := agentProject != ""

	if err := a.applyOverrides(ctx, cfg, agentProject); err != nil {
		return err
	}

	bold := color.New(color.Bold)

	_, _ = bold.Fprintf(out, "Optimizing agent %q...\n", cfg.Agent.Name)
	if configSource != "" {
		fmt.Fprintf(out, "  Config: %s\n", configSource)
	}

	resp, client, err := a.submitJob(ctx, out, endpoint, cfg, agentProject)
	if err != nil {
		return err
	}

	if !a.flags.noWait && !optimize_api.IsTerminal(resp.Status) {
		finalStatus, err := pollOptimizeJob(cmd, client, a.flags.pollInterval, resp.OperationID, a.envName)
		if err != nil {
			return err
		}
		printOptimizeResults(ctx, out, finalStatus, hasProject, a.envName)
	}

	return nil
}

// resolveConfig loads or builds an OptimizeConfig from flags, eval.yaml
// detection, and agent resolution. Returns the config, its source path
// (empty if using defaults), and the agent project directory.
func (a *OptimizeAction) resolveConfig(
	ctx context.Context,
) (cfg *OptimizeConfig, configSource, agentProject string, err error) {
	if a.flags.configFile != "" {
		cfg, err = LoadOptimizeConfig(a.flags.configFile)
		if err != nil {
			return nil, "", "", fmt.Errorf("%w\n\nCheck that the file path is correct and contains valid YAML", err)
		}

		// Even with explicit --config, try to reconcile agent name/version with the environment.
		resolved, resolveErr := resolveOptimizeAgent(ctx, a.flags.agent, a.envName, a.noPrompt)
		if resolveErr == nil {
			agentProject = resolved.agentProject
			a.serviceName = resolved.serviceName
			reconcileConfigAgent(os.Stderr, &cfg.Agent, resolved.agentName, resolved.agentVersion, a.flags.configFile)
		}

		return cfg, a.flags.configFile, agentProject, nil
	}

	resolved, err := resolveOptimizeAgent(ctx, a.flags.agent, a.envName, a.noPrompt)
	if err != nil {
		return nil, "", "", err
	}
	agentProject = resolved.agentProject
	a.serviceName = resolved.serviceName

	// Check if eval.yaml exists in the agent project and offer to use it.
	// In --no-prompt mode, use it automatically.
	if resolved.agentProject != "" {
		evalPath := filepath.Join(resolved.agentProject, defaultEvalConfigName)
		if _, statErr := os.Stat(evalPath); statErr == nil {
			useEval := a.noPrompt // auto-use in no-prompt mode
			if !a.noPrompt {
				azdClient, clientErr := azdext.NewAzdClient()
				if clientErr == nil {
					defer azdClient.Close()
					resp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
						Options: &azdext.ConfirmOptions{
							Message:      fmt.Sprintf("Found %s in project. Use it for optimization?", defaultEvalConfigName),
							DefaultValue: new(true),
						},
					})
					useEval = promptErr == nil && resp.Value != nil && *resp.Value
				}
			}
			if useEval {
				cfg, err = LoadOptimizeConfig(evalPath)
				if err != nil {
					return nil, "", "", fmt.Errorf("failed to load %s: %w", evalPath, err)
				}
				configSource = evalPath
			}
		}
	}

	if cfg == nil {
		cfg = defaultOptimizeConfig(resolved.agentName)
	} else {
		reconcileConfigAgent(os.Stderr, &cfg.Agent, resolved.agentName, resolved.agentVersion, configSource)
	}

	return cfg, configSource, agentProject, nil
}

// applyOverrides applies CLI flag overrides, resolves baseline agent config,
// and interactively fills missing instruction/skills/model values.
func (a *OptimizeAction) applyOverrides(
	ctx context.Context,
	cfg *OptimizeConfig,
	agentProject string,
) error {
	// Apply --dataset flag before anything else.
	if a.flags.dataset != "" {
		if eval_api.IsDatasetName(a.flags.dataset) {
			cfg.Dataset = &opt_eval.DatasetRef{Name: a.flags.dataset}
			cfg.DatasetFile = ""
		} else {
			resolved, err := resolveLocalDatasetFile(resolveCwdRelative(a.flags.dataset), agentProject)
			if err != nil {
				return err
			}
			cfg.DatasetFile = resolved
			cfg.Dataset = nil
		}
	}

	// Ensure Options is initialized.
	if cfg.Options == nil {
		cfg.Options = &opt_eval.Options{}
	}

	hasProject := agentProject != ""

	// CLI flags override config values (applied before prompts so prompts skip set values).
	if a.flags.evalModel != "" {
		cfg.Options.EvalModel = a.flags.evalModel
	}
	if a.flags.optimizationModel != "" {
		cfg.Options.OptimizationModel = a.flags.optimizationModel
	}
	if len(a.flags.evaluators) > 0 {
		// Append flag evaluators to any already in the config (deduped by name)
		// so --evaluator adds to config evaluators instead of replacing them.
		cfg.Evaluators = mergeEvaluators(cfg.Evaluators, evaluatorsFromFlags(a.flags.evaluators))
	}
	if a.flags.maxCandidates > 0 {
		cfg.Options.MaxCandidates = &a.flags.maxCandidates
	}

	// Resolve agent config: try existing config pointer, then default baseline.
	if hasProject {
		mergeAgentBaseline(cfg, agentProject)
	}

	// Create a single azd client for all interactive prompts and env lookups.
	// May be nil if running outside an azd project or if the gRPC connection fails.
	azdClient, _ := azdext.NewAzdClient()
	if azdClient != nil {
		defer azdClient.Close()
	}

	// If the model is still unknown, try the azd environment (set during deploy).
	if cfg.Agent.Model == "" && azdClient != nil {
		if m := getDeployedModelFromEnv(ctx, azdClient, a.envName); m != "" {
			cfg.Agent.Model = m
		}
	}

	// When baseline config is detected, show resolved values and let the user confirm.
	if cfg.Agent.ConfigFile != "" && hasProject && !a.noPrompt {
		if err := promptOptimizeConfigConfirmation(ctx, azdClient, cfg, agentProject); err != nil {
			return err
		}
	}

	// Resolve relative skill_dir against agent project directory.
	if cfg.SkillDir != "" && hasProject && !filepath.IsAbs(cfg.SkillDir) {
		cfg.SkillDir = filepath.Join(agentProject, cfg.SkillDir)
	}

	// Resolve relative tools_file against agent project directory.
	if cfg.ToolsFile != "" && hasProject && !filepath.IsAbs(cfg.ToolsFile) {
		cfg.ToolsFile = filepath.Join(agentProject, cfg.ToolsFile)
	}

	// Resolve agent instruction using a well-defined lifecycle:
	//  1. Config dir pointer (agent.config in eval.yaml) — resolves from metadata.yaml
	//  2. Config file (eval.yaml / --config) — instruction in the agent section (inline or file reference)
	//  3. Interactive prompt — ask the user to provide inline text or a file path
	if err := resolveOptimizeSystemPrompt(ctx, azdClient, cfg, agentProject, hasProject, a.noPrompt); err != nil {
		return err
	}

	// Resolve skill_dir: auto-detect, check baseline, or prompt user.
	if cfg.SkillDir == "" && hasProject {
		if err := resolveOptimizeSkillDir(ctx, azdClient, cfg, agentProject, a.noPrompt); err != nil {
			return err
		}
	}

	// Resolve eval_model: prompt user if not set.
	if cfg.Options.EvalModel == "" {
		if err := resolveOptimizeEvalModel(ctx, azdClient, cfg, a.noPrompt, a.envName); err != nil {
			return err
		}
	}

	// Resolve dataset: prompt user if neither file nor reference is set.
	if cfg.DatasetFile == "" && cfg.Dataset == nil {
		if err := resolveOptimizeDataset(ctx, azdClient, cfg, agentProject, a.noPrompt); err != nil {
			return err
		}
	}

	// Resolve optimization_config.model: prompt user if not set.
	if !hasModelConfig(cfg.Options.OptimizationConfig) && !a.noPrompt {
		if err := resolveOptimizeTargetModels(ctx, azdClient, cfg, a.envName); err != nil {
			return err
		}
	}

	// Resolve optimization_model: prompt user if not set.
	if cfg.Options.OptimizationModel == "" && !a.noPrompt {
		if err := resolveOptimizeOptimizationModel(ctx, azdClient, cfg, a.envName); err != nil {
			return err
		}
	}

	// Final validation after all overrides and prompts.
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	return nil
}

// mergeAgentBaseline resolves the baseline agent config and merges missing
// fields (instruction, model, skills, tools) into the OptimizeConfig.
func mergeAgentBaseline(cfg *OptimizeConfig, agentProject string) {
	var existing *opt_eval.Config
	if cfg.Agent.ConfigFile != "" {
		existing = &opt_eval.Config{Agent: cfg.Agent}
	}
	agentCfg := resolveAgentConfig(existing, agentProject)
	if agentCfg == nil {
		return
	}
	cfg.Agent.ConfigFile = agentCfg.ConfigFile
	if cfg.Agent.Instruction.IsEmpty() && agentCfg.InstructionFile != "" {
		cfg.Agent.Instruction.File = agentCfg.InstructionFile
	}
	if cfg.Agent.Model == "" {
		cfg.Agent.Model = agentCfg.Model
	}
	if cfg.SkillDir == "" {
		cfg.SkillDir = agentCfg.SkillDir
	}
	if cfg.ToolsFile == "" {
		cfg.ToolsFile = agentCfg.ToolsFile
	}
	if existing == nil {
		fmt.Printf("  Baseline:    %s\n", filepath.Join(agentProject, agentCfg.ConfigFile))
	}
}

// submitJob builds the optimization request, saves the baseline config,
// submits the job, and prints initial status.
func (a *OptimizeAction) submitJob(
	ctx context.Context,
	out io.Writer,
	endpoint string,
	cfg *OptimizeConfig,
	agentProject string,
) (*optimize_api.OptimizeResponse, *optimize_api.OptimizeClient, error) {
	credential, err := newAgentCredential()
	if err != nil {
		return nil, nil, err
	}

	client := optimize_api.NewOptimizeClient(endpoint, credential)

	// Resolve relative local dataset paths against the agent project directory
	// so configs with project-relative entries work regardless of the caller's
	// working directory.
	if agentProject != "" {
		if cfg.DatasetFile != "" && !filepath.IsAbs(cfg.DatasetFile) {
			cfg.DatasetFile = filepath.Join(agentProject, cfg.DatasetFile)
		}
		if cfg.Dataset.IsLocal() && !filepath.IsAbs(cfg.Dataset.LocalURI) {
			cfg.Dataset.LocalURI = filepath.Join(agentProject, cfg.Dataset.LocalURI)
		}
		if cfg.ValidationDataset.IsLocal() && !filepath.IsAbs(cfg.ValidationDataset.LocalURI) {
			cfg.ValidationDataset.LocalURI = filepath.Join(agentProject, cfg.ValidationDataset.LocalURI)
		}
	}

	optimizeReq, warnings, err := cfg.ToRequest()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build optimization request: %w", err)
	}

	for _, w := range warnings {
		fmt.Fprintf(out, "  warning: %s\n", w)
	}

	// Save baseline config before starting optimization.
	hasProject := agentProject != ""
	if hasProject {
		if err := writeBaselineConfig(agentProject, baselineParams{
			Model:       cfg.Agent.Model,
			Instruction: cfg.Agent.ResolvedSystemPrompt(),
			SkillDir:    cfg.SkillDir,
			ToolsFile:   cfg.ToolsFile,
		}); err != nil {
			fmt.Fprintf(out, "  warning: failed to save baseline config: %s\n", err)
		} else {
			baselineMetaPath := opt_eval.BaselineConfigRelPath()
			fmt.Fprintf(out, "  Baseline saved to %s\n", baselineMetaPath)
			if cfg.Agent.ConfigFile == "" {
				cfg.Agent.ConfigFile = baselineMetaPath
			}
		}
	}

	resp, err := client.StartOptimize(ctx, optimizeReq)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"failed to submit optimization job: %w\n\nCheck that the endpoint %q is reachable", err, endpoint)
	}

	fmt.Fprintf(out, "  Job ID: %s\n", color.CyanString(resp.OperationID))
	fmt.Fprintf(out, "  Status: %s\n", resp.Status)

	printOptimizePortalLink(ctx, out, cfg.Agent.Name, resp.OperationID, a.envName)
	fmt.Fprintln(out)

	saveLastOptimizeJobID(ctx, optimizeEnvKeyName(a.serviceName, cfg.Agent.Name), resp.OperationID, a.envName)

	return resp, client, nil
}

// pollOptimizeJob polls the optimization job until it reaches a terminal state.
// While polling it renders a live-updating candidate results table that shows
// completed candidates, the candidate currently being evaluated, and any
// queued candidates. The table is redrawn in place on each poll tick.
func pollOptimizeJob(
	cmd *cobra.Command,
	client *optimize_api.OptimizeClient,
	pollInterval int,
	operationID string,
	envName string,
) (*optimize_api.OptimizeJobStatus, error) {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()
	spinFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	frameIdx := 0

	// Track how many candidate rows have already been printed so we only
	// append new rows when new candidates complete.
	printedCandidates := 0
	headerPrinted := false
	hasStatusLine := false

	// Create azdClient once for eval URL lookups across all candidates.
	var (
		evalAzdClient   *azdext.AzdClient
		evalEnvResolved string
	)
	if c, err := azdext.NewAzdClient(); err == nil {
		evalAzdClient = c
		if env := getExistingEnvironment(ctx, envName, c); env != nil {
			evalEnvResolved = env.Name
		}
	}

	poller := &optimize_api.Poller{
		Client:      client,
		OperationID: operationID,
		Interval:    time.Duration(pollInterval) * time.Second,
		OnProgress: func(status *optimize_api.OptimizeJobStatus) {
			spin := spinFrames[frameIdx%len(spinFrames)]
			frameIdx++
			candidates := status.Candidates()

			// Erase the previous status line (spinner / "Evaluating…") so the
			// new candidate row or header takes its place.
			if hasStatusLine {
				fmt.Fprintf(out, "\033[1A\033[2K")
				hasStatusLine = false
			}

			// Print the table header once before the first candidate row.
			// Eval and Strategy columns are always shown during polling.
			if !headerPrinted && len(candidates) > 0 {
				headerPrinted = true
				header, sep := candidateTableHeader(true, true)
				fmt.Fprintln(out, header)
				fmt.Fprintln(out, sep)
			}

			bestName := ""
			if best := status.BestCandidate(); best != nil {
				bestName = best.Name
			}

			// Append rows for newly completed candidates. Eval URLs are
			// constructed per-candidate on the spot using the lazily
			// initialized azdClient.
			for i := printedCandidates; i < len(candidates); i++ {
				c := candidates[i]
				isBest := bestName != "" && c.Name == bestName
				evalCell := ""
				if c.EvalID != "" && c.EvalRunID != "" && evalAzdClient != nil {
					if url := buildEvalReportURL(ctx, evalAzdClient, evalEnvResolved, c.EvalID, c.EvalRunID); url != "" {
						evalCell = terminalHyperlink(url, "View")
					}
				}
				line := formatCandidateRow(
					candidateDisplayName(c.Name, isBest), c.AvgScore,
					evalCell, c.MutationKeys(), true, true)
				writeCandidateRow(out, line, isBest)
			}
			printedCandidates = len(candidates)

			// Print or update the in-progress status line. This single line
			// gets overwritten on the next tick.
			if !optimize_api.IsTerminal(status.Status) {
				var statusLine string
				if status.Progress != nil && status.Progress.InProgressCandidate != nil {
					inProg := status.Progress.InProgressCandidate
					phase := "Generating"
					if inProg.CandidateGenerated {
						phase = "Evaluating"
					}
					num := status.Progress.CandidatesCompleted + 1
					progress := fmt.Sprintf("%d", num)
					if status.Inputs != nil && status.Inputs.Options.MaxCandidates != nil {
						progress = fmt.Sprintf("%d/%d", num, *status.Inputs.Options.MaxCandidates)
					}
					statusLine = fmt.Sprintf("  %s %s candidate %s", spin, phase, progress)
					if inProg.CreatedAt > 0 {
						elapsed := time.Since(time.Unix(inProg.CreatedAt, 0)).Truncate(time.Second)
						statusLine += fmt.Sprintf(" · %s elapsed", elapsed)
					}
				} else {
					statusLine = fmt.Sprintf("  %s Waiting for candidates…", spin)
					if status.CreatedAt > 0 {
						elapsed := time.Since(time.Unix(status.CreatedAt, 0)).Truncate(time.Second)
						statusLine += fmt.Sprintf(" · %s elapsed", elapsed)
					}
				}
				fmt.Fprintln(out, statusLine)
				hasStatusLine = true
			}
		},
	}

	finalStatus, err := poller.PollUntilDone(ctx)

	// Clean up the lazily initialized azdClient.
	if evalAzdClient != nil {
		evalAzdClient.Close()
	}

	if err != nil {
		return nil, fmt.Errorf("failed while polling optimization job: %w", err)
	}

	// Clear the last status line so the final results table prints cleanly.
	if hasStatusLine {
		fmt.Fprintf(out, "\033[1A\033[2K")
	}

	// Print total job duration from server timestamps.
	if finalStatus.CreatedAt != 0 && finalStatus.UpdatedAt != 0 && finalStatus.UpdatedAt > finalStatus.CreatedAt {
		duration := time.Duration(finalStatus.UpdatedAt-finalStatus.CreatedAt) * time.Second
		fmt.Fprintf(out, "\n  Total time: %s\n", duration)
	}

	return finalStatus, nil
}

// printOptimizeResults prints the optimization results table and next-step commands.
func printOptimizeResults(ctx context.Context, out io.Writer, status *optimize_api.OptimizeJobStatus, hasProject bool, envName string) {
	if status.Error != nil {
		fmt.Fprintf(out, "\n  %s %s\n", color.RedString("Error:"), status.Error.Message)
	}

	if len(status.Candidates()) == 0 {
		return
	}

	bold := color.New(color.Bold)

	_, _ = bold.Fprintln(out, "\nResults:")
	// Resolve eval portal prefix once for building hyperlinks in the table.
	candidates := status.Candidates()
	evalURLs := buildCandidateEvalURLs(ctx, candidates, envName)
	hasEvalLinks := len(evalURLs) > 0

	best := status.BestCandidate()
	bestName := ""
	if best != nil {
		bestName = best.Name
	}

	// Show the Strategy column only when at least one candidate reports the
	// mutated agent attributes. It is placed last so it can grow freely.
	hasStrategy := false
	for _, c := range candidates {
		if len(c.Mutations) > 0 {
			hasStrategy = true
			break
		}
	}

	header, sep := candidateTableHeader(hasEvalLinks, hasStrategy)
	fmt.Fprintln(out, header)
	fmt.Fprintln(out, sep)

	for _, c := range candidates {
		isBest := c.Name == bestName
		evalCell := ""
		if hasEvalLinks {
			if url, ok := evalURLs[c.Name]; ok {
				evalCell = terminalHyperlink(url, "View")
			}
		}
		line := formatCandidateRow(
			candidateDisplayName(c.Name, isBest), c.AvgScore,
			evalCell, c.MutationKeys(), hasEvalLinks, hasStrategy)
		writeCandidateRow(out, line, isBest)
	}

	// Print candidate IDs for deploy
	hasIDs := false
	for _, c := range candidates {
		if c.CandidateID != "" {
			if !hasIDs {
				fmt.Fprintf(out, "\n  Candidate IDs:\n")
				hasIDs = true
			}
			marker := "  "
			if c.Name == bestName {
				marker = "★ "
			}
			fmt.Fprintf(out, "    %s%-20s %s\n", marker, c.Name, c.CandidateID)
		}
	}

	// Print next-step commands for best candidate
	if best != nil && best.CandidateID != "" {
		agentName := status.AgentName()
		if hasProject {
			fmt.Fprintf(out, "\n  Apply the best candidate locally, then deploy:\n")
			fmt.Fprintf(out, "    azd ai agent optimize apply --candidate %s\n", best.CandidateID)
			fmt.Fprintf(out, "    azd deploy\n")
		} else {
			fmt.Fprintf(out, "\n  Deploy the best candidate:\n")
			fmt.Fprintf(out, "    azd ai agent optimize deploy --candidate %s --agent %s\n",
				best.CandidateID, agentName)
		}
	}
	fmt.Fprintln(out)
}

// formatOptimizeStatus returns a colorized string for the given job status.
func formatOptimizeStatus(status string) string {
	switch status {
	case optimize_api.StatusCompleted, optimize_api.StatusSucceeded:
		return color.GreenString(status)
	case optimize_api.StatusFailed:
		return color.RedString(status)
	case optimize_api.StatusCancelled:
		return color.YellowString(status)
	case optimize_api.StatusRunning, optimize_api.StatusInProgress:
		return color.CyanString(status)
	case optimize_api.StatusPending, optimize_api.StatusQueued:
		return color.BlueString(status)
	default:
		return status
	}
}

// truncateString truncates s to maxLen characters, appending "..." if trimmed.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
