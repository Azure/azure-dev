// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
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
	activeTools      map[string]*toolState // keyed by toolCallID or generated ID
	currentToolID    string                // most recent tool for spinner display
	toolCounter      int                   // monotonic counter for generating unique tool IDs
	reasoningBuf     strings.Builder
	lastIntent       string
	activeSubagent   string // display name of active sub-agent, empty if none
	inSubagent       bool
	lastPrintedBlank bool // tracks if last output ended with a blank line
	messageReceived  bool // tracks if an assistant message has been received
	paused           bool // true while interactive prompts are active
	pendingIdle      bool // true when SessionIdle arrived before messageReceived

	// renderGuard serializes ticker renders with Pause/Resume.
	// Pause() write-locks to block ticker renders; ticker read-locks around canvas.Update().
	renderGuard sync.RWMutex

	// Usage metrics — accumulated from assistant.usage events
	totalInputTokens  float64
	totalOutputTokens float64
	billingRate       float64
	totalDurationMS   float64
	premiumRequests   float64
	lastModel         string

	// Lifecycle
	idleCh chan struct{}
	ctx    context.Context
}

// toolState tracks the state of an in-progress tool execution.
type toolState struct {
	name      string
	input     string // short summary for spinner display
	detail    string // richer detail for completion line (e.g., diff stats)
	subDetail string // optional second line (e.g., shell command text)
	errorMsg  string // error message on failure
	startTime time.Time
	failed    bool
	nested    bool // inside a subagent
}

// NewAgentDisplay creates a new AgentDisplay.
func NewAgentDisplay(console input.Console) *AgentDisplay {
	return &AgentDisplay{
		console:     console,
		activeTools: make(map[string]*toolState),
		idleCh:      make(chan struct{}, 1),
	}
}

// Pause stops canvas updates for interactive prompts.
// Write-locks renderGuard so any in-flight ticker render completes
// and future renders are blocked until Resume().
func (d *AgentDisplay) Pause() {
	d.renderGuard.Lock()
	d.mu.Lock()
	d.paused = true
	d.mu.Unlock()
	d.canvas.Clear()
}

