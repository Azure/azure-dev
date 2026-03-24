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
		Long: `Demonstrates the CopilotService gRPC integration by running an interactive
chat loop. Sessions are created lazily on the first message. Usage metrics
and file changes accumulate across turns and are displayed on exit.

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
	promptSvc := client.Prompt()

	fmt.Println()
	fmt.Println(color.HiCyanString("╔══════════════════════════════════════╗"))
	fmt.Println(color.HiCyanString("║") +
		color.HiWhiteString("   Copilot Chat — Demo Extension   ") +
		color.HiCyanString("   ║"))
	fmt.Println(color.HiCyanString("╚══════════════════════════════════════╝"))
	fmt.Println()

	// Optional: Initialize to warm up client and show resolved config
	initResp, err := copilot.Initialize(ctx, &azdext.InitializeCopilotRequest{
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

	// Determine starting session ID (empty = new, or from --resume)
	var sessionID string
	if flags.resume {
		sessionID = pickExistingSession(ctx, copilot, promptSvc)
	}

	fmt.Println()
	fmt.Println(color.HiBlackString("  Type your message and press Enter. Type 'exit' or 'quit' to stop."))
	fmt.Println()

	// Chat loop — SendMessage handles session creation/resumption on first call
	sessionID, err = chatLoop(ctx, copilot, promptSvc, sessionID, flags)
	if err != nil {
		return err
	}

	// Show cumulative metrics and file changes
	if sessionID != "" {
		showCumulativeMetrics(ctx, copilot, sessionID)
		showFileChanges(ctx, copilot, sessionID)

		// Stop the session
		if _, stopErr := copilot.StopSession(ctx, &azdext.StopCopilotSessionRequest{
			SessionId: sessionID,
		}); stopErr != nil {
			fmt.Printf("  %s Failed to stop session: %v\n", color.RedString("✗"), stopErr)
		} else {
			fmt.Println()
			fmt.Printf("  %s Session stopped\n", color.GreenString("✓"))
		}
	}

	fmt.Println()
	return nil
}

// pickExistingSession lists sessions and lets the user pick one to resume.
// Returns the SDK session ID to resume, or empty for a new session.
func pickExistingSession(
	ctx context.Context,
	copilot azdext.CopilotServiceClient,
	promptSvc azdext.PromptServiceClient,
) string {
	fmt.Println(color.HiYellowString("  Looking for existing sessions..."))

	listResp, err := copilot.ListSessions(ctx, &azdext.ListCopilotSessionsRequest{})
	if err != nil {
		fmt.Printf("  %s Could not list sessions: %v\n", color.YellowString("⚠"), err)
		return ""
	}

	if len(listResp.Sessions) == 0 {
		fmt.Println(color.HiBlackString("  No existing sessions found."))
		return ""
	}

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

	selectResp, err := promptSvc.Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Select a session",
			Choices: choices,
		},
	})
	if err != nil {
		return ""
	}

	idx := int(selectResp.GetValue())
	if idx < 0 || idx >= len(choices) || choices[idx].Value == "__new__" {
		return ""
	}

	sdkSessionID := choices[idx].Value
	fmt.Printf("  %s Will resume session: %s\n",
		color.GreenString("✓"), color.CyanString(sdkSessionID))

	return sdkSessionID
}

// chatLoop runs the interactive prompt → send → display cycle.
// Returns the session ID assigned by the server (from the first SendMessage response).
func chatLoop(
	ctx context.Context,
	copilot azdext.CopilotServiceClient,
	promptSvc azdext.PromptServiceClient,
	sessionID string,
	flags copilotFlags,
) (string, error) {
	for {
		resp, err := promptSvc.Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "You",
				Placeholder:    "Type a message...",
				IgnoreHintKeys: true,
			},
		})
		if err != nil {
			return sessionID, nil // user cancelled
		}

		input := strings.TrimSpace(resp.Value)
		if input == "" || strings.EqualFold(input, "exit") || strings.EqualFold(input, "quit") {
			fmt.Println()
			fmt.Println(color.HiBlackString("  Ending chat session..."))
			break
		}

		// SendMessage creates the session on the first call, reuses it after
		sendReq := &azdext.SendCopilotMessageRequest{
			Prompt: input,
		}
		if sessionID != "" {
			sendReq.SessionId = sessionID
		} else {
			// First call — include config options
			sendReq.Model = flags.model
			sendReq.ReasoningEffort = flags.reasoningEffort
			sendReq.SystemMessage = flags.systemMessage
			sendReq.Mode = flags.mode
		}

		sendResp, err := copilot.SendMessage(ctx, sendReq)
		if err != nil {
			fmt.Printf("  %s Error: %v\n", color.RedString("✗"), err)
			fmt.Println()
			continue
		}

		// Capture the session ID from the response for reuse
		sessionID = sendResp.SessionId

		fmt.Println()
	}

	return sessionID, nil
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

// showFileChanges displays accumulated file changes from the session.
func showFileChanges(
	ctx context.Context,
	copilot azdext.CopilotServiceClient,
	sessionID string,
) {
	changesResp, err := copilot.GetFileChanges(ctx, &azdext.GetCopilotFileChangesRequest{
		SessionId: sessionID,
	})
	if err != nil || len(changesResp.FileChanges) == 0 {
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
