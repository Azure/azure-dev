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

	"github.com/azure/azure-dev/cli/azd/internal/agent/logging"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/watch"
	"github.com/fatih/color"
)

// CopilotAgent implements the Agent interface using the GitHub Copilot SDK.
// It manages a copilot.Session for multi-turn conversations and streams
// session events for UX rendering.
type CopilotAgent struct {
	session     *copilot.Session
	console     input.Console
	thoughtChan chan logging.Thought
	cleanupFunc AgentCleanup
	debug       bool

	watchForFileChanges bool
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

// WithCopilotThoughtChannel sets the channel for streaming thoughts to the UX layer.
func WithCopilotThoughtChannel(ch chan logging.Thought) CopilotAgentOption {
	return func(a *CopilotAgent) { a.thoughtChan = ch }
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
func (a *CopilotAgent) SendMessage(ctx context.Context, args ...string) (string, error) {
	thoughtsCtx, cancelCtx := context.WithCancel(ctx)

	var watcher watch.Watcher
	if a.watchForFileChanges {
		var err error
		watcher, err = watch.NewWatcher(ctx)
		if err != nil {
			cancelCtx()
			return "", fmt.Errorf("failed to start watcher: %w", err)
		}
	}

	cleanup, err := a.renderThoughts(thoughtsCtx)
	if err != nil {
		cancelCtx()
		return "", err
	}

	defer func() {
		cancelCtx()
		time.Sleep(100 * time.Millisecond)
		cleanup()
		if a.watchForFileChanges {
			watcher.PrintChangedFiles(ctx)
		}
	}()

	prompt := strings.Join(args, "\n")
	log.Printf("[copilot] SendMessage: sending prompt (%d chars)...", len(prompt))

	// Use Send (non-blocking) + wait for session.idle event ourselves.
	// This avoids SendAndWait's 60s default timeout — agent tasks can run as long as needed.
	idleCh := make(chan struct{}, 1)
	var lastAssistantContent string

	unsubscribe := a.session.On(func(event copilot.SessionEvent) {
		if event.Type == copilot.SessionIdle {
			select {
			case idleCh <- struct{}{}:
			default:
			}
		}
		if event.Type == copilot.AssistantMessage && event.Data.Content != nil {
			lastAssistantContent = *event.Data.Content
		}
	})
	defer unsubscribe()

	_, err = a.session.Send(ctx, copilot.MessageOptions{
		Prompt: prompt,
	})
	if err != nil {
		log.Printf("[copilot] SendMessage: send error: %v", err)
		return "", fmt.Errorf("copilot agent error: %w", err)
	}

	// Wait for idle (no timeout — runs until complete or context cancelled)
	select {
	case <-idleCh:
		log.Printf("[copilot] SendMessage: session idle, response (%d chars)", len(lastAssistantContent))
	case <-ctx.Done():
		log.Printf("[copilot] SendMessage: context cancelled")
		return "", ctx.Err()
	}

	return lastAssistantContent, nil
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

// renderThoughts reuses the same UX rendering pattern as ConversationalAzdAiAgent,
// reading from the thought channel and displaying spinner + tool completion messages.
func (a *CopilotAgent) renderThoughts(ctx context.Context) (func(), error) {
	if a.thoughtChan == nil {
		return func() {}, nil
	}

	var latestThought string

	spinner := uxlib.NewSpinner(&uxlib.SpinnerOptions{
		Text: "Processing...",
	})

	canvas := uxlib.NewCanvas(
		spinner,
		uxlib.NewVisualElement(func(printer uxlib.Printer) error {
			printer.Fprintln()
			printer.Fprintln()

			if latestThought != "" {
				printer.Fprintln(color.HiBlackString(latestThought))
				printer.Fprintln()
				printer.Fprintln()
			}

			return nil
		}))

	printToolCompletion := func(action, actionInput, thought string) {
		if action == "" {
			return
		}

		completionMsg := fmt.Sprintf("%s Ran %s", color.GreenString("✔︎"), color.MagentaString(action))
		if actionInput != "" {
			completionMsg += " with " + color.HiBlackString(actionInput)
		}
		if thought != "" {
			completionMsg += color.MagentaString("\n\n◆ agent: ") + thought
		}

		canvas.Clear()
		fmt.Println(completionMsg)
		fmt.Println()
	}

	go func() {
		defer canvas.Clear()

		var latestAction string
		var latestActionInput string
		var spinnerText string
		var toolStartTime time.Time

		for {
			select {
			case thought := <-a.thoughtChan:
				if thought.Action != "" {
					if thought.Action != latestAction || thought.ActionInput != latestActionInput {
						printToolCompletion(latestAction, latestActionInput, latestThought)
					}
					latestAction = thought.Action
					latestActionInput = thought.ActionInput
					toolStartTime = time.Now()
				}
				if thought.Thought != "" {
					latestThought = thought.Thought
				}
			case <-ctx.Done():
				printToolCompletion(latestAction, latestActionInput, latestThought)
				return
			case <-time.After(200 * time.Millisecond):
			}

			if latestAction == "" {
				spinnerText = "Processing..."
			} else {
				elapsedSeconds := int(time.Since(toolStartTime).Seconds())
				spinnerText = fmt.Sprintf("Running %s tool", color.MagentaString(latestAction))
				if latestActionInput != "" {
					spinnerText += " with " + color.HiBlackString(latestActionInput)
				}
				spinnerText += "..."
				spinnerText += color.HiBlackString(fmt.Sprintf("\n(%ds, CTRL C to exit agentic mode)", elapsedSeconds))
			}

			spinner.UpdateText(spinnerText)
			canvas.Update()
		}
	}()

	cleanup := func() {
		canvas.Clear()
		canvas.Close()
	}

	return cleanup, canvas.Run()
}
