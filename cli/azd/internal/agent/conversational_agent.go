// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/watch"
	"github.com/fatih/color"
	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/memory"
	"github.com/tmc/langchaingo/prompts"
)

//go:embed prompts/conversational.txt
var conversational_prompt_template string

// ConversationalAzdAiAgent represents an enhanced `azd` agent with conversation memory,
// tool filtering, and interactive capabilities
type ConversationalAzdAiAgent struct {
	*agentBase
	console input.Console
}

// NewConversationalAzdAiAgent creates a new conversational agent with memory, tool loading,
// and MCP sampling capabilities. It filters out excluded tools and configures the agent
// for interactive conversations with a high iteration limit for complex tasks.
func NewConversationalAzdAiAgent(llm llms.Model, console input.Console, opts ...AgentCreateOption) (Agent, error) {
	azdAgent := &ConversationalAzdAiAgent{
		agentBase: &agentBase{
			defaultModel:        llm,
			tools:               []common.AnnotatedTool{},
			watchForFileChanges: true,
		},
		console: console,
	}

	for _, opt := range opts {
		opt(azdAgent.agentBase)
	}

	// Default max iterations
	if azdAgent.maxIterations <= 0 {
		azdAgent.maxIterations = 100
	}

	smartMemory := memory.NewConversationBuffer(
		memory.WithInputKey("input"),
		memory.WithOutputKey("output"),
		memory.WithHumanPrefix("Human"),
		memory.WithAIPrefix("AI"),
	)

	promptTemplate := prompts.PromptTemplate{
		Template:       conversational_prompt_template,
		TemplateFormat: prompts.TemplateFormatGoTemplate,
		InputVariables: []string{"input", "agent_scratchpad"},
		PartialVariables: map[string]any{
			"tool_names":        toolNames(azdAgent.tools),
			"tool_descriptions": toolDescriptions(azdAgent.tools),
			"history":           "",
		},
	}

	// 4. Create agent with memory directly integrated
	conversationAgent := agents.NewConversationalAgent(llm, common.ToLangChainTools(azdAgent.tools),
		agents.WithPrompt(promptTemplate),
		agents.WithMemory(smartMemory),
		agents.WithCallbacksHandler(azdAgent.callbacksHandler),
		agents.WithReturnIntermediateSteps(),
	)

	// 5. Create executor without separate memory configuration since agent already has it
	executor := agents.NewExecutor(conversationAgent,
		agents.WithMaxIterations(azdAgent.maxIterations),
		agents.WithMemory(smartMemory),
		agents.WithCallbacksHandler(azdAgent.callbacksHandler),
		agents.WithReturnIntermediateSteps(),
	)

	azdAgent.executor = executor
	return azdAgent, nil
}

// SendMessage processes a single message through the agent and returns the response
func (aai *ConversationalAzdAiAgent) SendMessage(ctx context.Context, args ...string) (string, error) {
	thoughtsCtx, cancelCtx := context.WithCancel(ctx)

	var watcher watch.Watcher

	if aai.watchForFileChanges {
		var err error
		watcher, err = watch.NewWatcher(ctx)
		if err != nil {
			cancelCtx()
			return "", fmt.Errorf("failed to start watcher: %w", err)
		}
	}

	cleanup, err := aai.renderThoughts(thoughtsCtx)
	if err != nil {
		cancelCtx()
		return "", err
	}

	defer func() {
		cancelCtx()
		// Give a brief moment for the final tool message "Ran..." to be printed
		time.Sleep(100 * time.Millisecond)
		cleanup()

		if aai.watchForFileChanges {
			watcher.PrintChangedFiles(ctx)
		}
	}()

	output, err := chains.Run(ctx, aai.executor, strings.Join(args, "\n"))
	if err != nil {
		return "", err
	}

	return output, nil
}

func (aai *ConversationalAzdAiAgent) SendMessageWithRetry(ctx context.Context, args ...string) (string, error) {
	for {
		agentOutput, err := aai.SendMessage(ctx, args...)
		if err != nil {
			if agentOutput != "" {
				aai.console.Message(ctx, output.WithMarkdown(agentOutput))
			}

			// Display error and ask if user wants to retry
			if shouldRetry := aai.handleErrorWithRetryPrompt(ctx, err); shouldRetry {
				continue // Retry the same operation
			}

			return "", err // User chose not to retry, return original error
		}

		return agentOutput, nil
	}
}

// handleErrorWithRetryPrompt displays an error and prompts user for retry
func (aai *ConversationalAzdAiAgent) handleErrorWithRetryPrompt(ctx context.Context, err error) bool {
	// Display error in error format
	aai.console.Message(ctx, "")
	aai.console.Message(ctx, output.WithErrorFormat("Error occurred: %s", err.Error()))
	aai.console.Message(ctx, "")

	// Prompt user if they want to try again
	retryPrompt := uxlib.NewConfirm(&uxlib.ConfirmOptions{
		Message:      "Oops, my reply didn’t quite fit what was needed. Want me to try again?",
		DefaultValue: uxlib.Ptr(true),
		HelpMessage:  "Choose 'yes' to retry the current step, or 'no' to stop the initialization.",
	})

	shouldRetry, promptErr := retryPrompt.Ask(ctx)
	if promptErr != nil {
		// If we can't prompt, don't retry
		return false
	}

	return shouldRetry != nil && *shouldRetry
}

func (aai *ConversationalAzdAiAgent) renderThoughts(ctx context.Context) (func(), error) {
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
			case thought := <-aai.thoughtChan:
				if thought.Action != "" {
					// When a new action starts (different name OR different input),
					// treat the previous action as complete and print its completion.
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

			// Update spinner text
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
