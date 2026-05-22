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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	agentProject string // agent project directory (empty if not in an azd project)
}

// resolveOptimizeAgent resolves the agent name and project directory.
// Resolution order:
//  1. Explicit --agent flag
//  2. azd project context (resolveAgentService + environment variables)
//  3. Error with guidance
func resolveOptimizeAgent(ctx context.Context, flagValue string, noPrompt bool) (*optimizeAgentContext, error) {
	if flagValue != "" {
		return &optimizeAgentContext{agentName: flagValue}, nil
	}

	// Try resolving from azd project — single resolveAgentService call
	// to get both project path and agent info from environment.
	azdClient, err := azdext.NewAzdClient()
	if err == nil {
		defer azdClient.Close()

		svc, project, svcErr := resolveAgentService(ctx, azdClient, "", noPrompt)
		if svcErr == nil && svc != nil && project != nil {
			agentProject := filepath.Join(project.Path, svc.RelativePath)
			serviceKey := toServiceKey(svc.Name)

			// Read agent name from azd environment
			envResp, envErr := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
			if envErr == nil && envResp.Environment != nil {
				nameKey := fmt.Sprintf("AGENT_%s_NAME", serviceKey)
				if v, e := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
					EnvName: envResp.Environment.Name,
					Key:     nameKey,
				}); e == nil && v.Value != "" {
					return &optimizeAgentContext{
						agentName:    v.Value,
						agentProject: agentProject,
					}, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("agent name is required: use --agent <name>, or run from an azd project after 'azd deploy'")
}

// optimizeFlags holds CLI flags for the optimize (submit) command.
type optimizeFlags struct {
	configFile       string   // path to YAML config file
	agent            string   // agent name override
	evalModel        string   // model for evaluation
	targetAttributes []string // optimization targets (instruction, skill)
	noWait           bool     // return immediately after submission
	pollInterval     int      // polling interval in seconds
	optimizeConnectionFlags
}

// newOptimizeCommand creates the top-level "optimize" command and registers its subcommands.
func newOptimizeCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &optimizeFlags{}
	action := &OptimizeAction{flags: flags, noPrompt: extCtx.NoPrompt}

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

  # Optimize with skill target
  azd ai agent optimize --target skill

  # Optimize with multiple target attributes
  azd ai agent optimize --target instruction --target skill

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

			return action.Run(ctx, cmd)
		},
	}

	cmd.Flags().StringVarP(&flags.configFile, "config", "c", "", "Path to YAML config file (optional — uses defaults if omitted)")
	cmd.Flags().StringVarP(&flags.agent, "agent", "a", "", "Agent name (auto-detected from azd project if omitted)")
	cmd.Flags().StringVarP(&flags.evalModel, "eval-model", "m", defaultEvalModel, "Model for evaluation")
	cmd.Flags().StringArrayVarP(&flags.targetAttributes, "target", "t", nil,
		"Target attribute for optimization: instruction, skill (repeatable)")
	cmd.Flags().BoolVar(&flags.noWait, "no-wait", false, "Submit job and return immediately without waiting for completion")
	cmd.Flags().IntVar(&flags.pollInterval, "poll-interval", 5, "Polling interval in seconds")
	flags.optimizeConnectionFlags.register(cmd)

	cmd.AddCommand(newOptimizeStatusCommand())
	cmd.AddCommand(newOptimizeListCommand())
	cmd.AddCommand(newOptimizeCancelCommand())
	cmd.AddCommand(newOptimizeApplyCommand(extCtx))
	cmd.AddCommand(newOptimizeDeployCommand())

	return cmd
}

// OptimizeAction implements the optimize (submit job) command.
type OptimizeAction struct {
	flags    *optimizeFlags
	noPrompt bool
}

