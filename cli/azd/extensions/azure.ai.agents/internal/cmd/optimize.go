// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

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

	"azureaiagent/internal/pkg/agents/opteval"
	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// optimizeAgentContext holds the resolved agent name and project directory.
type optimizeAgentContext struct {
	agentName    string
	agentProject string // project directory path (empty if not resolved from azd project)
}

// resolveOptimizeAgent resolves the agent name and project directory using:
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

type optimizeFlags struct {
	configFile       string
	agent            string
	evalModel        string
	targetAttributes []string
	noWait           bool
	watch            bool
	pollInterval     int
	optimizeConnectionFlags
}

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
	cmd.Flags().StringVarP(&flags.evalModel, "eval-model", "m", "gpt-4.1-mini", "Model for evaluation")
	cmd.Flags().StringArrayVarP(&flags.targetAttributes, "target", "s", nil, "Target attribute for optimization: instruction, skill (repeatable)")
	cmd.Flags().BoolVar(&flags.noWait, "no-wait", false, "Submit job and return immediately without waiting for completion")
	cmd.Flags().BoolVar(&flags.watch, "watch", true, "Watch for job completion (opposite of --no-wait)")
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

func (a *OptimizeAction) Run(ctx context.Context, cmd *cobra.Command) error {
	endpoint, err := a.flags.resolve(ctx)
	if err != nil {
		return err
	}

	var cfg *OptimizeConfig
	configSource := "" // tracks where the config came from for user messaging
	hasProject := false
	agentProject := ""

	if a.flags.configFile != "" {
		cfg, err = LoadOptimizeConfig(a.flags.configFile)
		if err != nil {
			return fmt.Errorf("%w\n\nCheck that the file path is correct and contains valid YAML", err)
		}
		configSource = a.flags.configFile
	} else {
		resolved, err := resolveOptimizeAgent(ctx, a.flags.agent, a.noPrompt)
		if err != nil {
			return err
		}
		hasProject = resolved.agentProject != ""
		agentProject = resolved.agentProject

		// Check if eval.yaml exists in the agent project and offer to use it
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
							return fmt.Errorf("failed to load %s: %w", evalPath, err)
						}
						configSource = evalPath
					}
				}
			}
		}

		if cfg == nil {
			cfg = defaultOptimizeConfig(resolved.agentName)
		}
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// CLI flags override config values
	if a.flags.evalModel != "" {
		cfg.Options.EvalModel = a.flags.evalModel
	}
	if len(a.flags.targetAttributes) > 0 {
		cfg.Options.TargetAttributes = a.flags.targetAttributes
	}

	// Resolve relative skill_dir against agent project directory.
	if cfg.Agent.SkillDir != "" && hasProject && !filepath.IsAbs(cfg.Agent.SkillDir) {
		cfg.Agent.SkillDir = filepath.Join(agentProject, cfg.Agent.SkillDir)
	}

	// Resolve agent instruction using a well-defined lifecycle:
	//  1. Config file (eval.yaml / --config) — instruction in the agent section (inline or file reference)
	//  2. Baseline config — .agent_optimization/baseline/config.json from a prior optimize run
	//  3. Interactive prompt — ask the user to provide inline text or a file path
	if err := resolveOptimizeSystemPrompt(ctx, cfg, agentProject, hasProject, a.noPrompt); err != nil {
		return err
	}

	// Resolve skill_dir: auto-detect, check baseline, or prompt user.
	if cfg.Agent.SkillDir == "" && hasProject {
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

	out := cmd.OutOrStdout()
	bold := color.New(color.Bold)

	bold.Fprintf(out, "Optimizing agent %q...\n", cfg.Agent.Name)
	if configSource == "" {
		fmt.Fprintf(out, "  Dataset: built-in (3 tasks, 12 criteria)\n")
	} else {
		fmt.Fprintf(out, "  Config: %s\n", configSource)
	}

	credential, err := newAgentCredential()
	if err != nil {
		return err
	}

	client := optimize_api.NewOptimizeClient(endpoint, credential)

	optimizeReq, err := cfg.ToRequest(endpoint)
	if err != nil {
		return fmt.Errorf("failed to build optimization request: %w", err)
	}

	if body, jsonErr := json.MarshalIndent(optimizeReq, "", "  "); jsonErr == nil {
		log.Printf("[debug] optimization request:\n%s", body)
	}

	// Save baseline config before starting optimization.
	if hasProject {
		if err := saveBaselineConfig(agentProject, cfg.Agent.SkillDir, optimizeReq); err != nil {
			fmt.Fprintf(out, "  warning: failed to save baseline config: %s\n", err)
		} else {
			fmt.Fprintf(out, "  Baseline saved to %s\n",
				filepath.Join(optimizationDir, "baseline", "config.json"))
		}
	}

	resp, err := client.StartOptimize(ctx, optimizeReq)
	if err != nil {
		return fmt.Errorf("failed to submit optimization job: %w\n\nCheck that the endpoint %q is reachable", err, endpoint)
	}

	fmt.Fprintf(out, "  Job ID: %s\n", color.CyanString(resp.OperationID))
	fmt.Fprintf(out, "  Status: %s\n\n", resp.Status)

	// Store last operation ID in azd environment for use by status/deploy
	saveLastOptimizeJobID(ctx, resp.OperationID)

	if !a.flags.noWait && !optimize_api.IsTerminal(resp.Status) {
		finalStatus, err := pollOptimizeJob(cmd, client, a.flags.pollInterval, resp.OperationID)
		if err != nil {
			return err
		}
		printOptimizeResults(out, finalStatus, hasProject)
	}

	return nil
}