// Resume restarts canvas updates after an interactive prompt.
func (d *AgentDisplay) Resume() {
	d.mu.Lock()
	d.paused = false
	d.mu.Unlock()
	d.renderGuard.Unlock()
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

			// Trim only leading/trailing whitespace from the whole block
			reasoning = strings.TrimSpace(reasoning)
			if reasoning == "" {
				return nil
			}

			// Show the last ~5 lines of reasoning above the spinner
			lines := strings.Split(reasoning, "\n")
			const maxLines = 5
			start := 0
			if len(lines) > maxLines {
				start = len(lines) - maxLines
			}
			tail := lines[start:]

			printer.Fprintln()
			for _, line := range tail {
				printer.Fprintln(color.HiBlackString("  %s", line))
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
		// Sub-detail lines below spinner (e.g., shell command, MCP args)
		uxlib.NewVisualElement(func(printer uxlib.Printer) error {
			d.mu.Lock()
			ts := d.activeTools[d.currentToolID]
			d.mu.Unlock()

			if ts != nil && ts.subDetail != "" {
				printer.Fprintln() // newline after spinner text
				lines := strings.Split(ts.subDetail, "\n")
				for i, line := range lines {
					connector := "├"
					if i == len(lines)-1 {
						connector = "└"
					}
					printer.Fprintln(color.HiBlackString("  %s %s", connector, line))
				}
			}
			return nil
		}),
		// Newline after spinner, then cancel hint
		uxlib.NewVisualElement(func(printer uxlib.Printer) error {
			printer.Fprintln()
			printer.Fprintln(color.HiBlackString("  Press %s to cancel", color.New(color.Bold).Sprint("Ctrl+C")))
			return nil
		}),
		// Blank line after
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
				d.renderGuard.RLock()

				d.mu.Lock()
				isPaused := d.paused
				ts := d.activeTools[d.currentToolID]
				d.mu.Unlock()

				if isPaused {
					d.renderGuard.RUnlock()
					continue
				}

				if ts != nil {
					elapsed := int(time.Since(ts.startTime).Seconds())
					prefix := ""
					if ts.nested {
						prefix = "  "
					}
					text := fmt.Sprintf("%sRunning %s", prefix, color.MagentaString(ts.name))
					if ts.input != "" {
						text += " " + color.HiBlackString(ts.input)
					}
					text += color.HiBlackString("...") + color.HiBlackString(fmt.Sprintf(" (%ds)", elapsed))
					d.spinner.UpdateText(text)
				}

				d.canvas.Update()
				d.renderGuard.RUnlock()
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
		d.activeTools = make(map[string]*toolState)
		d.currentToolID = ""
		d.reasoningBuf.Reset()
		d.messageReceived = false
		d.pendingIdle = false
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
			content := strings.TrimSpace(*event.Data.Content)
			log.Printf("[copilot-display] assistant.message received (%d chars)", len(content))
			if content != "" {
				d.canvas.Clear()
				d.printSeparated(output.WithMarkdown(content))
			}
			d.mu.Lock()
			d.messageReceived = true
			hasPendingIdle := d.pendingIdle
			d.pendingIdle = false
			d.mu.Unlock()

			// If SessionIdle arrived before this message, flush the deferred signal now
			if hasPendingIdle {
				log.Println("[copilot-display] flushing deferred idle signal after message received")
				select {
				case d.idleCh <- struct{}{}:
					log.Println("[copilot-display] signaled idleCh (deferred)")
				default:
					log.Println("[copilot-display] idleCh already full (deferred)")
				}
			}
		} else {
			log.Println("[copilot-display] assistant.message received with nil content")
		}

	case copilot.AssistantUsage:
		d.mu.Lock()
		if event.Data.InputTokens != nil {
			d.totalInputTokens += *event.Data.InputTokens
		}
		if event.Data.OutputTokens != nil {
			d.totalOutputTokens += *event.Data.OutputTokens
		}
		if event.Data.Cost != nil {
			d.billingRate = *event.Data.Cost // per-request multiplier, not cumulative
		}
		if event.Data.Duration != nil {
			d.totalDurationMS += *event.Data.Duration
		}
		if event.Data.Model != nil {
			d.lastModel = *event.Data.Model
		}
		d.mu.Unlock()

	case copilot.SessionUsageInfo, copilot.SessionShutdown:
		d.mu.Lock()
		if event.Data.TotalPremiumRequests != nil {
			d.premiumRequests = *event.Data.TotalPremiumRequests
		}
		d.mu.Unlock()

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

		// Skip suppressed tools and skill invocations
		if suppressedTools[toolName] || strings.HasPrefix(toolName, "skill:") {
			return
		}

		d.flushReasoning()

		toolInput := extractToolInputSummary(event.Data.Arguments)
		toolDetail, toolSubDetail := extractToolDetail(toolName, event.Data.Arguments)
		toolID := ""
		if event.Data.ToolCallID != nil {
			toolID = *event.Data.ToolCallID
		}

		d.mu.Lock()
		if toolID == "" {
			d.toolCounter++
			toolID = fmt.Sprintf("%s-%d", toolName, d.toolCounter)
		}
		d.activeTools[toolID] = &toolState{
			name:      toolName,
			input:     toolInput,
			detail:    toolDetail,
			subDetail: toolSubDetail,
			startTime: time.Now(),
			nested:    d.inSubagent,
		}
		d.currentToolID = toolID
		d.mu.Unlock()

		// Spinner text: show tool name + short input summary (not the full detail/subDetail)
		text := fmt.Sprintf("Running %s", color.MagentaString(toolName))
		if toolInput != "" {
			text += " " + color.HiBlackString(toolInput)
		}
		text += color.HiBlackString("...")
		d.spinner.UpdateText(text)

	case copilot.ToolExecutionProgress:
		if event.Data.ProgressMessage != nil && *event.Data.ProgressMessage != "" {
			d.mu.Lock()
			ts := d.activeTools[d.currentToolID]
			d.mu.Unlock()

			if ts != nil {
				text := fmt.Sprintf("Running %s", color.MagentaString(ts.name))
				text += " — " + color.HiBlackString(*event.Data.ProgressMessage)
				d.spinner.UpdateText(text)
			}
		}

	case copilot.ToolExecutionComplete:
		toolID := ""
		if event.Data.ToolCallID != nil {
			toolID = *event.Data.ToolCallID
		}

		d.mu.Lock()
		// If no toolCallID, try to find by tool name (fallback for SDK versions without IDs)
		if toolID == "" {
			toolName := derefStr(event.Data.ToolName)
			for id, ts := range d.activeTools {
				if ts.name == toolName {
					toolID = id
					break
				}
			}
		}
		ts := d.activeTools[toolID]
		if ts != nil {
			ts.failed = event.Data.Error != nil
			if event.Data.Error != nil {
				if event.Data.Error.ErrorClass != nil {
					ts.errorMsg = event.Data.Error.ErrorClass.Message
				} else if event.Data.Error.String != nil {
					ts.errorMsg = *event.Data.Error.String
				}
			}
		}
		d.mu.Unlock()

		d.printToolCompletionByID(toolID)

		d.mu.Lock()
		delete(d.activeTools, toolID)
		if d.currentToolID == toolID {
			d.currentToolID = ""
		}
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
		d.printSeparated(output.WithErrorFormat("Agent error: %s", msg))
		// Signal idle so WaitForIdle unblocks on fatal errors
		select {
		case d.idleCh <- struct{}{}:
		default:
		}

	case copilot.SessionWarning:
		if event.Data.Message != nil {
			d.canvas.Clear()
			d.printSeparated(output.WithWarningFormat("Warning: %s", *event.Data.Message))
		}

	case copilot.SkillInvoked:
		name := derefStr(event.Data.Name)
		if name != "" {
			d.canvas.Clear()
			msg := color.CyanString("◇ Using skill: %s", name)
			pluginName := derefStr(event.Data.PluginName)
			pluginVersion := derefStr(event.Data.PluginVersion)
			if pluginName != "" {
				origin := pluginName
				if pluginVersion != "" {
					origin += "@" + pluginVersion
				}
				msg += color.HiBlackString(" from %s", origin)
			}
			d.printSeparated(msg)
		}

	case copilot.SubagentStarted:
		displayName := derefStr(event.Data.AgentDisplayName)
		if displayName == "" {
			displayName = derefStr(event.Data.AgentName)
		}
		agentDesc := derefStr(event.Data.AgentDescription)

		d.mu.Lock()
		d.activeSubagent = displayName
		d.inSubagent = true
		d.mu.Unlock()

		if displayName != "" {
			d.canvas.Clear()
			msg := color.MagentaString("◆ %s", displayName)
			if agentDesc != "" {
				msg += "\n" + fmt.Sprintf("  %s %s", color.HiBlackString("└"), color.HiBlackString(agentDesc))
			}
			d.printSeparated(msg)
		}

	case copilot.SubagentCompleted:
		displayName := derefStr(event.Data.AgentDisplayName)
		if displayName == "" {
			displayName = derefStr(event.Data.AgentName)
		}
		summary := derefStr(event.Data.Summary)

		d.flushActiveTools()

		d.mu.Lock()
		d.activeSubagent = ""
		d.inSubagent = false
		d.mu.Unlock()

		if displayName != "" {
			d.canvas.Clear()
			msg := fmt.Sprintf("%s %s completed", color.GreenString("✔︎"), color.MagentaString(displayName))
			if summary != "" {
				msg += "\n" + fmt.Sprintf("  %s %s", color.HiBlackString("└"), color.HiBlackString(summary))
			}
			d.printSeparated(msg)
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
			d.printSeparated(output.WithErrorFormat("✖ %s failed: %s", displayName, errMsg))
		}

	case copilot.SubagentDeselected:
		d.mu.Lock()
		d.activeSubagent = ""
		d.inSubagent = false
		d.mu.Unlock()

	case copilot.AssistantTurnEnd:
		d.flushActiveTools()
		d.flushReasoning()

	case copilot.SessionIdle:
		d.mu.Lock()
		hasMessage := d.messageReceived
		if !hasMessage {
			d.pendingIdle = true
		}
		d.mu.Unlock()

		log.Printf("[copilot-display] session.idle received (hasMessage=%v)", hasMessage)

		if hasMessage {
			select {
			case d.idleCh <- struct{}{}:
				log.Println("[copilot-display] signaled idleCh")
			default:
				log.Println("[copilot-display] idleCh already full")
			}
		} else {
			log.Println("[copilot-display] session.idle deferred (no message yet)")
		}

	case copilot.SessionTaskComplete:
		// Also signal completion on task_complete
		log.Printf("[copilot-display] %s received, signaling completion", event.Type)
		select {
		case d.idleCh <- struct{}{}:
		default:
		}
	}
}

