// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/watch"
)

// CopilotAgent implements the Agent interface using the GitHub Copilot SDK.
// It manages a copilot.Session for multi-turn conversations and uses
// AgentDisplay for rendering session events as UX.
type CopilotAgent struct {
	session     *copilot.Session
	console     input.Console
	cleanupFunc AgentCleanup
	debug       bool

	watchForFileChanges bool
	lastDisplay         *AgentDisplay // tracks last display for usage metrics
}

// CopilotAgentOption is a functional option for configuring a CopilotAgent.
type CopilotAgentOption func(*CopilotAgent)

// WithCopilotDebug enables debug logging for the Copilot agent.
func WithCopilotDebug(debug bool) CopilotAgentOption {
	return func(a *CopilotAgent) { a.debug = debug }
}

// WithCopilotFileWatching enables file change detection after tool execution.
func WithCopilotFileWatching(enabled bool) CopilotAgentOption {
	return func(a *CopilotAgent) { a.watchForFileChanges = enabled }
}

// WithCopilotCleanup sets the cleanup function called on Stop().
func WithCopilotCleanup(fn AgentCleanup) CopilotAgentOption {
	return func(a *CopilotAgent) { a.cleanupFunc = fn }
}

// NewCopilotAgent creates a new CopilotAgent backed by the given copilot.Session.
func NewCopilotAgent(
	session *copilot.Session,
	console input.Console,
	opts ...CopilotAgentOption,
) *CopilotAgent {
	agent := &CopilotAgent{
		session:             session,
		console:             console,
		watchForFileChanges: true,
	}

	for _, opt := range opts {
		opt(agent)
	}

	return agent
}

// SendMessage sends a message to the Copilot agent session and waits for a response.
// It creates an AgentDisplay that subscribes to session events for real-time UX rendering.
func (a *CopilotAgent) SendMessage(ctx context.Context, args ...string) (string, error) {
	var watcher watch.Watcher
	if a.watchForFileChanges {
		var err error
		watcher, err = watch.NewWatcher(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to start watcher: %w", err)
		}
	}

	// Create display for this message turn
	display := NewAgentDisplay(a.console)
	a.lastDisplay = display
	displayCtx, displayCancel := context.WithCancel(ctx)

	cleanup, err := display.Start(displayCtx)
	if err != nil {
		displayCancel()
		return "", err
	}

	defer func() {
		displayCancel()
		time.Sleep(100 * time.Millisecond)
		cleanup()
		if a.watchForFileChanges {
			watcher.PrintChangedFiles(ctx)
		}
	}()

	// Subscribe display to session events
	unsubscribe := a.session.On(display.HandleEvent)
	defer unsubscribe()

	prompt := strings.Join(args, "\n")
	log.Printf("[copilot] SendMessage: sending prompt (%d chars)...", len(prompt))

	// Send prompt (non-blocking) in interactive mode
	_, err = a.session.Send(ctx, copilot.MessageOptions{
		Prompt: prompt,
		Mode:   string(copilot.Interactive),
	})
	if err != nil {
		log.Printf("[copilot] SendMessage: send error: %v", err)
		return "", fmt.Errorf("copilot agent error: %w", err)
	}

	// Wait for idle — display handles all UX rendering
	return display.WaitForIdle(ctx)
}

// SendMessageWithRetry sends a message and prompts the user to retry on error.
func (a *CopilotAgent) SendMessageWithRetry(ctx context.Context, args ...string) (string, error) {
	for {
		agentOutput, err := a.SendMessage(ctx, args...)
		if err != nil {
			if agentOutput != "" {
				a.console.Message(ctx, output.WithMarkdown(agentOutput))
			}

			if shouldRetry := a.handleErrorWithRetryPrompt(ctx, err); shouldRetry {
				continue
			}
			return "", err
		}

		return agentOutput, nil
	}
}

// Stop terminates the agent and performs cleanup.
func (a *CopilotAgent) Stop() error {
	if a.cleanupFunc != nil {
		return a.cleanupFunc()
	}
	return nil
}

// UsageSummary returns a formatted string with session usage metrics.
// Returns empty string if no usage data was collected.
func (a *CopilotAgent) UsageSummary() string {
	if a.lastDisplay == nil {
		return ""
	}
	return a.lastDisplay.UsageSummary()
}

func (a *CopilotAgent) handleErrorWithRetryPrompt(ctx context.Context, err error) bool {
	a.console.Message(ctx, "")
	a.console.Message(ctx, output.WithErrorFormat("Error occurred: %s", err.Error()))
	a.console.Message(ctx, "")

	retryPrompt := uxlib.NewConfirm(&uxlib.ConfirmOptions{
		Message:      "Oops, my reply didn't quite fit what was needed. Want me to try again?",
		DefaultValue: uxlib.Ptr(true),
		HelpMessage:  "Choose 'yes' to retry the current step, or 'no' to stop the initialization.",
	})

	shouldRetry, promptErr := retryPrompt.Ask(ctx)
	if promptErr != nil {
		return false
	}

	return shouldRetry != nil && *shouldRetry
}
