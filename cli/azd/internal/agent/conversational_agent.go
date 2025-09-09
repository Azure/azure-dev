// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
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

type FileChanges struct {
	Created  map[string]bool
	Modified map[string]bool
	Deleted  map[string]bool
}

// SendMessage processes a single message through the agent and returns the response
func (aai *ConversationalAzdAiAgent) SendMessage(ctx context.Context, args ...string) (string, error) {
	thoughtsCtx, cancelCtx := context.WithCancel(ctx)
	fileChanges := &FileChanges{
		Created:  make(map[string]bool),
		Modified: make(map[string]bool),
		Deleted:  make(map[string]bool),
	}
	var mu sync.Mutex

	watcher, done, err := startWatcher(ctx, fileChanges, &mu)
	if err != nil {
		return "", fmt.Errorf("failed to start watcher: %w", err)
	}

	cleanup, err := aai.renderThoughts(thoughtsCtx)
	if err != nil {
		cancelCtx()
		return "", err
	}

	defer func() {
		cleanup()
		close(done)
		watcher.Close()
		cancelCtx()
	}()

	output, err := chains.Run(ctx, aai.executor, strings.Join(args, "\n"))
	if err != nil {
		return "", err
	}

	printChangedFiles(fileChanges, &mu)

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

		for {

			select {
			case thought := <-aai.thoughtChan:
				if thought.Action != "" {
					latestAction = thought.Action
					latestActionInput = thought.ActionInput
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
				spinnerText = fmt.Sprintf("Running %s tool", color.BlueString(latestAction))
				if latestActionInput != "" {
					spinnerText += " with " + color.BlueString(latestActionInput)
				}

				spinnerText += "..."
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

func startWatcher(ctx context.Context, fileChanges *FileChanges, mu *sync.Mutex) (*fsnotify.Watcher, chan bool, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	done := make(chan bool)

	go func() {
		for {
			select {
			case event := <-watcher.Events:
				mu.Lock()
				name := event.Name
				switch {
				case event.Has(fsnotify.Create):
					fileChanges.Created[name] = true
				case event.Has(fsnotify.Write) || event.Has(fsnotify.Rename):
					if !fileChanges.Created[name] && !fileChanges.Deleted[name] {
						fileChanges.Modified[name] = true
					}
				case event.Has(fsnotify.Remove):
					if fileChanges.Created[name] {
						delete(fileChanges.Created, name)
					} else {
						fileChanges.Deleted[name] = true
						delete(fileChanges.Modified, name)
					}
				}
				mu.Unlock()
			case err := <-watcher.Errors:
				log.Printf("watcher error: %v", err)
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get current working directory: %w", err)
	}

	if err := watchRecursive(cwd, watcher); err != nil {
		return nil, nil, fmt.Errorf("watcher failed: %w", err)
	}

	return watcher, done, nil
}

func watchRecursive(root string, watcher *fsnotify.Watcher) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			err = watcher.Add(path)
			if err != nil {
				return fmt.Errorf("failed to watch directory %s: %w", path, err)
			}
		}

		return nil
	})
}

func printChangedFiles(fileChanges *FileChanges, mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Println(output.WithHintFormat("| Files changed:"))

	if len(fileChanges.Created) > 0 {
		for file := range fileChanges.Created {
			fmt.Println(output.WithHintFormat("| "), color.GreenString("+ Created "), file)
		}
	}

	if len(fileChanges.Modified) > 0 {
		for file := range fileChanges.Modified {
			fmt.Println(output.WithHintFormat("| "), color.YellowString("+/- Modified "), file)
		}
	}

	if len(fileChanges.Deleted) > 0 {
		for file := range fileChanges.Deleted {
			fmt.Println(output.WithHintFormat("| "), color.RedString("- Deleted "), file)
		}
	}
}
