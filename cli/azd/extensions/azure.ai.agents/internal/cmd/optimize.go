// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// optimize.go implements the top-level "optimize" command, which submits
// agent optimization jobs. It resolves the agent, loads or builds a config,
// resolves any required local inputs, and polls for results.
//
// Subcommands (status, list, cancel, apply, deploy) are registered here
// and implemented in their own files.

package cmd

import (
	"context"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"time"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/optconfig"
	"azureaiagent/internal/pkg/agents/optimize_api"
	projectpkg "azureaiagent/internal/project"

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
	promptAgent  bool   // prompt agents use their deployed service-side definition
	project      *azdext.ProjectConfig
	service      *azdext.ServiceConfig
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
		if isServiceGateError(svcErr) {
			return nil, svcErr
		}
		if svcErr == nil && svc != nil && project != nil {
			agentProject := filepath.Join(project.Path, svc.RelativePath)
			serviceKey := toServiceKey(svc.Name)
			validation, validationErr := projectpkg.ValidateAgentServiceDefinition(svc, project.Path)
			if validationErr != nil {
				return nil, validationErr
			}
			promptAgent := validation.Kind == agent_yaml.AgentKindPrompt

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
						promptAgent:  promptAgent,
						project:      project,
						service:      svc,
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
	output            string
	configFile        string // path to YAML config file
	evalConfigFile    string
	evalName          string
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
Use --eval to reuse an agent-targeted evaluation from an azure.ai.eval service,
or --eval-config to select an evaluation configuration file.
Use --config for an optimization request file (YAML or JSON).
Omitted optional optimization settings use service defaults.
Legacy agent eval.yaml files are not supported.`,
		Example: `  # Optimize (auto-detect agent from azd project)
  azd ai agent optimize

  # Optimize a specific agent
  azd ai agent optimize my-agent

  # Full control via config file
  azd ai agent optimize --config spec.yaml

  # Reuse an evaluation from the new evaluation workflow
  azd ai agent optimize --eval quality --optimize-model optimizer-deployment

  # Subcommands
  azd ai agent optimize status <id> --watch
  azd ai agent optimize list
  azd ai agent optimize cancel <id>
  azd ai agent optimize deploy --candidate <id>`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOptimizeCommandFlags(cmd, flags, args); err != nil {
				return err
			}
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			// Positional arg fills in agent name
			if len(args) > 0 && flags.agent == "" {
				flags.agent = args[0]
			}

			// Read extCtx fields here (after PersistentPreRunE has populated them
			// from -e / AZD_ENVIRONMENT), not at command construction time.
			action.envName = extCtx.Environment
			flags.output = extCtx.OutputFormat
			action.noPrompt = extCtx.NoPrompt || flags.output == "json"
			return action.Run(ctx, cmd)
		},
	}

	cmd.Flags().StringVarP(&flags.configFile, "config", "c", "", "Optimization request file (YAML or JSON)")
	cmd.Flags().StringVar(&flags.evalConfigFile, "eval-config", "", "Evaluation configuration from 'azd ai eval init'")
	cmd.Flags().StringVar(&flags.evalName, "eval", "", "Evaluation name to reuse from the selected evaluation configuration")
	cmd.Flags().StringVarP(&flags.agent, "agent", "a", "",
		"Agent service name from azure.yaml, or Foundry agent name outside a project")
	cmd.Flags().StringVarP(&flags.dataset, "dataset", "d", "", "Local JSONL file or registered dataset as name:version")
	cmd.Flags().StringVarP(&flags.evalModel, "eval-model", "m", "", "Model for evaluation (required)")
	cmd.Flags().StringVar(&flags.optimizationModel, "optimize-model", "",
		"Model for optimization reasoning (gpt-5 family recommended; required)")
	cmd.Flags().StringArrayVar(&flags.evaluators, "evaluator", nil,
		"Built-in or custom evaluator name (repeatable; required when not set in config)")
	cmd.Flags().IntVar(&flags.maxCandidates, "max-candidates", 0,
		"Maximum number of optimization candidates to generate (must be >= 1; service default when omitted)")
	cmd.Flags().BoolVar(&flags.noWait, "no-wait", false, "Submit job and return immediately without waiting for completion")
	cmd.Flags().IntVar(&flags.pollInterval, "poll-interval", 10, "Polling interval in seconds")
	flags.optimizeConnectionFlags.register(cmd)
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})

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
	promptAgent bool   // deployed prompt agents use the service-side definition
}

// Run resolves inputs, submits a GA optimization job, and optionally polls for results.
func (a *OptimizeAction) Run(ctx context.Context, cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	jsonOutput := a.flags.output == "json"
	if jsonOutput {
		out = cmd.ErrOrStderr()
	}

	fmt.Fprintf(out, "\n  %s Optimization creates candidate agents as draft versions.\n"+
		"  Your live agent versions are not affected until you explicitly deploy a candidate.\n\n",
		color.CyanString("Note:"))

	cfg, configSource, agentProject, err := a.resolveConfig(ctx)
	if err != nil {
		return err
	}
	hasProject := agentProject != ""

	if err := a.applyOverrides(ctx, cfg, agentProject); err != nil {
		return err
	}
	endpoint, err := a.flags.resolve(ctx, a.envName)
	if err != nil {
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

	finalStatus := resp
	polled := false
	if !a.flags.noWait && !optimize_api.IsTerminal(resp.Status) {
		finalStatus, err = pollOptimizeJob(cmd, client, a.flags.pollInterval, resp.ID, a.envName, jsonOutput)
		if err != nil {
			return err
		}
		polled = true
	}
	if jsonOutput {
		if err := printOptimizeStatusJSON(cmd.OutOrStdout(), finalStatus); err != nil {
			return err
		}
	} else if optimize_api.IsTerminal(finalStatus.Status) {
		if !polled {
			if err := client.LoadCandidates(ctx, finalStatus); err != nil {
				return fmt.Errorf("failed to load optimization candidates: %w", err)
			}
		}
		printOptimizeResults(ctx, out, finalStatus, hasProject, a.envName)
	}
	return optimizeJobError(finalStatus)
}

// resolveConfig loads a GA request or reuses a canonical eval configuration.
// Returns the config, its source path
// (empty if using defaults), and the agent project directory.
func (a *OptimizeAction) resolveConfig(
	ctx context.Context,
) (cfg *OptimizeConfig, configSource, agentProject string, err error) {
	if a.flags.configFile != "" {
		cfg, err = LoadOptimizeConfig(a.flags.configFile)
		if err != nil {
			return nil, "", "", fmt.Errorf("%w\n\nCheck that the file path is correct and contains valid YAML", err)
		}

		// Explicit GA target identities and pins are authoritative.
		resolved, resolveErr := resolveOptimizeAgent(ctx, a.flags.agent, a.envName, true)
		if resolveErr != nil && a.flags.agent != "" {
			return nil, "", "", resolveErr
		}
		if resolveErr == nil && (a.flags.agent != "" || cfg.Agent.Name == resolved.agentName) {
			if err := projectpkg.RequireAgentFeatures(resolved.service, resolved.project.GetPath()); err != nil {
				return nil, "", "", err
			}
			agentProject = resolved.agentProject
			a.serviceName = resolved.serviceName
			a.promptAgent = resolved.promptAgent
			if a.flags.agent != "" {
				cfg.Agent.Name = resolved.agentName
				cfg.Agent.Version = resolved.agentVersion
			}
		}

		return cfg, a.flags.configFile, agentProject, nil
	}

	resolved, err := resolveOptimizeAgent(ctx, a.flags.agent, a.envName, a.noPrompt)
	if err != nil {
		return nil, "", "", err
	}
	if err := projectpkg.RequireAgentFeatures(resolved.service, resolved.project.GetPath()); err != nil {
		return nil, "", "", err
	}
	agentProject = resolved.agentProject
	a.serviceName = resolved.serviceName
	a.promptAgent = resolved.promptAgent

	var choices []optimizeEvalChoice
	if a.flags.evalConfigFile != "" {
		evalConfig, err := readOptimizeEvalConfig(a.flags.evalConfigFile)
		if err != nil {
			return nil, "", "", err
		}
		choices, err = optimizeEvalChoices(
			evalConfig, a.flags.evalConfigFile, a.flags.evalName, resolved.agentName, resolved.serviceName)
		if err != nil {
			return nil, "", "", err
		}
	} else if resolved.project != nil {
		choices, err = projectOptimizeEvalChoices(
			resolved.project, a.flags.evalName, resolved.agentName, resolved.serviceName)
		if err != nil {
			return nil, "", "", err
		}
	}
	if len(choices) == 0 {
		if a.flags.evalName != "" || a.flags.evalConfigFile != "" {
			return nil, "", "", fmt.Errorf("no evaluation matches the selected eval and agent")
		}
		cfg = defaultOptimizeConfig(resolved.agentName)
		cfg.Agent.Version = resolved.agentVersion
	} else {
		index := 0
		if len(choices) > 1 {
			if a.noPrompt {
				return nil, "", "", fmt.Errorf("multiple evaluations match; specify --eval and, " +
					"if necessary, --eval-config to select one")
			}
			client, err := azdext.NewAzdClient()
			if err != nil {
				return nil, "", "", fmt.Errorf("creating evaluation selection client: %w", err)
			}
			defer client.Close()
			items := make([]*azdext.SelectChoice, len(choices))
			for i, choice := range choices {
				items[i] = &azdext.SelectChoice{Label: choice.name, Value: choice.name}
			}
			response, err := client.Prompt().Select(ctx, &azdext.SelectRequest{
				Options: &azdext.SelectOptions{Message: "Select an evaluation for optimization", Choices: items},
			})
			if err != nil {
				return nil, "", "", fmt.Errorf("selecting evaluation: %w", err)
			}
			if response.Value == nil || *response.Value < 0 || int(*response.Value) >= len(choices) {
				return nil, "", "", fmt.Errorf("evaluation selection was cancelled")
			}
			index = int(*response.Value)
		}
		cfg, err = optimizeConfigFromEval(choices[index], resolved.agentName, resolved.agentVersion, a.flags.evalModel)
		if err != nil {
			return nil, "", "", err
		}
		configSource = choices[index].name
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
	if cfg.Request != nil && cfg.Request.OptimizationConfiguration.Type == "prompt_optimization" {
		if a.flags.optimizationModel != "" {
			cfg.Options.OptimizationModel = a.flags.optimizationModel
		}
		return cfg.Validate()
	}
	// Apply --dataset flag before anything else.
	if a.flags.dataset != "" {
		cfg.TrainingSet = nil
		file, ref, err := resolveOptimizeDatasetInput(a.flags.dataset, agentProject)
		if err != nil {
			return err
		}
		cfg.DatasetFile = file
		cfg.Dataset = ref
	}

	// Ensure Options is initialized.
	if cfg.Options == nil {
		cfg.Options = &optconfig.Options{}
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
	if cfg.Request != nil {
		return cfg.Validate()
	}

	// Resolve agent config: try existing config pointer, then default baseline.
	if hasProject && !a.promptAgent {
		mergeAgentBaseline(cfg, agentProject)
	}

	// Create a single azd client for all interactive prompts and env lookups.
	// May be nil if running outside an azd project or if the gRPC connection fails.
	azdClient, _ := azdext.NewAzdClient()
	if azdClient != nil {
		defer azdClient.Close()
	}

	// If the model is still unknown, try the azd environment (set during deploy).
	if !a.promptAgent && cfg.Agent.Model == "" && azdClient != nil {
		if m := getDeployedModelFromEnv(ctx, azdClient, a.envName); m != "" {
			cfg.Agent.Model = m
		}
	}

	// When baseline config is detected, show resolved values and let the user confirm.
	if !a.promptAgent && cfg.Agent.ConfigFile != "" && hasProject && !a.noPrompt {
		if err := promptOptimizeConfigConfirmation(ctx, azdClient, cfg, agentProject); err != nil {
			return err
		}
	}

	// Resolve relative skill_dir against agent project directory.
	if cfg.SkillDir != "" && hasProject && !filepath.IsAbs(cfg.SkillDir) {
		cfg.SkillDir = filepath.Join(agentProject, cfg.SkillDir)
	}

	// Resolve relative tools file paths against agent project directory.
	if cfg.ToolsFile != "" && hasProject && !filepath.IsAbs(cfg.ToolsFile) {
		cfg.ToolsFile = filepath.Join(agentProject, cfg.ToolsFile)
	}

	// Explicit GA inputs take precedence over a saved baseline and interactive input.
	if !a.promptAgent {
		if err := resolveOptimizeSystemPrompt(ctx, azdClient, cfg, agentProject, hasProject, a.noPrompt); err != nil {
			return err
		}
	}

	// Resolve skill_dir: auto-detect, check baseline, or prompt user.
	if !a.promptAgent && cfg.SkillDir == "" && hasProject {
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
	if cfg.DatasetFile == "" && cfg.Dataset == nil && cfg.TrainingSet == nil {
		if err := resolveOptimizeDataset(ctx, azdClient, cfg, agentProject, a.noPrompt); err != nil {
			return err
		}
	}

	// Resolve optimization_config.model: prompt user if not set.
	if !a.promptAgent && !hasModelConfig(cfg.Options.OptimizationConfig) && !a.noPrompt {
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
	var existing *optconfig.Config
	if cfg.Agent.ConfigFile != "" {
		existing = &optconfig.Config{Agent: cfg.Agent}
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

	requestConfig := optimizeRequestConfig(cfg, a.promptAgent)
	optimizeReq, warnings, err := requestConfig.ToRequest()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build optimization request: %w", err)
	}
	credential, err := newAgentCredential()
	if err != nil {
		return nil, nil, err
	}
	client := optimize_api.NewOptimizeClient(endpoint, credential)

	for _, w := range warnings {
		fmt.Fprintf(out, "  warning: %s\n", w)
	}

	// Save baseline config before starting optimization.
	hasProject := agentProject != ""
	if hasProject && !a.promptAgent {
		if err := cfg.saveBaseline(agentProject, optimizeReq); err != nil {
			fmt.Fprintf(out, "  warning: failed to save baseline config: %s\n", err)
		} else {
			baselineMetaPath := optconfig.BaselineConfigRelPath()
			fmt.Fprintf(out, "  Baseline saved to %s\n", baselineMetaPath)
			if cfg.Agent.ConfigFile == "" {
				cfg.Agent.ConfigFile = baselineMetaPath
			}
		}
	}

	resp, err := client.StartOptimize(ctx, optimizeReq)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"failed to submit optimization job: %w\n\nCheck that the project endpoint is reachable", err)
	}

	fmt.Fprintf(out, "  Job ID: %s\n", color.CyanString(resp.ID))
	fmt.Fprintf(out, "  Status: %s\n", resp.Status)

	printOptimizePortalLink(ctx, out, cfg.Agent.Name, resp.ID, a.envName)
	fmt.Fprintln(out)

	saveLastOptimizeJobID(ctx, optimizeEnvKeyName(a.serviceName, cfg.Agent.Name), resp.ID, a.envName)

	return resp, client, nil
}

func optimizeRequestConfig(cfg *OptimizeConfig, promptAgent bool) *OptimizeConfig {
	if !promptAgent || cfg.Request != nil {
		return cfg
	}

	requestConfig := *cfg
	requestConfig.Agent = cfg.Agent
	requestConfig.Agent.Model = ""
	requestConfig.Agent.Instruction = optconfig.InstructionRef{}
	requestConfig.SkillDir = ""
	requestConfig.ToolsFile = ""
	if cfg.Options != nil {
		options := *cfg.Options
		options.OptimizationConfig = maps.Clone(cfg.Options.OptimizationConfig)
		for _, key := range []string{"model", "system_prompt", "skills", "tools"} {
			delete(options.OptimizationConfig, key)
		}
		if len(options.OptimizationConfig) == 0 {
			options.OptimizationConfig = nil
		}
		requestConfig.Options = &options
	}

	return &requestConfig
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
	jsonOutput bool,
) (*optimize_api.OptimizeJobStatus, error) {
	if jsonOutput {
		poller := &optimize_api.Poller{
			Client: client, OperationID: operationID,
			Interval: time.Duration(pollInterval) * time.Second,
		}
		return poller.PollUntilDone(cmd.Context())
	}
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
		Client:            client,
		OperationID:       operationID,
		Interval:          time.Duration(pollInterval) * time.Second,
		IncludeCandidates: true,
		OnProgress: func(status *optimize_api.OptimizeJobStatus) {
			spin := spinFrames[frameIdx%len(spinFrames)]
			frameIdx++
			candidates := status.Candidates()
			completed := make([]optimize_api.CandidateResult, 0, len(candidates))
			for _, candidate := range candidates {
				if candidate.Status == "completed" {
					completed = append(completed, candidate)
				}
			}
			candidates = completed

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
				if c.Evaluation != nil && c.Evaluation.EvalID != "" &&
					c.Evaluation.EvalRunID != "" && evalAzdClient != nil {
					if url := buildEvalReportURL(ctx, evalAzdClient, evalEnvResolved,
						c.Evaluation.EvalID, c.Evaluation.EvalRunID); url != "" {
						evalCell = terminalHyperlink(url, "View")
					}
				}
				line := formatCandidateRow(
					candidateDisplayName(c.Name, isBest), c.Score(),
					evalCell, c.MutationKeys(), true, true)
				writeCandidateRow(out, line, isBest)
			}
			printedCandidates = len(candidates)

			// Print or update the in-progress status line. This single line
			// gets overwritten on the next tick.
			if !optimize_api.IsTerminal(status.Status) {
				var statusLine string
				statusLine = fmt.Sprintf("  %s %s", spin, status.Status)
				if status.ActivityProgress != nil {
					for _, activity := range status.ActivityProgress.Activities {
						if activity.Status == "in_progress" {
							statusLine += " · " + activity.Title
						}
					}
				}
				if status.RunDurationMS > 0 {
					statusLine += fmt.Sprintf(" · %s elapsed", time.Duration(status.RunDurationMS)*time.Millisecond)
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
		if len(c.MutationKeys()) > 0 {
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
			candidateDisplayName(c.Name, isBest), c.Score(),
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
	case optimize_api.StatusSucceeded:
		return color.GreenString(status)
	case optimize_api.StatusFailed:
		return color.RedString(status)
	case optimize_api.StatusCancelled, optimize_api.StatusSkipped:
		return color.YellowString(status)
	case optimize_api.StatusInProgress:
		return color.CyanString(status)
	case optimize_api.StatusQueued:
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