// WaitForIdle blocks until the session becomes idle or the context is cancelled.
// Returns the final assistant message content.
func (d *AgentDisplay) WaitForIdle(ctx context.Context) error {
	log.Println("[copilot-display] WaitForIdle: waiting...")
	select {
	case <-d.idleCh:
		log.Println("[copilot] Session idle")
		return nil
	case <-ctx.Done():
		log.Printf("[copilot] Context cancelled while waiting for idle")
		return ctx.Err()
	}
}

// GetUsageMetrics returns the accumulated usage metrics for this display session.
func (d *AgentDisplay) GetUsageMetrics() UsageMetrics {
	d.mu.Lock()
	defer d.mu.Unlock()

	return UsageMetrics{
		Model:           d.lastModel,
		InputTokens:     d.totalInputTokens,
		OutputTokens:    d.totalOutputTokens,
		BillingRate:     d.billingRate,
		PremiumRequests: d.premiumRequests,
		DurationMS:      d.totalDurationMS,
	}
}

// printToolCompletionByID prints a completion message for a specific tool by its ID.
func (d *AgentDisplay) printToolCompletionByID(toolID string) {
	d.mu.Lock()
	ts := d.activeTools[toolID]
	d.mu.Unlock()

	if ts == nil {
		return
	}

	d.printToolState(ts)
}

