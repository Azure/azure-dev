// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"fmt"
	"strings"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// AgentResult is returned by SendMessage with response content and metrics.
type AgentResult struct {
	// Content is the final assistant message text.
	Content string
	// SessionID is the session identifier for resuming later.
	SessionID string
	// Usage contains token and cost metrics for the session.
	Usage UsageMetrics
}

// UsageMetrics tracks resource consumption for an agent session.
type UsageMetrics struct {
	Model           string
	InputTokens     float64
	OutputTokens    float64
	BillingRate     float64 // per-request cost multiplier (e.g., 1.0x, 2.0x)
	PremiumRequests float64
	DurationMS      float64
}

// TotalTokens returns input + output tokens.
func (u UsageMetrics) TotalTokens() float64 {
	return u.InputTokens + u.OutputTokens
}

// Format returns a multi-line formatted string for display.
func (u UsageMetrics) Format() string {
	if u.InputTokens == 0 && u.OutputTokens == 0 {
		return ""
	}

	lines := []string{
		output.WithGrayFormat("  Session usage:"),
	}

	if u.Model != "" {
		lines = append(lines, output.WithGrayFormat("  • Model:            %s", u.Model))
	}
	lines = append(lines, output.WithGrayFormat("  • Input tokens:     %s", formatTokenCount(u.InputTokens)))
	lines = append(lines, output.WithGrayFormat("  • Output tokens:    %s", formatTokenCount(u.OutputTokens)))
	lines = append(lines, output.WithGrayFormat("  • Total tokens:     %s", formatTokenCount(u.TotalTokens())))

	if u.BillingRate > 0 {
		lines = append(lines, output.WithGrayFormat("  • Billing rate:     %.0fx per request", u.BillingRate))
	}
	if u.PremiumRequests > 0 {
		lines = append(lines, output.WithGrayFormat("  • Premium requests: %.0f", u.PremiumRequests))
	}
	if u.DurationMS > 0 {
		seconds := u.DurationMS / 1000
		if seconds >= 60 {
			lines = append(lines, output.WithGrayFormat("  • API duration:     %.0fm %.0fs",
				seconds/60, float64(int(seconds)%60)))
		} else {
			lines = append(lines, output.WithGrayFormat("  • API duration:     %.1fs", seconds))
		}
	}

	var result strings.Builder
	for i, line := range lines {
		if i > 0 {
			result.WriteString("\n")
		}
		result.WriteString(line)
	}
	return result.String()
}

func formatTokenCount(tokens float64) string {
	if tokens >= 1_000_000 {
		return fmt.Sprintf("%.1fM", tokens/1_000_000)
	}
	if tokens >= 1_000 {
		return fmt.Sprintf("%.1fK", tokens/1_000)
	}
	return fmt.Sprintf("%.0f", tokens)
}

// InitResult is returned by Initialize with configuration state.
type InitResult struct {
	// Model is the selected model name (empty = default).
	Model string
	// ReasoningEffort is the selected reasoning level.
	ReasoningEffort string
	// IsFirstRun is true if the user was prompted for configuration.
	IsFirstRun bool
}

// AgentMode represents the operating mode for the agent.
type AgentMode string

const (
	// AgentModeInteractive asks for approval before executing tools.
	AgentModeInteractive AgentMode = "interactive"
	// AgentModeAutopilot executes tools automatically without approval.
	AgentModeAutopilot AgentMode = "autopilot"
	// AgentModePlan creates a plan first, then executes after approval.
	AgentModePlan AgentMode = "plan"
)

// AgentOption configures agent creation via the factory.
type AgentOption func(*CopilotAgent)

// WithModel overrides the configured model for this agent.
func WithModel(model string) AgentOption {
	return func(a *CopilotAgent) { a.modelOverride = model }
}

// WithReasoningEffort overrides the configured reasoning effort.
func WithReasoningEffort(effort string) AgentOption {
	return func(a *CopilotAgent) { a.reasoningEffortOverride = effort }
}

// WithMode sets the agent mode.
func WithMode(mode AgentMode) AgentOption {
	return func(a *CopilotAgent) { a.mode = mode }
}

// WithDebug enables debug logging.
func WithDebug(debug bool) AgentOption {
	return func(a *CopilotAgent) { a.debug = debug }
}

// InitOption configures the Initialize call.
type InitOption func(*initOptions)

type initOptions struct {
	forcePrompt bool
}

// WithForcePrompt forces the config prompts even if config exists.
func WithForcePrompt() InitOption {
	return func(o *initOptions) { o.forcePrompt = true }
}

// SendOption configures a SendMessage call.
type SendOption func(*sendOptions)

type sendOptions struct {
	sessionID string
}

// WithSessionID resumes the specified session instead of creating a new one.
func WithSessionID(id string) SendOption {
	return func(o *sendOptions) { o.sessionID = id }
}

// SessionMetadata contains metadata about a previous session.
type SessionMetadata = copilot.SessionMetadata
