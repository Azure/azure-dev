// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	configFile   string
	agent        string
	evalModel    string
	strategies   []string
	noWait       bool
	watch        bool
	pollInterval int
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

  # Optimize with skill strategy
  azd ai agent optimize --strategy skill

  # Optimize with both strategies
  azd ai agent optimize --strategy instruction --strategy skill

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
	cmd.Flags().StringArrayVarP(&flags.strategies, "strategy", "s", nil, "Optimization strategy: instruction, skill (repeatable)")
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
	if len(a.flags.strategies) > 0 {
		cfg.Options.Strategies = a.flags.strategies
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
		printOptimizeResults(out, finalStatus)
	}

	return nil
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

func printOptimizeResults(out io.Writer, status *optimize_api.OptimizeJobStatus) {
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

	// Print deploy command for best candidate
	if status.Best != nil && status.Best.CandidateID != "" {
		agentName := ""
		if status.Agent != nil {
			agentName = status.Agent.AgentName
		}
		fmt.Fprintf(out, "\n  Deploy the best candidate:\n")
		fmt.Fprintf(out, "    azd ai agent optimize deploy --candidate %s --agent %s\n",
			status.Best.CandidateID, agentName)
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
