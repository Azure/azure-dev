// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
)

type ErrorMiddleware struct {
	options         *Options
	console         input.Console
	agentFactory    *agent.AgentFactory
	global          *internal.GlobalCommandOptions
	featuresManager *alpha.FeatureManager
}

func NewErrorMiddleware(
	options *Options, console input.Console,
	agentFactory *agent.AgentFactory,
	global *internal.GlobalCommandOptions,
	featuresManager *alpha.FeatureManager,
) Middleware {
	return &ErrorMiddleware{
		options:         options,
		console:         console,
		agentFactory:    agentFactory,
		global:          global,
		featuresManager: featuresManager,
	}
}

func (e *ErrorMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	var actionResult *actions.ActionResult
	var err error

	if e.featuresManager.IsEnabled(llm.FeatureLlm) {
		if e.options.IsChildAction(ctx) {
			return next(ctx)
		}

		actionResult, err = next(ctx)

		attempt := 0
		originalError := err
		suggestion := ""
		var previousError error
		var suggestionErr *internal.ErrorWithSuggestion
		var errorWithTraceId *internal.ErrorWithTraceId
		skipAnalyzingErrors := []string{
			"environment already initialized",
			"interrupt",
			"no project exists",
		}
		agentName := "AI"

		for {
			if originalError == nil {
				break
			}

			for _, s := range skipAnalyzingErrors {
				if strings.Contains(originalError.Error(), s) {
					return actionResult, originalError
				}
			}

			if previousError != nil && errors.Is(originalError, previousError) {
				attempt++
				if attempt > 3 {
					e.console.Message(ctx, fmt.Sprintf("%s was unable to resolve the error after multiple attempts. "+
						"Please review the error and fix it manually.", agentName))
					return actionResult, originalError
				}
			}

			e.console.Message(ctx, output.WithErrorFormat("\nERROR: %s", originalError.Error()))

			if errors.As(originalError, &errorWithTraceId) {
				e.console.Message(ctx, output.WithErrorFormat("TraceID: %s", errorWithTraceId.TraceId))
			}

			if errors.As(originalError, &suggestionErr) {
				suggestion = suggestionErr.Suggestion
				e.console.Message(ctx, suggestion)
				return actionResult, originalError
			}

			// Warn user that this is an alpha feature
			e.console.WarnForFeature(ctx, llm.FeatureLlm)

			azdAgent, err := e.agentFactory.Create(
				agent.WithDebug(e.global.EnableDebugLogging),
			)
			if err != nil {
				return nil, err
			}

			defer azdAgent.Stop()

			errorInput := originalError.Error()
			if suggestion != "" {
				errorInput += "\n" + "Suggestion: " + suggestion
			}

			agentOutput, err := azdAgent.SendMessage(ctx, fmt.Sprintf(
				`Steps to follow:
			1. Use available tool to identify, explain and diagnose this error when running azd command and its root cause.
			2. Provide actionable troubleshooting steps. Do not perform any file changes.
			Error details: %s`, errorInput))

			if err != nil {
				if agentOutput != "" {
					e.console.Message(ctx, output.WithMarkdown(agentOutput))
				}

				return nil, err
			}

			// Ask if user wants to provide AI generated troubleshooting steps
			confirm, err := e.console.Confirm(ctx, input.ConsoleOptions{
				Message:      fmt.Sprintf("Provide %s generated troubleshooting steps?", agentName),
				DefaultValue: true,
			})
			if err != nil {
				return nil, fmt.Errorf("prompting to provide troubleshooting steps: %w", err)
			}

			if confirm {
				// Provide manual steps for troubleshooting
				e.console.Message(ctx, "")
				e.console.Message(ctx, fmt.Sprintf("%s:", output.AzdAgentLabel()))
				e.console.Message(ctx, output.WithMarkdown(agentOutput))
				e.console.Message(ctx, "")
			}

			// TODO: update to "GitHub Copilot for Azure"
			// Ask user if they want to let AI fix the error
			selection, err := e.console.Select(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("Do you want to continue to fix the error using %s?", agentName),
				Options: []string{
					"Yes",
					"No",
				},
			})

			if err != nil {
				return nil, fmt.Errorf("prompting failed to confirm selection: %w", err)
			}

			switch selection {
			// fix the error with AI
			case 0:
				previousError = originalError
				changedFiles := make(map[string]bool)
				var mu sync.Mutex

				watcher, done, err := startWatcher(ctx, changedFiles, &mu)
				if err != nil {
					return nil, fmt.Errorf("failed to start watcher during error fix: %w", err)
				}

				agentOutput, err = azdAgent.SendMessage(ctx, fmt.Sprintf(
					`Steps to follow:
			1. Use available tool to identify, explain and diagnose this error when running azd command and its root cause.
			2. Resolve the error by making the minimal, targeted change required to the code or configuration.
			Avoid unnecessary modifications and focus only on what is essential to restore correct functionality.
			3. Remove any changes that were created solely for validation and are not part of the actual error fix.
			Error details: %s`, errorInput))

				if err != nil {
					if agentOutput != "" {
						e.console.Message(ctx, output.WithMarkdown(agentOutput))
					}

					return nil, err
				}

				// Print out changed files
				close(done)
				printChangedFiles(changedFiles, &mu)
				watcher.Close()

			// Not fix the error with AI
			case 1:
				return actionResult, err
			}

			// Ask the user to add feedback
			if err := e.collectAndApplyFeedback(ctx, azdAgent, "Any feedback or changes?"); err != nil {
				return nil, err
			}

			// Clear check cache to prevent skip of tool related error
			ctx = tools.WithInstalledCheckCache(ctx)

			actionResult, err = next(ctx)
			originalError = err
		}
	}

	if actionResult == nil {
		actionResult, err = next(ctx)
	}

	return actionResult, err
}

