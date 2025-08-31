// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	actionResult, err := next(ctx)

	if e.featuresManager.IsEnabled(llm.FeatureLlm) {
		if e.options.IsChildAction(ctx) {
			return next(ctx)
		}

		attempt := 0
		originalError := err
		suggestion := ""
		var previousError error
		var suggestionErr *internal.ErrorWithSuggestion
		var errorWithTraceId *internal.ErrorWithTraceId

		// TODO: think about Error exclusive or inclusive
		skipAnalyzingErrors := []string{
			"environment already initialized",
			"interrupt",
		}

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
					e.console.Message(ctx, "AI was unable to resolve the error after multiple attempts. "+
						"Please review the error and fix it manually.")
					return actionResult, originalError
				}
			}

			// For debug, will be cleaned
			// e.console.Confirm(ctx, input.ConsoleOptions{
			// 	Message:      "Debugger Ready?",
			// 	DefaultValue: true,
			// })
			e.console.StopSpinner(ctx, "", input.Step)
			e.console.Message(ctx, output.WithErrorFormat("\nERROR: %s", originalError.Error()))

			if errors.As(originalError, &errorWithTraceId) {
				e.console.Message(ctx, output.WithErrorFormat("TraceID: %s", errorWithTraceId.TraceId))
			}

			if errors.As(originalError, &suggestionErr) {
				suggestion = suggestionErr.Suggestion
				e.console.Message(ctx, suggestion)
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
			2. Provide actionable troubleshooting steps.
			Error details: %s`, errorInput))

			if err != nil {
				if agentOutput != "" {
					e.console.Message(ctx, output.WithMarkdown(agentOutput))
				}

				return nil, err
			}

			// Ask if user wants to provide AI generated troubleshooting steps
			confirm, err := e.console.Confirm(ctx, input.ConsoleOptions{
				Message:      "Provide AI generated troubleshooting steps?",
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

			// Ask user if they want to let AI fix the error
			selection, err := e.console.Select(ctx, input.ConsoleOptions{
				Message: "Do you want to continue to fix the error using AI?",
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
				agentOutput, err = azdAgent.SendMessage(ctx, fmt.Sprintf(
					`Steps to follow:
			1. Use available tool to identify, explain and diagnose this error when running azd command and its root cause.
			2. Resolve the error by making the minimal, targeted change required to the code or configuration. 
			Avoid unnecessary modifications and focus only on what is essential to restore correct functionality.
			Error details: %s`, errorInput))

				if err != nil {
					if agentOutput != "" {
						e.console.Message(ctx, output.WithMarkdown(agentOutput))
					}

					return nil, err
				}

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

		feedbackOutput, err := azdAgent.SendMessage(ctx, userInput)
		if err != nil {
			if feedbackOutput != "" {
				e.console.Message(ctx, output.WithMarkdown(feedbackOutput))
			}
			return err
		}

		e.console.Message(ctx, "")
		e.console.Message(ctx, fmt.Sprintf("%s:", output.AzdAgentLabel()))
		e.console.Message(ctx, output.WithMarkdown(feedbackOutput))
		e.console.Message(ctx, "")
	}

	return nil
}
