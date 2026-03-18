// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func newCopilotCommand() *cobra.Command {
	var model string
	var reasoningEffort string
	var systemMessage string
	var mode string
	var resume bool

	cmd := &cobra.Command{
		Use:   "copilot",
		Short: "Interactive Copilot chat loop demonstrating the CopilotService gRPC API.",
		Long: `Demonstrates the full CopilotService gRPC integration by running an interactive
chat loop. Creates a session, sends messages, displays usage metrics per turn,
and shows cumulative stats and file changes on exit.

Use --resume to list and resume a previous session.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
					return nil
				}
				return fmt.Errorf("failed waiting for debugger: %w", err)
			}

			return runCopilotChat(ctx, azdClient, copilotFlags{
				model:           model,
				reasoningEffort: reasoningEffort,
				systemMessage:   systemMessage,
				mode:            mode,
				resume:          resume,
			})
		},
	}

	cmd.Flags().StringVar(&model, "model", "", "Model to use (empty = default)")
	cmd.Flags().StringVar(
		&reasoningEffort, "reasoning-effort", "", "Reasoning effort level (low, medium, high)",
	)
	cmd.Flags().StringVar(&systemMessage, "system-message", "", "Custom system message")
	cmd.Flags().StringVar(&mode, "mode", "autopilot", "Agent mode (autopilot, interactive, plan)")
	cmd.Flags().BoolVar(&resume, "resume", false, "Resume an existing session")

	return cmd
}

type copilotFlags struct {
	model           string
	reasoningEffort string
	systemMessage   string
	mode            string
	resume          bool
}

func runCopilotChat(ctx context.Context, client *azdext.AzdClient, flags copilotFlags) error {
	copilot := client.Copilot()
	prompt := client.Prompt()

	fmt.Println()
	fmt.Println(color.HiCyanString("╔══════════════════════════════════════╗"))
	fmt.Println(color.HiCyanString("║") + color.HiWhiteString("   Copilot Chat — Demo Extension   ") +
		color.HiCyanString("  ║"))
	fmt.Println(color.HiCyanString("╚══════════════════════════════════════╝"))
	fmt.Println()

	var sessionID string

	if flags.resume {
		// List and resume an existing session
		resumed, err := resumeExistingSession(ctx, copilot, prompt)
		if err != nil {
			return err
		}
		if resumed != "" {
			sessionID = resumed
		}
	}

	if sessionID == "" {
		// Create a new session
		created, err := createNewSession(ctx, copilot, flags)
		if err != nil {
			return err
		}
		sessionID = created
	}

	// Run the chat loop
	if err := chatLoop(ctx, copilot, prompt, sessionID); err != nil {
		return err
	}

	// Show cumulative usage metrics
	showCumulativeMetrics(ctx, copilot, sessionID)

	// Show file changes
	showFileChanges(ctx, copilot, sessionID)

	// Stop the session
	_, err := copilot.StopSession(ctx, &azdext.StopCopilotSessionRequest{
		SessionId: sessionID,
	})
	if err != nil {
		fmt.Printf("  %s Failed to stop session: %v\n", color.RedString("✗"), err)
	} else {
		fmt.Printf("  %s Session stopped\n", color.GreenString("✓"))
	}

	fmt.Println()
	return nil
}

// resumeExistingSession lists available sessions and prompts the user to select one.
// Returns the session handle if resumed, or empty string if user chose to start new.
func resumeExistingSession(
	ctx context.Context,
	copilot azdext.CopilotServiceClient,
	prompt azdext.PromptServiceClient,
) (string, error) {
	fmt.Println(color.HiYellowString("  Looking for existing sessions..."))

	listResp, err := copilot.ListSessions(ctx, &azdext.ListCopilotSessionsRequest{})
	if err != nil {
		fmt.Printf("  %s Could not list sessions: %v\n", color.YellowString("⚠"), err)
		return "", nil
	}

	if len(listResp.Sessions) == 0 {
		fmt.Println(color.HiBlackString("  No existing sessions found. Starting new session."))
		fmt.Println()
		return "", nil
	}

	// Build choices for the session picker
	choices := []*azdext.SelectChoice{
		{Value: "__new__", Label: "Start a new session"},
	}
	for _, s := range listResp.Sessions {
		label := fmt.Sprintf("Resume: %s", s.SessionId)
		if s.Summary != "" {
			summary := s.Summary
			if len(summary) > 60 {
				summary = summary[:57] + "..."
			}
			label += fmt.Sprintf(" — %s", summary)
		}
		if s.ModifiedTime != "" {
			label += fmt.Sprintf(" (%s)", s.ModifiedTime)
		}
		choices = append(choices, &azdext.SelectChoice{
			Value: s.SessionId,
			Label: label,
		})
	}

	selectResp, err := prompt.Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Select a session",
			Choices: choices,
		},
	})
	if err != nil {
		return "", nil
	}

	idx := int(selectResp.GetValue())
	if idx < 0 || idx >= len(choices) || choices[idx].Value == "__new__" {
		fmt.Println()
		return "", nil
	}

	selectedSDKSessionID := choices[idx].Value

	resumeResp, err := copilot.ResumeSession(ctx, &azdext.ResumeCopilotSessionRequest{
		SessionId: selectedSDKSessionID,
		Headless:  true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to resume session: %w", err)
	}

	fmt.Printf("  %s Resumed session: %s\n",
		color.GreenString("✓"), color.CyanString(resumeResp.SessionId))
	fmt.Println()

	return resumeResp.SessionId, nil
}

// createNewSession creates a fresh Copilot session with the given flags.
func createNewSession(
	ctx context.Context,
	copilot azdext.CopilotServiceClient,
	flags copilotFlags,
) (string, error) {
	fmt.Println(color.HiYellowString("  Creating Copilot session..."))

	createResp, err := copilot.CreateSession(ctx, &azdext.CreateCopilotSessionRequest{
		Model:           flags.model,
		ReasoningEffort: flags.reasoningEffort,
		SystemMessage:   flags.systemMessage,
		Mode:            flags.mode,
		Headless:        true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	sessionID := createResp.SessionId
	fmt.Printf("  %s Session created: %s\n",
		color.GreenString("✓"), color.CyanString(sessionID))

	// Initialize the session
	initResp, err := copilot.Initialize(ctx, &azdext.InitializeCopilotRequest{
		SessionId:       sessionID,
		Model:           flags.model,
		ReasoningEffort: flags.reasoningEffort,
	})
	if err != nil {
		fmt.Printf("  %s Initialize warning: %v\n", color.YellowString("⚠"), err)
	} else {
		if initResp.Model != "" {
			fmt.Printf("  %s Model: %s\n", color.HiBlackString("•"), initResp.Model)
		}
		if initResp.ReasoningEffort != "" {
			fmt.Printf("  %s Reasoning: %s\n",
				color.HiBlackString("•"), initResp.ReasoningEffort)
		}
		if initResp.IsFirstRun {
			fmt.Printf("  %s First-time configuration applied\n", color.HiBlackString("•"))
		}
	}

	fmt.Println()
	fmt.Println(color.HiBlackString("  Type your message and press Enter. Type 'exit' or 'quit' to stop."))
	fmt.Println()

	return sessionID, nil
}

// chatLoop runs the interactive prompt → send → display cycle.
func chatLoop(
	ctx context.Context,
	copilot azdext.CopilotServiceClient,
	promptSvc azdext.PromptServiceClient,
	sessionID string,
) error {
	turn := 0
	for {
		turn++

		// Prompt user for input
		resp, err := promptSvc.Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:     fmt.Sprintf("[%d] You", turn),
				Placeholder: "Type a message...",
			},
		})
		if err != nil {
			return nil // user cancelled
		}

		input := strings.TrimSpace(resp.Value)
		if input == "" || strings.EqualFold(input, "exit") || strings.EqualFold(input, "quit") {
			fmt.Println()
			fmt.Println(color.HiBlackString("  Ending chat session..."))
			break
		}

		// Send message to agent
		fmt.Printf("  %s Sending to agent...\n", color.HiBlackString("⏳"))

		sendResp, err := copilot.SendMessage(ctx, &azdext.SendCopilotMessageRequest{
			SessionId: sessionID,
			Prompt:    input,
		})
		if err != nil {
			fmt.Printf("  %s Error: %v\n", color.RedString("✗"), err)
			fmt.Println()
			continue
		}

		// Display turn usage metrics
		if sendResp.Usage != nil {
			displayTurnUsage(turn, sendResp.Usage)
		}

		fmt.Println()
	}

	return nil
}

// displayTurnUsage shows usage metrics for a single turn.
func displayTurnUsage(turn int, usage *azdext.CopilotUsageMetrics) {
	fmt.Printf("  %s Turn %d complete", color.GreenString("✓"), turn)
	if usage.Model != "" {
		fmt.Printf(" (%s)", color.CyanString(usage.Model))
	}
	fmt.Println()

	fmt.Printf("    %s Tokens: %s in / %s out / %s total\n",
		color.HiBlackString("•"),
		formatTokens(usage.InputTokens),
		formatTokens(usage.OutputTokens),
		formatTokens(usage.TotalTokens))

	if usage.DurationMs > 0 {
		fmt.Printf("    %s Duration: %s\n",
			color.HiBlackString("•"), formatDuration(usage.DurationMs))
	}
	if usage.PremiumRequests > 0 {
		fmt.Printf("    %s Premium requests: %.0f\n",
			color.HiBlackString("•"), usage.PremiumRequests)
	}
}

// showCumulativeMetrics displays cumulative session usage.
func showCumulativeMetrics(
	ctx context.Context,
	copilot azdext.CopilotServiceClient,
	sessionID string,
) {
	metricsResp, err := copilot.GetUsageMetrics(ctx, &azdext.GetCopilotUsageMetricsRequest{
		SessionId: sessionID,
	})
	if err != nil {
		fmt.Printf("  %s Could not retrieve metrics: %v\n", color.YellowString("⚠"), err)
		return
	}

	usage := metricsResp.Usage
	if usage == nil || (usage.InputTokens == 0 && usage.OutputTokens == 0) {
		return
	}

	fmt.Println()
	fmt.Println(color.HiWhiteString("  ── Session Usage ──"))
	if usage.Model != "" {
		fmt.Printf("  %s Model:            %s\n", color.HiBlackString("•"), usage.Model)
	}
	fmt.Printf("  %s Input tokens:     %s\n",
		color.HiBlackString("•"), formatTokens(usage.InputTokens))
	fmt.Printf("  %s Output tokens:    %s\n",
		color.HiBlackString("•"), formatTokens(usage.OutputTokens))
	fmt.Printf("  %s Total tokens:     %s\n",
		color.HiBlackString("•"), formatTokens(usage.TotalTokens))
	if usage.BillingRate > 0 {
		fmt.Printf("  %s Billing rate:     %.0fx per request\n",
			color.HiBlackString("•"), usage.BillingRate)
	}
	fmt.Printf("  %s Premium requests: %.0f\n",
		color.HiBlackString("•"), usage.PremiumRequests)
	if usage.DurationMs > 0 {
		fmt.Printf("  %s API duration:     %s\n",
			color.HiBlackString("•"), formatDuration(usage.DurationMs))
	}
}

// showFileChanges displays files changed during the session.
func showFileChanges(
	ctx context.Context,
	copilot azdext.CopilotServiceClient,
	sessionID string,
) {
	changesResp, err := copilot.GetFileChanges(ctx, &azdext.GetCopilotFileChangesRequest{
		SessionId: sessionID,
	})
	if err != nil {
		return
	}

	if len(changesResp.FileChanges) == 0 {
		return
	}

	fmt.Println()
	fmt.Println(color.HiWhiteString("  ── File Changes ──"))

	for _, change := range changesResp.FileChanges {
		switch change.ChangeType {
		case azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_CREATED:
			fmt.Printf("  %s %s\n", color.GreenString("+ Created "), change.Path)
		case azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_MODIFIED:
			fmt.Printf("  %s %s\n", color.YellowString("± Modified"), change.Path)
		case azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_DELETED:
			fmt.Printf("  %s %s\n", color.RedString("- Deleted "), change.Path)
		default:
			fmt.Printf("  %s %s\n", color.HiBlackString("? Unknown "), change.Path)
		}
	}
}

func formatTokens(tokens float64) string {
	if tokens >= 1_000_000 {
		return fmt.Sprintf("%.1fM", tokens/1_000_000)
	}
	if tokens >= 1_000 {
		return fmt.Sprintf("%.1fK", tokens/1_000)
	}
	return fmt.Sprintf("%.0f", tokens)
}

func formatDuration(ms float64) string {
	seconds := ms / 1000
	if seconds >= 60 {
		return fmt.Sprintf("%.0fm %.0fs", seconds/60, float64(int(seconds)%60))
	}
	return fmt.Sprintf("%.1fs", seconds)
}
