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
}

// NewConversationalAzdAiAgent creates a new conversational agent with memory, tool loading,
// and MCP sampling capabilities. It filters out excluded tools and configures the agent
// for interactive conversations with a high iteration limit for complex tasks.
func NewConversationalAzdAiAgent(llm llms.Model, opts ...AgentCreateOption) (Agent, error) {
	azdAgent := &ConversationalAzdAiAgent{
		agentBase: &agentBase{
			defaultModel: llm,
			tools:        []common.AnnotatedTool{},
		},
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
	defer cancelCtx()

	var watcher watch.Watcher

	if aai.fileWatchingEnabled {
		var err error
		watcher, err = watch.NewWatcher(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to start watcher: %w", err)
		}
	}

	cleanup, err := aai.renderThoughts(thoughtsCtx)
	if err != nil {
		return "", err
	}

	defer func() {
		cleanup()

		if aai.fileWatchingEnabled {
			watcher.PrintChangedFiles(ctx)
		}
	}()

	output, err := chains.Run(ctx, aai.executor, strings.Join(args, "\n"))
	if err != nil {
		return "", err
	}

	return output, nil
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
					latestAction = thought.Action
					latestActionInput = thought.ActionInput
					toolStartTime = time.Now()
				}
				if thought.Thought != "" {
					latestThought = thought.Thought
				}
			case <-ctx.Done():
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
				spinnerText += color.HiBlackString(fmt.Sprintf("\n(%ds, esc exit agentic mode)", elapsedSeconds))

				// print out the result and use spinner to indicate processing
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