// flushActiveTools prints completion for all remaining active tools.
func (d *AgentDisplay) flushActiveTools() {
	d.mu.Lock()
	tools := make([]*toolState, 0, len(d.activeTools))
	for _, ts := range d.activeTools {
		tools = append(tools, ts)
	}
	d.activeTools = make(map[string]*toolState)
	d.currentToolID = ""
	d.mu.Unlock()

	for _, ts := range tools {
		d.printToolState(ts)
	}
}

// printToolState prints a single tool completion line with contextual verb and detail.
func (d *AgentDisplay) printToolState(ts *toolState) {
	indent := ""
	if ts.nested {
		indent = "  "
	}

	var completionMsg string
	if ts.failed {
		completionMsg = fmt.Sprintf("%s%s %s", indent, color.RedString("✖"), color.MagentaString(ts.name))
		if ts.input != "" {
			completionMsg += " " + color.HiBlackString(ts.input)
		}
	} else {
		verb := toolVerb(ts.name)
		completionMsg = fmt.Sprintf("%s%s %s", indent, color.GreenString("✔︎"), verb)
		// Detail is pre-colorized for tools with diff stats (edit, create);
		// other tools get gray detail text.
		if ts.detail != "" {
			completionMsg += " " + ts.detail
		} else if ts.input != "" {
			completionMsg += " " + color.HiBlackString(ts.input)
		}
	}

	d.canvas.Clear()

	// For multi-line output (error or tree), build the full block and use printSeparated
	hasSubLines := (ts.failed && ts.errorMsg != "") || (ts.subDetail != "" && !ts.failed)
	if hasSubLines {
		var block strings.Builder
		block.WriteString(completionMsg)

		if ts.failed && ts.errorMsg != "" {
			block.WriteString(fmt.Sprintf("\n%s  %s %s", indent, color.RedString("└"), color.RedString(ts.errorMsg)))
		}

		if ts.subDetail != "" && !ts.failed {
			lines := strings.Split(ts.subDetail, "\n")
			for i, line := range lines {
				connector := "├"
				if i == len(lines)-1 {
					connector = "└"
				}
				block.WriteString(fmt.Sprintf("\n%s  %s %s",
					indent, color.HiBlackString(connector), color.HiBlackString(line)))
			}
		}

		d.printSeparated(block.String())
	} else {
		d.printLine(completionMsg)
	}
}