// resolveOptimizeSystemPrompt resolves the agent's system prompt using a well-defined lifecycle:
//
//  1. Config (eval.yaml / --config): instruction in the agent section (inline or file).
//  2. Baseline: .agent_optimization/baseline/config.json from a prior optimization run.
//  3. Interactive prompt: ask the user to provide inline text or a file path.
//
// Relative file paths are resolved against agentProject.
func resolveOptimizeSystemPrompt(
	ctx context.Context,
	cfg *OptimizeConfig,
	agentProject string,
	hasProject bool,
	noPrompt bool,
) error {
	// Resolve relative instruction file paths against the agent project directory.
	if cfg.Agent.Instruction.File != "" && hasProject && !filepath.IsAbs(cfg.Agent.Instruction.File) {
		cfg.Agent.Instruction.File = filepath.Join(agentProject, cfg.Agent.Instruction.File)
	}

	// Step 1: Config explicitly declares a file reference — validate it's readable.
	if cfg.Agent.Instruction.File != "" {
		if _, err := os.Stat(cfg.Agent.Instruction.File); err != nil {
			return fmt.Errorf("instruction file %q from config is not accessible: %w",
				cfg.Agent.Instruction.File, err)
		}
		return nil
	}

	// Step 1b: Config already has inline instruction — nothing to do.
	if cfg.Agent.Instruction.Value != "" {
		return nil
	}

	// Step 2: Check baseline config from a prior optimization run.
	if hasProject {
		if baseline, loadErr := loadBaselineConfig(agentProject); loadErr == nil && baseline.Instructions != "" {
			if noPrompt {
				cfg.Agent.Instruction.Value = baseline.Instructions
				return nil
			}

			azdClient, clientErr := azdext.NewAzdClient()
			if clientErr == nil {
				defer azdClient.Close()
				resp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
					Options: &azdext.ConfirmOptions{
						Message: "No instruction in config. " +
							"Found one in baseline (.agent_optimization/baseline/config.json). Use it?",
						DefaultValue: new(true),
					},
				})
				if promptErr == nil && resp.Value != nil && *resp.Value {
					cfg.Agent.Instruction.Value = baseline.Instructions
					return nil
				}
			}
		}
	}

	// Step 3: Interactive prompt — ask user to provide inline text or a file path.
	if noPrompt {
		return fmt.Errorf("instruction is required for optimization.\n\n" +
			"Provide it via one of:\n" +
			"  1. instruction in eval.yaml (agent section): inline string or file reference\n" +
			"  2. Run a prior optimization to create a baseline (.agent_optimization/baseline/config.json)\n" +
			"  3. Run without --no-prompt to enter it interactively")
	}

	azdClient, clientErr := azdext.NewAzdClient()
	if clientErr != nil {
		return fmt.Errorf("instruction is required but could not open interactive prompt: %w", clientErr)
	}
	defer azdClient.Close()

	inputChoices := []*azdext.SelectChoice{
		{Label: "Type inline", Value: "inline"},
		{Label: "Load from file", Value: "file"},
	}
	defaultIdx := int32(0)
	selResp, selErr := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "No instruction found in config or baseline. " +
				"How would you like to provide it?",
			Choices:       inputChoices,
			SelectedIndex: &defaultIdx,
		},
	})
	if selErr != nil {
		return fmt.Errorf("prompting for instruction input method: %w", selErr)
	}

	if inputChoices[int(*selResp.Value)].Value == "file" {
		pathResp, pathErr := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Path to instruction file",
				IgnoreHintKeys: true,
			},
		})
		if pathErr != nil {
			return fmt.Errorf("prompting for instruction file path: %w", pathErr)
		}
		filePath := strings.TrimSpace(pathResp.Value)
		// Resolve relative paths against the agent project directory.
		if !filepath.IsAbs(filePath) && hasProject {
			filePath = filepath.Join(agentProject, filePath)
		}
		if _, err := os.Stat(filePath); err != nil {
			return fmt.Errorf("instruction file %q is not accessible: %w", filePath, err)
		}
		cfg.Agent.Instruction.File = filePath
	} else {
		resp, promptErr := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Enter the agent's instruction",
				IgnoreHintKeys: true,
			},
		})
		if promptErr != nil {
			return fmt.Errorf("prompting for instruction: %w", promptErr)
		}
		cfg.Agent.Instruction.Value = strings.TrimSpace(resp.Value)
	}

	return nil
}

