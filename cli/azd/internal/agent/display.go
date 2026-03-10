// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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
	currentTool      string
	currentToolInput string
	toolStartTime    time.Time
	finalContent     string
	reasoningBuf     strings.Builder
	lastIntent       string
	activeSubagent   string // display name of active sub-agent, empty if none
	inSubagent       bool

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
		Text: "Thinking...",
	})

	d.canvas = uxlib.NewCanvas(
		// Reasoning display — above the spinner, with blank lines before/after
		uxlib.NewVisualElement(func(printer uxlib.Printer) error {
			d.mu.Lock()
			reasoning := d.reasoningBuf.String()
			d.mu.Unlock()

			if reasoning == "" {
				return nil
			}

			// Show the last ~5 lines of reasoning above the spinner
			lines := strings.Split(strings.TrimSpace(reasoning), "\n")
			const maxLines = 5
			start := 0
			if len(lines) > maxLines {
				start = len(lines) - maxLines
			}
			tail := lines[start:]

			printer.Fprintln()
			for _, line := range tail {
				printer.Fprintln(color.HiBlackString("  %s", strings.TrimSpace(line)))
			}
			printer.Fprintln()
			return nil
		}),
		// Blank line before spinner
		uxlib.NewVisualElement(func(printer uxlib.Printer) error {
			printer.Fprintln()
			return nil
		}),
		d.spinner,
		// Blank line after spinner
		uxlib.NewVisualElement(func(printer uxlib.Printer) error {
			printer.Fprintln()
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
				nested := d.inSubagent
				d.mu.Unlock()

				if tool != "" {
					elapsed := int(time.Since(startTime).Seconds())
					prefix := ""
					if nested {
						prefix = "  "
					}
					text := fmt.Sprintf("%sRunning %s", prefix, color.MagentaString(tool))
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
		d.mu.Lock()
		intent := d.lastIntent
		d.currentTool = ""
		d.currentToolInput = ""
		d.reasoningBuf.Reset()
		d.finalContent = "" // Reset — only the last turn's message matters
		d.mu.Unlock()
		if intent != "" {
			d.spinner.UpdateText(intent)
		} else {
			d.spinner.UpdateText("Thinking...")
		}

	case copilot.AssistantIntent:
		if event.Data.Intent != nil && *event.Data.Intent != "" {
			intent := logging.TruncateString(*event.Data.Intent, 80)
			d.mu.Lock()
			d.lastIntent = intent
			d.mu.Unlock()
			d.spinner.UpdateText(intent)
		}

	case copilot.AssistantReasoning:
		if event.Data.ReasoningText != nil && *event.Data.ReasoningText != "" {
			d.mu.Lock()
			d.reasoningBuf.WriteString(*event.Data.ReasoningText)
			d.mu.Unlock()
			d.canvas.Update()
		}

	case copilot.AssistantReasoningDelta:
		if event.Data.DeltaContent != nil && *event.Data.DeltaContent != "" {
			d.mu.Lock()
			d.reasoningBuf.WriteString(*event.Data.DeltaContent)
			d.mu.Unlock()
			d.canvas.Update()
		}

	case copilot.AssistantStreamingDelta:
		// Some models emit reasoning via streaming delta with phase="thinking"
		if event.Data.Phase != nil && *event.Data.Phase == "thinking" {
			if event.Data.DeltaContent != nil && *event.Data.DeltaContent != "" {
				d.mu.Lock()
				d.reasoningBuf.WriteString(*event.Data.DeltaContent)
				d.mu.Unlock()
				d.canvas.Update()
			}
		}

	case copilot.AssistantMessage:
		if event.Data.Content != nil {
			log.Printf("[copilot-display] assistant.message received (%d chars)", len(*event.Data.Content))
			d.mu.Lock()
			d.finalContent = *event.Data.Content
			d.mu.Unlock()
		} else {
			log.Println("[copilot-display] assistant.message received with nil content")
		}

	case copilot.ToolExecutionStart:
		toolName := derefStr(event.Data.ToolName)
		if toolName == "" {
			toolName = derefStr(event.Data.MCPToolName)
		}
		if toolName == "" {
			return
		}

		// Suppress internal/UX tools from display
		suppressedTools := map[string]bool{
			"report_intent": true,
			"ask_user":      true,
			"task":          true,
			"skill":         true,
		}

		// The report_intent tool carries the agent's current intent as its argument.
		if toolName == "report_intent" {
			if intent := extractIntentFromArgs(event.Data.Arguments); intent != "" {
				d.mu.Lock()
				d.lastIntent = intent
				d.mu.Unlock()
				d.spinner.UpdateText(intent)
			}
			return
		}

		// Skip other suppressed tools and skill invocations
		if suppressedTools[toolName] || strings.HasPrefix(toolName, "skill:") {
			return
		}

		// Print completion for previous tool and flush any accumulated reasoning
		d.printToolCompletion()
		d.flushReasoning()

		toolInput := extractToolInputSummary(event.Data.Arguments)

		d.mu.Lock()
		d.currentTool = toolName
		d.currentToolInput = toolInput
		d.toolStartTime = time.Now()
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
		intent := d.lastIntent
		d.mu.Unlock()
		if intent != "" {
			d.spinner.UpdateText(intent)
		} else {
			d.spinner.UpdateText("Thinking...")
		}

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
			fmt.Println()
			fmt.Println(color.CyanString("◇ Using skill: %s", name))
			fmt.Println()
		}

	case copilot.SubagentStarted:
		displayName := derefStr(event.Data.AgentDisplayName)
		if displayName == "" {
			displayName = derefStr(event.Data.AgentName)
		}
		description := derefStr(event.Data.AgentDescription)

		d.mu.Lock()
		d.activeSubagent = displayName
		d.inSubagent = true
		d.mu.Unlock()

		if displayName != "" {
			d.canvas.Clear()
			fmt.Println()
			msg := color.MagentaString("◆ %s", displayName)
			if description != "" {
				msg += color.HiBlackString(" — %s", description)
			}
			fmt.Println(msg)
		}

	case copilot.SubagentCompleted:
		displayName := derefStr(event.Data.AgentDisplayName)
		if displayName == "" {
			displayName = derefStr(event.Data.AgentName)
		}
		summary := derefStr(event.Data.Summary)

		d.printToolCompletion()

		d.mu.Lock()
		d.activeSubagent = ""
		d.inSubagent = false
		d.mu.Unlock()

		if displayName != "" {
			d.canvas.Clear()
			msg := fmt.Sprintf("%s %s completed", color.GreenString("✔︎"), color.MagentaString(displayName))
			if summary != "" {
				msg += "\n" + color.HiBlackString("  %s", logging.TruncateString(summary, 200))
			}
			fmt.Println(msg)
		}

	case copilot.SubagentFailed:
		displayName := derefStr(event.Data.AgentDisplayName)
		if displayName == "" {
			displayName = derefStr(event.Data.AgentName)
		}
		errMsg := derefStr(event.Data.Message)

		d.mu.Lock()
		d.activeSubagent = ""
		d.inSubagent = false
		d.mu.Unlock()

		if displayName != "" {
			d.canvas.Clear()
			fmt.Println(output.WithErrorFormat("✖ %s failed: %s", displayName, errMsg))
		}

	case copilot.SubagentDeselected:
		d.mu.Lock()
		d.activeSubagent = ""
		d.inSubagent = false
		d.mu.Unlock()

	case copilot.AssistantTurnEnd:
		d.printToolCompletion()
		d.flushReasoning()

	case copilot.SessionIdle:
		d.mu.Lock()
		hasContent := d.finalContent != ""
		contentLen := len(d.finalContent)
		d.mu.Unlock()

		log.Printf("[copilot-display] session.idle received (hasContent=%v, contentLen=%d)", hasContent, contentLen)

		// Only signal idle when we have a final assistant message.
		// Ignore early idle events (e.g., between permission prompts).
		if hasContent {
			select {
			case d.idleCh <- struct{}{}:
				log.Println("[copilot-display] signaled idleCh")
			default:
				log.Println("[copilot-display] idleCh already full")
			}
		}

	case copilot.SessionTaskComplete, copilot.SessionShutdown:
		// Also signal completion on task_complete or shutdown
		log.Printf("[copilot-display] %s received, signaling completion", event.Type)
		select {
		case d.idleCh <- struct{}{}:
		default:
		}
	}
}

// WaitForIdle blocks until the session becomes idle or the context is cancelled.
// Returns the final assistant message content.
func (d *AgentDisplay) WaitForIdle(ctx context.Context) (string, error) {
	log.Println("[copilot-display] WaitForIdle: waiting...")
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
// When inside a subagent, the output is indented to show nesting.
func (d *AgentDisplay) printToolCompletion() {
	d.mu.Lock()
	tool := d.currentTool
	toolInput := d.currentToolInput
	nested := d.inSubagent
	d.mu.Unlock()

	if tool == "" {
		return
	}

	indent := ""
	if nested {
		indent = "  "
	}

	completionMsg := fmt.Sprintf("%s%s Ran %s", indent, color.GreenString("✔︎"), color.MagentaString(tool))
	if toolInput != "" {
		completionMsg += " with " + color.HiBlackString(toolInput)
	}

	d.canvas.Clear()
	fmt.Println(completionMsg)
}

// flushReasoning prints the full accumulated reasoning with markdown rendering
// and resets the buffer. Called when transitioning to a new phase (tool start, turn end).
func (d *AgentDisplay) flushReasoning() {
	d.mu.Lock()
	reasoning := d.reasoningBuf.String()
	d.reasoningBuf.Reset()
	d.mu.Unlock()

	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return
	}

	d.canvas.Clear()
	fmt.Println()
	fmt.Println(output.WithMarkdown(reasoning))
	fmt.Println()
}

// extractToolInputSummary creates a short summary of tool arguments for display.
// Paths are shown relative to cwd when possible.
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
			valStr := fmt.Sprintf("%v", val)
			// Make paths relative to cwd for cleaner display
			if key == "path" || key == "filename" {
				valStr = toRelativePath(valStr)
			}
			s := fmt.Sprintf("%s: %s", key, valStr)
			return logging.TruncateString(s, 120)
		}
	}

	return ""
}

// toRelativePath converts an absolute path to relative if it's under cwd
// or a known skills directory.
func toRelativePath(p string) string {
	// Try cwd first
	cwd, err := os.Getwd()
	if err == nil {
		rel, err := filepath.Rel(cwd, p)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}

	// Try skills directories (e.g., ~/.copilot/installed-plugins/.../skills/)
	home, err := os.UserHomeDir()
	if err == nil {
		pluginsRoot := filepath.Join(home, ".copilot", "installed-plugins")
		rel, err := filepath.Rel(pluginsRoot, p)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}

	return p
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// extractIntentFromArgs extracts the intent text from report_intent tool arguments.
func extractIntentFromArgs(args any) string {
	if args == nil {
		return ""
	}

	argsMap, ok := args.(map[string]any)
	if !ok {
		return ""
	}

	// The intent may be in "intent", "description", or "text" field
	for _, key := range []string{"intent", "description", "text"} {
		if val, exists := argsMap[key]; exists {
			if s, ok := val.(string); ok && s != "" {
				return logging.TruncateString(s, 80)
			}
		}
	}

	return ""
}