// flushReasoning prints the full accumulated reasoning with markdown rendering
// and resets the buffer. Separated by blank lines before and after.
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
	d.printSeparated(output.WithMarkdown(reasoning))
}

// printSeparated prints content with a blank line before and after,
// avoiding duplicate blank lines. Content is trimmed to prevent stacking.
func (d *AgentDisplay) printSeparated(content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}

	d.mu.Lock()
	wasBlank := d.lastPrintedBlank
	d.mu.Unlock()

	if !wasBlank {
		fmt.Println()
	}
	fmt.Println(content)
	fmt.Println()

	d.mu.Lock()
	d.lastPrintedBlank = true
	d.mu.Unlock()
}

// printLine prints a single line without extra blank lines.
func (d *AgentDisplay) printLine(content string) {
	fmt.Println(content)

	d.mu.Lock()
	d.lastPrintedBlank = false
	d.mu.Unlock()
}

// extractToolInputSummary creates a short summary of tool arguments for the spinner.
// Prefers description/intent over raw values for a cleaner in-progress display.
func extractToolInputSummary(args any) string {
	if args == nil {
		return ""
	}

	argsMap, ok := args.(map[string]any)
	if !ok {
		return ""
	}

	// Prefer human-readable description for spinner display
	if desc := stringArg(argsMap, "description"); desc != "" {
		return logging.TruncateString(desc, 100)
	}
	if intent := stringArg(argsMap, "intent"); intent != "" {
		return logging.TruncateString(intent, 100)
	}

	prioritizedKeys := []string{"path", "pattern", "filename", "command"}
	for _, key := range prioritizedKeys {
		if val, exists := argsMap[key]; exists {
			valStr := fmt.Sprintf("%v", val)
			if key == "path" || key == "filename" {
				valStr = toRelativePath(valStr)
			}
			s := fmt.Sprintf("%s: %s", key, valStr)
			return logging.TruncateString(s, 120)
		}
	}

	return ""
}

// toolVerb returns a contextual past-tense verb for the tool completion line.
func toolVerb(toolName string) string {
	switch toolName {
	case "view":
		return "Read"
	case "edit":
		return "Edit"
	case "create":
		return "Create"
	case "grep":
		return "Search"
	case "glob":
		return "Find"
	case "powershell":
		return fmt.Sprintf("Ran %s", color.MagentaString("powershell"))
	case "web_fetch":
		return "Fetched"
	case "web_search":
		return "Searched"
	case "sql":
		return "Queried"
	default:
		return fmt.Sprintf("Ran %s", color.MagentaString(toolName))
	}
}

// extractToolDetail builds a rich completion detail string from tool arguments.
// Returns (detail, subDetail) where subDetail is an optional second line (e.g., shell command).
func extractToolDetail(toolName string, args any) (string, string) {
	if args == nil {
		return "", ""
	}
	argsMap, ok := args.(map[string]any)
	if !ok {
		return "", ""
	}

	switch toolName {
	case "view":
		return viewDetail(argsMap), ""
	case "edit":
		return editDetail(argsMap), ""
	case "create":
		return createDetail(argsMap), ""
	case "grep":
		return grepDetail(argsMap), ""
	case "glob":
		return globDetail(argsMap), ""
	case "powershell":
		return powershellDetail(argsMap)
	case "web_fetch":
		return color.HiBlackString(stringArg(argsMap, "url")), ""
	case "web_search":
		return color.HiBlackString(stringArg(argsMap, "query")), ""
	case "sql":
		return color.HiBlackString(stringArg(argsMap, "description")), ""
	default:
		return mcpToolDetail(toolName, argsMap)
	}
}