// Run executes the optimize command: resolves the agent, loads/builds the config, applies overrides, submits the job, and optionally polls for results.
func (a *OptimizeAction) Run(ctx context.Context, cmd *cobra.Command) error {
	endpoint, err := a.flags.resolve(ctx)
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

	out := cmd.OutOrStdout()
	bold := color.New(color.Bold)

	_, _ = bold.Fprintf(out, "Optimizing agent %q...\n", cfg.Agent.Name)
	if configSource == "" {
		fmt.Fprintf(out, "  Dataset: built-in (3 tasks, 12 criteria)\n")
	} else {
		fmt.Fprintf(out, "  Config: %s\n", configSource)
	}

	resp, client, err := a.submitJob(ctx, out, endpoint, cfg, agentProject)
	if err != nil {
		return err
	}

	if !a.flags.noWait && !optimize_api.IsTerminal(resp.Status) {
		finalStatus, err := pollOptimizeJob(cmd, client, a.flags.pollInterval, resp.OperationID)
		if err != nil {
			return err
		}
		printOptimizeResults(out, finalStatus, hasProject)
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

		// Even with explicit --config, try to reconcile agent name with the environment.
		resolved, resolveErr := resolveOptimizeAgent(ctx, a.flags.agent, a.noPrompt)
		if resolveErr == nil {
			agentProject = resolved.agentProject
			reconcileConfigAgentName(&cfg.Agent, resolved.agentName, a.flags.configFile)
		}

		return cfg, a.flags.configFile, agentProject, nil
	}

	resolved, err := resolveOptimizeAgent(ctx, a.flags.agent, a.noPrompt)
	if err != nil {
		return nil, "", "", err
	}
	agentProject = resolved.agentProject

	// Check if eval.yaml exists in the agent project and offer to use it.
	if resolved.agentProject != "" {
		evalPath := filepath.Join(resolved.agentProject, defaultEvalConfigName)
		if _, statErr := os.Stat(evalPath); statErr == nil && !a.noPrompt {
			azdClient, clientErr := azdext.NewAzdClient()
			if clientErr == nil {
				defer azdClient.Close()
				resp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
					Options: &azdext.ConfirmOptions{
						Message:      fmt.Sprintf("Found %s in project. Use it for optimization?", defaultEvalConfigName),
						DefaultValue: new(true),
					},
				})
				if promptErr == nil && resp.Value != nil && *resp.Value {
					cfg, err = LoadOptimizeConfig(evalPath)
					if err != nil {
						return nil, "", "", fmt.Errorf("failed to load %s: %w", evalPath, err)
					}
					configSource = evalPath
				}
			}
		}
	}

	if cfg == nil {
		cfg = defaultOptimizeConfig(resolved.agentName)
	} else {
		reconcileConfigAgentName(&cfg.Agent, resolved.agentName, configSource)
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
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	hasProject := agentProject != ""

	// CLI flags override config values.
	if a.flags.evalModel != "" {
		cfg.Options.EvalModel = a.flags.evalModel
	}
	if len(a.flags.targetAttributes) > 0 {
		cfg.Options.TargetAttributes = a.flags.targetAttributes
	}

	// Resolve agent config: try existing config pointer, then default baseline.
	if hasProject {
		mergeAgentBaseline(cfg, agentProject)
	}

	// When baseline config is detected, show resolved values and let the user confirm.
	if cfg.Agent.ConfigFile != "" && hasProject && !a.noPrompt {
		if err := promptOptimizeConfigConfirmation(ctx, cfg, agentProject); err != nil {
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
	if err := resolveOptimizeSystemPrompt(ctx, cfg, agentProject, hasProject, a.noPrompt); err != nil {
		return err
	}

	// Resolve skill_dir: auto-detect, check baseline, or prompt user.
	if cfg.SkillDir == "" && hasProject {
		if err := resolveOptimizeSkillDir(ctx, cfg, agentProject, a.noPrompt); err != nil {
			return err
		}
	}

	// Resolve target_config.model: prompt user if not set.
	if (cfg.Options.TargetConfig == nil || len(cfg.Options.TargetConfig.Model) == 0) && !a.noPrompt {
		if err := resolveOptimizeTargetModels(ctx, cfg); err != nil {
			return err
		}
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

	optimizeReq, err := cfg.ToRequest(endpoint)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build optimization request: %w", err)
	}

	if body, jsonErr := json.MarshalIndent(optimizeReq, "", "  "); jsonErr == nil {
		log.Printf("[debug] optimization request:\n%s", body)
	}

	// Save baseline config before starting optimization.
	hasProject := agentProject != ""
	if hasProject {
		if err := writeBaselineConfig(agentProject, baselineParams{
			Model:       optimizeReq.Agent.Model,
			Instruction: optimizeReq.Agent.SystemPrompt,
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

	printOptimizePortalLink(ctx, out, cfg.Agent.Name, resp.OperationID)
	fmt.Fprintln(out)

	saveLastOptimizeJobID(ctx, resp.OperationID)

	return resp, client, nil
}

// pollOptimizeJob polls the optimization job until it reaches a terminal state.
func pollOptimizeJob(
	cmd *cobra.Command,
	client *optimize_api.OptimizeClient,
	pollInterval int,
	operationID string,
) (*optimize_api.OptimizeJobStatus, error) {
	out := cmd.OutOrStdout()
	spinFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	frameIdx := 0
	startTime := time.Now()

	poller := &optimize_api.Poller{
		Client:      client,
		OperationID: operationID,
		Interval:    time.Duration(pollInterval) * time.Second,
		OnProgress: func(status *optimize_api.OptimizeJobStatus) {
			elapsed := time.Since(startTime).Truncate(time.Second)
			spin := spinFrames[frameIdx%len(spinFrames)]
			frameIdx++

			progress := fmt.Sprintf("\r  %s %s", spin, status.Status)
			if status.Progress != nil {
				p := status.Progress
				if p.CurrentStrategy != "" {
					progress += fmt.Sprintf(" · strategy: %s", p.CurrentStrategy)
				}
				if p.CurrentIteration > 0 {
					progress += fmt.Sprintf(" · iteration %d", p.CurrentIteration)
				}
				if p.BestScore > 0 {
					progress += fmt.Sprintf(" · score: %.2f", p.BestScore)
				}
			}
			progress += fmt.Sprintf(" · %s", elapsed)
			fmt.Fprintf(out, "%-80s", progress)
		},
	}

	finalStatus, err := poller.PollUntilDone(cmd.Context())
	fmt.Fprintln(out)
	if err != nil {
		return nil, fmt.Errorf("failed while polling optimization job: %w", err)
	}

	return finalStatus, nil
}

// printOptimizeResults prints the optimization results table and next-step commands.
func printOptimizeResults(out io.Writer, status *optimize_api.OptimizeJobStatus, hasProject bool) {
	if status.Error != nil {
		fmt.Fprintf(out, "\n  %s %s\n", color.RedString("Error:"), status.Error.Message)
	}

	if len(status.Candidates) == 0 {
		return
	}

	bold := color.New(color.Bold)
	green := color.New(color.FgGreen)

	_, _ = bold.Fprintln(out, "\nResults:")
	fmt.Fprintf(out, "  %-20s %7s %7s %8s\n", "Candidate", "Score", "Pass", "Tokens")
	fmt.Fprintf(out, "  %-20s %7s %7s %8s\n",
		strings.Repeat("─", 20), strings.Repeat("─", 7),
		strings.Repeat("─", 7), strings.Repeat("─", 8))

	bestName := ""
	if status.Best != nil {
		bestName = status.Best.Name
	}

	for _, c := range status.Candidates {
		isBest := c.Name == bestName
		name := c.Name
		if isBest {
			name += " ★"
		}

		line := fmt.Sprintf("  %-20s %7.2f %6.0f%% %8.0f", name, c.AvgScore, c.PassRate*100, c.AvgTokens)
		if isBest {
			_, _ = green.Fprintln(out, line)
		} else {
			fmt.Fprintln(out, line)
		}
	}

	// Print candidate IDs for deploy
	hasIDs := false
	for _, c := range status.Candidates {
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
	if status.Best != nil && status.Best.CandidateID != "" {
		agentName := ""
		if status.Agent != nil {
			agentName = status.Agent.AgentName
		}
		if hasProject {
			fmt.Fprintf(out, "\n  Apply the best candidate locally, then deploy:\n")
			fmt.Fprintf(out, "    azd ai agent optimize apply --candidate %s\n", status.Best.CandidateID)
			fmt.Fprintf(out, "    azd deploy\n")
		} else {
			fmt.Fprintf(out, "\n  Deploy the best candidate:\n")
			fmt.Fprintf(out, "    azd ai agent optimize deploy --candidate %s --agent %s\n",
				status.Best.CandidateID, agentName)
		}
	}
	fmt.Fprintln(out)
}

// formatOptimizeStatus returns a colorized string for the given job status.
func formatOptimizeStatus(status string) string {
	switch status {
	case optimize_api.StatusCompleted:
		return color.GreenString(status)
	case optimize_api.StatusFailed:
		return color.RedString(status)
	case optimize_api.StatusCancelled:
		return color.YellowString(status)
	case optimize_api.StatusRunning:
		return color.CyanString(status)
	case optimize_api.StatusPending:
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
