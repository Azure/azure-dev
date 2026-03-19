// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	"fmt"
	"strings"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/watch"
)

// AgentResult is returned by SendMessage with session and usage metadata.
type AgentResult struct {
	// SessionID is the session identifier for resuming later.
	SessionID string
	// Usage contains token and cost metrics for this turn.
	Usage UsageMetrics
	// FileChanges contains files created/modified/deleted during this turn.
	FileChanges watch.FileChanges
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

// String returns a multi-line formatted string for display.
func (u UsageMetrics) String() string {
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
	lines = append(lines, output.WithGrayFormat("  • Premium requests: %.0f", u.PremiumRequests))
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

// AgentMetrics contains cumulative session metrics for usage and file changes.
type AgentMetrics struct {
	// Usage contains cumulative token and cost metrics.
	Usage UsageMetrics
	// FileChanges contains accumulated file changes across all SendMessage calls.
	FileChanges watch.FileChanges
}

// String returns a formatted display of file changes followed by usage metrics.
func (m AgentMetrics) String() string {
	var parts []string
	if s := m.FileChanges.String(); s != "" {
		parts = append(parts, s)
	}
	if s := m.Usage.String(); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n")
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

// WithSystemMessage appends a custom system message to the agent's default system prompt.
func WithSystemMessage(msg string) AgentOption {
	return func(a *CopilotAgent) { a.systemMessageOverride = msg }
}

// WithMode sets the agent mode.
func WithMode(mode AgentMode) AgentOption {
	return func(a *CopilotAgent) { a.mode = mode }
}

// WithDebug enables debug logging.
func WithDebug(debug bool) AgentOption {
	return func(a *CopilotAgent) { a.debug = debug }
}

// WithHeadless enables headless mode, suppressing all console output.
// In headless mode, the agent uses a silent collector instead of the
// interactive display, and defaults to autopilot mode.
func WithHeadless(headless bool) AgentOption {
	return func(a *CopilotAgent) { a.headless = headless }
}

// OnSessionStarted registers a callback that fires when a session is created or resumed.
func OnSessionStarted(fn func(sessionID string)) AgentOption {
	return func(a *CopilotAgent) { a.onSessionStarted = fn }
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

// SessionEvent represents a single event from the Copilot session log.
type SessionEvent = copilot.SessionEvent

// Agent defines the interface for Copilot agent operations.
// Used by the gRPC service layer to decouple from the concrete CopilotAgent implementation.
type Agent interface {
	// Initialize handles first-run configuration (model/reasoning setup).
	Initialize(ctx context.Context, opts ...InitOption) (*InitResult, error)
	// SendMessage sends a prompt and returns per-turn results.
	SendMessage(ctx context.Context, prompt string, opts ...SendOption) (*AgentResult, error)
	// SendMessageWithRetry wraps SendMessage with interactive retry-on-error UX.
	SendMessageWithRetry(ctx context.Context, prompt string, opts ...SendOption) (*AgentResult, error)
	// ListSessions returns previous sessions for the given working directory.
	ListSessions(ctx context.Context, cwd string) ([]SessionMetadata, error)
	// GetMetrics returns cumulative session metrics (usage + file changes).
	GetMetrics() AgentMetrics
	// GetMessages returns the session event log from the Copilot SDK.
	GetMessages(ctx context.Context) ([]SessionEvent, error)
	// SessionID returns the current session ID, or empty if no session exists.
	SessionID() string
	// Stop terminates the agent and releases resources.
	Stop() error
}

// AgentFactory creates Agent instances with all dependencies wired.
type AgentFactory interface {
	// Create builds a new Agent with the given options.
	Create(ctx context.Context, opts ...AgentOption) (Agent, error)
}