// collectAndApplyFeedback prompts for user feedback and applies it using the agent
func (e *ErrorMiddleware) collectAndApplyFeedback(
	ctx context.Context,
	azdAgent agent.Agent,
	promptMessage string,
) error {
	confirmFeedback := uxlib.NewConfirm(&uxlib.ConfirmOptions{
		Message:      promptMessage,
		DefaultValue: uxlib.Ptr(false),
		HelpMessage:  "You will be able to provide and feedback or changes after AI fix.",
	})

	hasFeedback, err := confirmFeedback.Ask(ctx)
	if err != nil {
		return err
	}

	if !*hasFeedback {
		e.console.Message(ctx, "")
		return nil
	}

	userInputPrompt := uxlib.NewPrompt(&uxlib.PromptOptions{
		Message:        "You",
		PlaceHolder:    "Provide feedback or changes to the project",
		Required:       true,
		IgnoreHintKeys: true,
	})

	userInput, err := userInputPrompt.Ask(ctx)
	if err != nil {
		return fmt.Errorf("failed to collect feedback after AI fix: %w", err)
	}

	e.console.Message(ctx, "")

	if userInput != "" {
		e.console.Message(ctx, color.MagentaString("Feedback"))

		changedFiles := make(map[string]bool)
		var mu sync.Mutex

		watcher, done, err := startWatcher(ctx, changedFiles, &mu)
		if err != nil {
			return fmt.Errorf("failed to start watcher during error fix: %w", err)
		}

		feedbackOutput, err := azdAgent.SendMessage(ctx, userInput)
		if err != nil {
			if feedbackOutput != "" {
				e.console.Message(ctx, output.WithMarkdown(feedbackOutput))
			}
			return err
		}

		// Print out changed files
		close(done)
		printChangedFiles(changedFiles, &mu)
		watcher.Close()

		e.console.Message(ctx, "")
		e.console.Message(ctx, fmt.Sprintf("%s:", output.AzdAgentLabel()))
		e.console.Message(ctx, output.WithMarkdown(feedbackOutput))
		e.console.Message(ctx, "")
	}

	return nil
}

func printChangedFiles(changedFiles map[string]bool, mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Println("\nFiles changed:")
	for file := range changedFiles {
		fmt.Println("-", file)
	}
}

func startWatcher(ctx context.Context, changedFiles map[string]bool, mu *sync.Mutex) (*fsnotify.Watcher, chan bool, error) {
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
				changedFiles[event.Name] = true
				mu.Unlock()
			case err := <-watcher.Errors:
				fmt.Errorf("watcher error: %w", err)
			case <-done:
				return
			}
		}
	}()

	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get current working directory: %w", err)
	}

	if err := watchRecursive(cwd, watcher); err != nil {
		return nil, nil, fmt.Errorf("failed to watch for changes: %w", err)
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
