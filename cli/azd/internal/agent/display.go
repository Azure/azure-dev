// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/azure/azure-dev/cli/azd/internal/agent/logging"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
)

// AgentDisplay handles UX rendering for Copilot SDK session events.
// It subscribes directly to session.On() and manages a Canvas with Spinner
// and VisualElement layers to render tool execution, thinking, streaming
// response tokens, errors, and other agent activity.
type AgentDisplay struct {
	console input.Console
	canvas  uxlib.Canvas
	spinner *uxlib.Spinner

	// State — protected by mu
	mu               sync.Mutex
	latestThought    string
	currentTool      string
	currentToolInput string
	toolStartTime    time.Time
	finalContent     string

	// Lifecycle
	idleCh chan struct{}
	ctx    context.Context
}

// NewAgentDisplay creates a new AgentDisplay.
func NewAgentDisplay(console input.Console) *AgentDisplay {
	return &AgentDisplay{
		console: console,
		idleCh:  make(chan struct{}, 1),
	}
}

// Start initializes the canvas and spinner for rendering.
// Returns a cleanup function that must be called when done.
func (d *AgentDisplay) Start(ctx context.Context) (func(), error) {
	d.ctx = ctx

	d.spinner = uxlib.NewSpinner(&uxlib.SpinnerOptions{
		Text: "Processing...",
	})

	d.canvas = uxlib.NewCanvas(
		d.spinner,
		uxlib.NewVisualElement(func(printer uxlib.Printer) error {
			d.mu.Lock()
			thought := d.latestThought
			d.mu.Unlock()

			printer.Fprintln()
			if thought != "" {
				printer.Fprintln(color.HiBlackString(thought))
				printer.Fprintln()
			}
			return nil
		}),
	)

	// Ticker goroutine for spinner elapsed time updates
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.mu.Lock()
				tool := d.currentTool
				toolInput := d.currentToolInput
				startTime := d.toolStartTime
				d.mu.Unlock()

				if tool != "" {
					elapsed := int(time.Since(startTime).Seconds())
					text := fmt.Sprintf("Running %s", color.MagentaString(tool))
					if toolInput != "" {
						text += " with " + color.HiBlackString(toolInput)
					}
					text += "..."
					text += color.HiBlackString(fmt.Sprintf("\n(%ds, CTRL+C to cancel)", elapsed))
					d.spinner.UpdateText(text)
				}

				d.canvas.Update()
			}
		}
	}()

	cleanup := func() {
		d.canvas.Clear()
		d.canvas.Close()
	}

	return cleanup, d.canvas.Run()
}