func viewDetail(args map[string]any) string {
	path := toRelativePath(stringArg(args, "path"))
	if path == "" {
		return ""
	}

	// Show line range if specified
	if viewRange, ok := args["view_range"]; ok {
		if rangeSlice, ok := viewRange.([]any); ok && len(rangeSlice) == 2 {
			start, _ := rangeSlice[0].(float64)
			end, _ := rangeSlice[1].(float64)
			if start > 0 && end > 0 {
				lines := int(end - start + 1)
				return fmt.Sprintf("%s %s", color.HiBlackString(path), color.HiBlackString("(%d lines)", lines))
			} else if start > 0 && end < 0 {
				return fmt.Sprintf("%s %s", color.HiBlackString(path), color.HiBlackString("(from line %d)", int(start)))
			}
		}
	}

	return color.HiBlackString(path)
}

func editDetail(args map[string]any) string {
	path := toRelativePath(stringArg(args, "path"))
	if path == "" {
		return ""
	}

	oldStr := stringArg(args, "old_str")
	newStr := stringArg(args, "new_str")
	if oldStr == "" && newStr == "" {
		return color.HiBlackString(path)
	}

	added := countLines(newStr)
	removed := countLines(oldStr)
	return fmt.Sprintf("%s (%s %s)",
		color.HiBlackString(path), color.GreenString("+%d", added), color.RedString("-%d", removed))
}

func createDetail(args map[string]any) string {
	path := toRelativePath(stringArg(args, "path"))
	if path == "" {
		return ""
	}

	fileText := stringArg(args, "file_text")
	if fileText == "" {
		return color.HiBlackString(path)
	}

	lines := countLines(fileText)
	return fmt.Sprintf("%s (%s)", color.HiBlackString(path), color.GreenString("+%d", lines))
}

func grepDetail(args map[string]any) string {
	pattern := stringArg(args, "pattern")
	if pattern == "" {
		return ""
	}

	detail := color.HiBlackString(logging.TruncateString(pattern, 60))
	if path := stringArg(args, "path"); path != "" {
		detail += color.HiBlackString(" in %s", toRelativePath(path))
	}
	return detail
}

func globDetail(args map[string]any) string {
	pattern := stringArg(args, "pattern")
	if pattern == "" {
		return ""
	}

	detail := color.HiBlackString(pattern)
	if path := stringArg(args, "path"); path != "" {
		detail += color.HiBlackString(" in %s", toRelativePath(path))
	}
	return detail
}

func powershellDetail(args map[string]any) (string, string) {
	desc := stringArg(args, "description")
	cmd := stringArg(args, "command")

	detail := ""
	if desc != "" {
		detail = color.HiBlackString(logging.TruncateString(desc, 80))
	}

	subDetail := ""
	if cmd != "" {
		subDetail = logging.TruncateString(cmd, 120)
	}

	return detail, subDetail
}

// mcpToolDetail formats MCP/unknown tool arguments as a tree of key: value lines.
func mcpToolDetail(toolName string, args map[string]any) (string, string) {
	if len(args) == 0 {
		return "", ""
	}

	// Build sub-detail lines from args, skipping large values
	var lines []string
	for key, val := range args {
		valStr := formatArgValue(val)
		if valStr != "" {
			lines = append(lines, fmt.Sprintf("%s: %s", key, valStr))
		}
	}

	if len(lines) == 0 {
		return "", ""
	}

	// Sort for deterministic output
	sort.Strings(lines)

	return "", strings.Join(lines, "\n")
}

// formatArgValue formats a tool argument value for display, truncating large values.
func formatArgValue(val any) string {
	switch v := val.(type) {
	case string:
		if len(v) > 120 {
			return logging.TruncateString(v, 120)
		}
		return v
	case float64:
		if v == float64(int(v)) {
			return fmt.Sprintf("%d", int(v))
		}
		return fmt.Sprintf("%g", v)
	case bool:
		return fmt.Sprintf("%v", v)
	case nil:
		return ""
	case map[string]any:
		return "{...}"
	case []any:
		if len(v) == 0 {
			return "[]"
		}
		return fmt.Sprintf("[%d items]", len(v))
	default:
		s := fmt.Sprintf("%v", v)
		return logging.TruncateString(s, 120)
	}
}

func stringArg(args map[string]any, key string) string {
	if val, ok := args[key]; ok {
		if s, ok := val.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", val)
	}
	return ""
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	// Content without a trailing newline still has at least one line
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
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