// resolveOptimizeSkillDir resolves the agent's skill directory:
//  1. Auto-detect: look for a "skills/" folder in the agent project — confirm with user.
//  2. Baseline: check .agent_optimization/baseline/config.json for a saved skill_dir.
//  3. Interactive prompt: ask the user to provide a path or skip.
func resolveOptimizeSkillDir(
	ctx context.Context,
	cfg *OptimizeConfig,
	agentProject string,
	noPrompt bool,
) error {
	// Step 1: Auto-detect common skill directory names.
	var detectedDir string
	for _, candidate := range []string{"skills", "skill"} {
		dir := filepath.Join(agentProject, candidate)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			detectedDir = dir
			break
		}
	}

	// Step 2: Check baseline config.
	if detectedDir == "" {
		if baseline, loadErr := loadBaselineConfig(agentProject); loadErr == nil && baseline.SkillDir != "" {
			if _, err := os.Stat(baseline.SkillDir); err == nil {
				detectedDir = baseline.SkillDir
			}
		}
	}

	if noPrompt {
		// In no-prompt mode, use whatever was detected (may be empty).
		cfg.Agent.SkillDir = detectedDir
		return nil
	}

	azdClient, clientErr := azdext.NewAzdClient()
	if clientErr != nil {
		cfg.Agent.SkillDir = detectedDir
		return nil
	}
	defer azdClient.Close()

	if detectedDir != "" {
		// Found a skill directory — ask user to confirm or provide a different one.
		choices := []*azdext.SelectChoice{
			{Label: fmt.Sprintf("Use detected: %s", detectedDir), Value: "use"},
			{Label: "Provide a different path", Value: "other"},
			{Label: "Skip (no skills)", Value: "skip"},
		}
		defaultIdx := int32(0)
		selResp, selErr := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       fmt.Sprintf("Found skills directory: %s", detectedDir),
				Choices:       choices,
				SelectedIndex: &defaultIdx,
			},
		})
		if selErr != nil {
			cfg.Agent.SkillDir = detectedDir
			return nil
		}

		switch choices[int(*selResp.Value)].Value {
		case "use":
			cfg.Agent.SkillDir = detectedDir
			return nil
		case "skip":
			return nil
		case "other":
			// Fall through to path prompt below.
		}
	} else {
		// No skill directory found — ask if they want to provide one.
		resp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      "No skills directory found. Would you like to provide one?",
				DefaultValue: new(bool), // default false
			},
		})
		if promptErr != nil || !resp.GetValue() {
			return nil // skip skills
		}
	}

	// Prompt for a custom path.
	pathResp, pathErr := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        "Path to skills directory",
			IgnoreHintKeys: true,
		},
	})
	if pathErr != nil {
		return fmt.Errorf("prompting for skills directory: %w", pathErr)
	}

	dir := strings.TrimSpace(pathResp.Value)
	if dir == "" {
		return nil
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(agentProject, dir)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("skills directory %q is not accessible or not a directory", dir)
	}

	cfg.Agent.SkillDir = dir
	return nil
}