// HandleEvent processes a Copilot SDK SessionEvent and updates the UX.
// This is called synchronously by the SDK for each event.
func (d *AgentDisplay) HandleEvent(event copilot.SessionEvent) {
	switch event.Type {
	case copilot.AssistantTurnStart:
		d.spinner.UpdateText("Processing...")
		d.mu.Lock()
		d.latestThought = ""
		d.currentTool = ""
		d.currentToolInput = ""
		d.mu.Unlock()

	case copilot.AssistantIntent:
		if event.Data.Intent != nil && *event.Data.Intent != "" {
			d.spinner.UpdateText(fmt.Sprintf("◆ %s", *event.Data.Intent))
		}

	case copilot.AssistantReasoning:
		if event.Data.ReasoningText != nil && *event.Data.ReasoningText != "" {
			d.mu.Lock()
			d.latestThought = logging.TruncateString(*event.Data.ReasoningText, 200)
			d.mu.Unlock()
		}

	case copilot.AssistantReasoningDelta:
		if event.Data.DeltaContent != nil && *event.Data.DeltaContent != "" {
			d.mu.Lock()
			d.latestThought = logging.TruncateString(*event.Data.DeltaContent, 200)
			d.mu.Unlock()
		}

	case copilot.AssistantMessage:
		if event.Data.Content != nil {
			d.mu.Lock()
			d.finalContent = *event.Data.Content
			d.mu.Unlock()
		}

	case copilot.AssistantMessageDelta:
		if event.Data.DeltaContent != nil && *event.Data.DeltaContent != "" {
			d.mu.Lock()
			d.latestThought = logging.TruncateString(*event.Data.DeltaContent, 200)
			d.mu.Unlock()
		}

	case copilot.ToolExecutionStart:
		toolName := derefStr(event.Data.ToolName)
		if toolName == "" {
			toolName = derefStr(event.Data.MCPToolName)
		}
		if toolName == "" {
			return
		}

		// Print completion for previous tool
		d.printToolCompletion()

		toolInput := extractToolInputSummary(event.Data.Arguments)

		d.mu.Lock()
		d.currentTool = toolName
		d.currentToolInput = toolInput
		d.toolStartTime = time.Now()
		d.latestThought = ""
		d.mu.Unlock()

		text := fmt.Sprintf("Running %s", color.MagentaString(toolName))
		if toolInput != "" {
			text += " with " + color.HiBlackString(toolInput)
		}
		text += "..."
		d.spinner.UpdateText(text)

	case copilot.ToolExecutionProgress:
		if event.Data.ProgressMessage != nil && *event.Data.ProgressMessage != "" {
			d.mu.Lock()
			tool := d.currentTool
			d.mu.Unlock()

			text := fmt.Sprintf("Running %s", color.MagentaString(tool))
			text += " — " + color.HiBlackString(*event.Data.ProgressMessage)
			d.spinner.UpdateText(text)
		}

	case copilot.ToolExecutionComplete:
		d.printToolCompletion()
		d.mu.Lock()
		d.currentTool = ""
		d.currentToolInput = ""
		d.mu.Unlock()
		d.spinner.UpdateText("Processing...")

	case copilot.SessionError:
		msg := "unknown error"
		if event.Data.Message != nil {
			msg = *event.Data.Message
		}
		log.Printf("[copilot] Session error: %s", msg)
		d.canvas.Clear()
		fmt.Println(output.WithErrorFormat("Agent error: %s", msg))

	case copilot.SessionWarning:
		if event.Data.Message != nil {
			d.canvas.Clear()
			fmt.Println(output.WithWarningFormat("Warning: %s", *event.Data.Message))
		}

	case copilot.SkillInvoked:
		name := derefStr(event.Data.Name)
		if name != "" {
			d.canvas.Clear()
			fmt.Println(color.CyanString("◇ Using skill: %s", name))
		}

	case copilot.SubagentStarted:
		name := derefStr(event.Data.AgentName)
		if name != "" {
			d.canvas.Clear()
			fmt.Println(color.MagentaString("◆ Delegating to: %s", name))
		}

	case copilot.AssistantTurnEnd:
		d.printToolCompletion()

	case copilot.SessionIdle:
		select {
		case d.idleCh <- struct{}{}:
		default:
		}
	}
}

// WaitForIdle blocks until the session becomes idle or the context is cancelled.
// Returns the final assistant message content.
func (d *AgentDisplay) WaitForIdle(ctx context.Context) (string, error) {
	select {
	case <-d.idleCh:
		d.mu.Lock()
		content := d.finalContent
		d.mu.Unlock()
		log.Printf("[copilot] Session idle, response (%d chars)", len(content))
		return content, nil
	case <-ctx.Done():
		log.Printf("[copilot] Context cancelled while waiting for idle")
		return "", ctx.Err()
	}
}

// printToolCompletion prints a completion message for the current tool.
func (d *AgentDisplay) printToolCompletion() {
	d.mu.Lock()
	tool := d.currentTool
	toolInput := d.currentToolInput
	thought := d.latestThought
	d.mu.Unlock()

	if tool == "" {
		return
	}

	completionMsg := fmt.Sprintf("%s Ran %s", color.GreenString("✔︎"), color.MagentaString(tool))
	if toolInput != "" {
		completionMsg += " with " + color.HiBlackString(toolInput)
	}
	if thought != "" {
		completionMsg += color.MagentaString("\n  ◆ agent: ") + thought
	}

	d.canvas.Clear()
	fmt.Println(completionMsg)
}

// extractToolInputSummary creates a short summary of tool arguments for display.
func extractToolInputSummary(args any) string {
	if args == nil {
		return ""
	}

	argsMap, ok := args.(map[string]any)
	if !ok {
		return ""
	}

	prioritizedKeys := []string{"path", "pattern", "filename", "command"}
	for _, key := range prioritizedKeys {
		if val, exists := argsMap[key]; exists {
			s := fmt.Sprintf("%s: %v", key, val)
			return logging.TruncateString(s, 120)
		}
	}

	return ""
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
