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
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
)

type ErrorMiddleware struct {
	options           *Options
	console           input.Console
	agentFactory      *agent.AgentFactory
	global            *internal.GlobalCommandOptions
	featuresManager   *alpha.FeatureManager
	userConfigManager config.UserConfigManager
}

func NewErrorMiddleware(
	options *Options, console input.Console,
	agentFactory *agent.AgentFactory,
	global *internal.GlobalCommandOptions,
	featuresManager *alpha.FeatureManager,
	userConfigManager config.UserConfigManager,
) Middleware {
	return &ErrorMiddleware{
		options:           options,
		console:           console,
		agentFactory:      agentFactory,
		global:            global,
		featuresManager:   featuresManager,
		userConfigManager: userConfigManager,
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
		AIDisclaimer := output.WithHintFormat("The following content is AI-generated. AI responses may be incorrect.")
		agentName := "agent mode"

		azdAgent, err := e.agentFactory.Create(
			agent.WithDebug(e.global.EnableDebugLogging),
		)
		if err != nil {
			return nil, err
		}

		defer azdAgent.Stop()

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
					e.console.Message(ctx, fmt.Sprintf("Please review the error and fix it manually, "+
						"%s was unable to resolve the error after multiple attempts.", agentName))
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

			errorInput := originalError.Error()

			confirm, err := e.checkErrorHandlingConsent(
				ctx,
				"mcp.errorHandling.troubleshooting",
				fmt.Sprintf("Generate troubleshooting steps using %s?", agentName),
				true,
			)
			if err != nil {
				return nil, fmt.Errorf("prompting to provide troubleshooting steps: %w", err)
			}

			if confirm {
				// Provide manual steps for troubleshooting
				agentOutput, err := azdAgent.SendMessage(ctx, fmt.Sprintf(
					`Steps to follow:
			1. Use available tool to identify, explain and diagnose this error when running azd command and its root cause.
			2. Provide actionable troubleshooting steps. Do not perform any file changes.
			Error details: %s`, errorInput))

				if err != nil {
					if agentOutput != "" {
						e.console.Message(ctx, AIDisclaimer)
						e.console.Message(ctx, output.WithMarkdown(agentOutput))
					}

					return nil, err
				}

				e.console.Message(ctx, AIDisclaimer)
				e.console.Message(ctx, "")
				e.console.Message(ctx, fmt.Sprintf("%s:", output.AzdAgentLabel()))
				e.console.Message(ctx, output.WithMarkdown(agentOutput))
				e.console.Message(ctx, "")
			}

			// Ask user if they want to let AI fix the
			e.console.Confirm(ctx, input.ConsoleOptions{
				Message:      "Debugger Ready?",
				DefaultValue: true,
			})
			confirm, err = e.checkErrorHandlingConsent(
				ctx,
				"mcp.errorHandling.fix",
				fmt.Sprintf("Fix this error using %s?", agentName),
				false,
			)
			if err != nil {
				return nil, fmt.Errorf("prompting to fix error using %s: %w", agentName, err)
			}

			if !confirm {
				return actionResult, err
			}

			previousError = originalError
			agentOutput, err := azdAgent.SendMessage(ctx, fmt.Sprintf(
				`Steps to follow:
			1. Use available tool to identify, explain and diagnose this error when running azd command and its root cause.
			2. Resolve the error by making the minimal, targeted change required to the code or configuration.
			Avoid unnecessary modifications and focus only on what is essential to restore correct functionality.
			3. Remove any changes that were created solely for validation and are not part of the actual error fix.
			Error details: %s`, errorInput))

			if err != nil {
				if agentOutput != "" {
					e.console.Message(ctx, AIDisclaimer)
					e.console.Message(ctx, output.WithMarkdown(agentOutput))
				}

				return nil, err
			}

			// Ask the user to add feedback
			if err := e.collectAndApplyFeedback(ctx, azdAgent, AIDisclaimer); err != nil {
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
	AIDisclaimer string,
) error {
	userInputPrompt := uxlib.NewPrompt(&uxlib.PromptOptions{
		Message:  "Any changes you'd like to make?",
		Hint:     "Describe your changes or press enter to skip.",
		Required: false,
	})

	userInput, err := userInputPrompt.Ask(ctx)
	if err != nil {
		return fmt.Errorf("failed to collect feedback for user input: %w", err)
	}

	if userInput == "" {
		e.console.Message(ctx, "")
		return nil
	}

	e.console.Message(ctx, "")
	e.console.Message(ctx, color.MagentaString("Feedback"))

	feedbackOutput, err := azdAgent.SendMessage(ctx, userInput)
	if err != nil {
		if feedbackOutput != "" {
			e.console.Message(ctx, AIDisclaimer)
			e.console.Message(ctx, output.WithMarkdown(feedbackOutput))
		}
		return err
	}

	e.console.Message(ctx, AIDisclaimer)
	e.console.Message(ctx, "")
	e.console.Message(ctx, fmt.Sprintf("%s:", output.AzdAgentLabel()))
	e.console.Message(ctx, output.WithMarkdown(feedbackOutput))
	e.console.Message(ctx, "")

	return nil
}

func (e *ErrorMiddleware) checkErrorHandlingConsent(
	ctx context.Context,
	promptName string,
	message string,
	skip bool,
) (bool, error) {
	userConfig, err := e.userConfigManager.Load()
	if err != nil {
		return false, fmt.Errorf("failed to load user config: %w", err)
	}

	if exists, ok := userConfig.GetString(promptName); !ok && exists == "" {
		choice, err := promptForErrorHandlingConsent(ctx, message, skip)
		if err != nil {
			return false, fmt.Errorf("prompting for error handling consent: %w", err)
		}

		if choice == "skip" || choice == "deny" {
			return false, nil
		}

		if choice == "always" {
			if err := userConfig.Set(promptName, "allow"); err != nil {
				return false, fmt.Errorf("failed to set consent config: %w", err)
			}

			if err := e.userConfigManager.Save(userConfig); err != nil {
				return false, err
			}
		}
	}

	return true, nil
}

func promptForErrorHandlingConsent(
	ctx context.Context,
	message string,
	skip bool,
) (string, error) {
	choices := []*uxlib.SelectChoice{
		{
			Value: "once",
			Label: "Yes, allow once",
		},
		{
			Value: "always",
			Label: "Yes, allow always",
		},
	}

	if skip {
		choices = append(choices, &uxlib.SelectChoice{
			Value: "skip",
			Label: "No, skip to next step",
		})
	} else {
		choices = append(choices, &uxlib.SelectChoice{
			Value: "deny",
			Label: "No, cancel this interaction (esc)",
		})
	}

	selector := uxlib.NewSelect(&uxlib.SelectOptions{
		Message: message,
		HelpMessage: fmt.Sprintf("This action will run AI tools to generate troubleshooting steps."+
			" Edit permissions for AI tools anytime by running %s.",
			output.WithHighLightFormat("azd mcp")),
		Choices:         choices,
		EnableFiltering: uxlib.Ptr(false),
		DisplayCount:    5,
	})

	choiceIndex, err := selector.Ask(ctx)
	if err != nil {
		return "", err
	}

	if choiceIndex == nil || *choiceIndex < 0 || *choiceIndex >= len(choices) {
		return "", fmt.Errorf("invalid choice selected")
	}

	return choices[*choiceIndex].Value, nil
}