// knownOptimizationModels is the list of models commonly used for optimization.
var knownOptimizationModels = []string{
	"gpt-4.1",
	"gpt-4.1-mini",
	"gpt-4.1-nano",
	"gpt-4o",
	"gpt-4o-mini",
}

// resolveOptimizeTargetModels prompts the user to select model candidates
// for optimization (target_config.model). Shows the current deployed model
// and allows multi-select from known models.
func resolveOptimizeTargetModels(
	ctx context.Context,
	cfg *OptimizeConfig,
) error {
	azdClient, clientErr := azdext.NewAzdClient()
	if clientErr != nil {
		return nil
	}
	defer azdClient.Close()

	currentModel := cfg.Agent.Model

	message := "Select target models for optimization"
	if currentModel != "" {
		message = fmt.Sprintf("Select target models for optimization (current: %s)", currentModel)
	}

	resp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      "Would you like to specify target models for optimization?",
			DefaultValue: new(bool), // default false
		},
	})
	if promptErr != nil || !resp.GetValue() {
		return nil
	}

	// Build choices — include current model if not already in the known list.
	choices := buildOptimizeModelChoices(currentModel)

	multiResp, multiErr := azdClient.Prompt().MultiSelect(ctx, &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message: message,
			Choices: choices,
		},
	})
	if multiErr != nil {
		return fmt.Errorf("prompting for target models: %w", multiErr)
	}

	var models []string
	for _, v := range multiResp.Values {
		models = append(models, v.Value)
	}

	if len(models) > 0 {
		if cfg.Options.TargetConfig == nil {
			cfg.Options.TargetConfig = &opteval.TargetConfig{}
		}
		cfg.Options.TargetConfig.Model = models
	}

	return nil
}

// buildOptimizeModelChoices returns MultiSelectChoice items for model selection.
// The current deployed model is included and pre-selected; placed first if not in the known list.
func buildOptimizeModelChoices(currentModel string) []*azdext.MultiSelectChoice {
	seen := make(map[string]bool)
	var choices []*azdext.MultiSelectChoice

	// If the current model is not in the known list, prepend it.
	if currentModel != "" {
		found := false
		for _, m := range knownOptimizationModels {
			if m == currentModel {
				found = true
				break
			}
		}
		if !found {
			choices = append(choices, &azdext.MultiSelectChoice{
				Label:    currentModel + " (current)",
				Value:    currentModel,
				Selected: true,
			})
			seen[currentModel] = true
		}
	}

	for _, m := range knownOptimizationModels {
		if seen[m] {
			continue
		}
		label := m
		selected := false
		if m == currentModel {
			label = m + " (current)"
			selected = true
		}
		choices = append(choices, &azdext.MultiSelectChoice{
			Label:    label,
			Value:    m,
			Selected: selected,
		})
	}

	return choices
}

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

func printOptimizeResults(out io.Writer, status *optimize_api.OptimizeJobStatus, hasProject bool) {
	if status.Error != nil {
		fmt.Fprintf(out, "\n  %s %s\n", color.RedString("Error:"), status.Error.Message)
	}

	if len(status.Candidates) == 0 {
		return
	}

	bold := color.New(color.Bold)
	green := color.New(color.FgGreen)

	bold.Fprintln(out, "\nResults:")
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
			green.Fprintln(out, line)
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

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
