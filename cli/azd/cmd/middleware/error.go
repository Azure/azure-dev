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
	"github.com/azure/azure-dev/cli/azd/internal/agent/feedback"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"go.opentelemetry.io/otel/codes"
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
	actionResult, err := next(ctx)

	// Short-circuit agentic error handling in non-interactive scenarios:
	// - LLM feature is disabled
	// - User specified --no-prompt (non-interactive mode)
	// - Running in CI/CD environment where user interaction is not possible
	if !e.featuresManager.IsEnabled(llm.FeatureLlm) || e.global.NoPrompt || resource.IsRunningOnCI() {
		return actionResult, err
	}

	// Stop the spinner always to un-hide cursor
	e.console.StopSpinner(ctx, "", input.Step)
	if err == nil || e.options.IsChildAction(ctx) {
		return actionResult, err
	}

	// Error already has a suggestion, no need for AI
	var suggestionErr *internal.ErrorWithSuggestion
	if errors.As(err, &suggestionErr) {
		e.console.Message(ctx, suggestionErr.Suggestion)
		return actionResult, err
	}

	// Skip certain errors, no need for AI
	skipAnalyzingErrors := []string{
		"environment already initialized",
		"interrupt",
		"no project exists",
		"tool execution denied",
	}
	for _, s := range skipAnalyzingErrors {
		if strings.Contains(err.Error(), s) {
			return actionResult, err
		}
	}

	// Warn user that this is an alpha feature
	e.console.WarnForFeature(ctx, llm.FeatureLlm)
	ctx, span := tracing.Start(ctx, events.AgentTroubleshootEvent)
	defer span.End()

	originalError := err
	azdAgent, err := e.agentFactory.Create(
		ctx,
		agent.WithDebug(e.global.EnableDebugLogging),
	)
	if err != nil {
		span.SetStatus(codes.Error, "agent.creation.failed")
		return nil, err
	}

	defer azdAgent.Stop()

	attempt := 0
	var previousError error
	var errorWithTraceId *internal.ErrorWithTraceId
	AIDisclaimer := output.WithGrayFormat("The following content is AI-generated. AI responses may be incorrect.")
	agentName := "agent mode"

	for {
		if originalError == nil {
			span.SetStatus(codes.Ok, "agent.fix.succeeded")
			break
		}

		e.console.Message(ctx, output.WithErrorFormat("\nERROR: %s", originalError.Error()))

		if previousError != nil && originalError.Error() == previousError.Error() {
			attempt++
			span.SetAttributes(fields.AgentFixAttempts.Int(attempt))

			if attempt >= 3 {
				e.console.Message(ctx, fmt.Sprintf("Please review the error and fix it manually, "+
					"%s was unable to resolve the error after multiple attempts.", agentName))
				span.SetStatus(codes.Error, "agent.fix.max_attempts_reached")
				return actionResult, originalError
			}
		}

		if errors.As(originalError, &errorWithTraceId) {
			e.console.Message(ctx, output.WithErrorFormat("TraceID: %s", errorWithTraceId.TraceId))
		}

		errorInput := originalError.Error()

		e.console.Message(ctx, "")
		confirmTroubleshoot, err := e.checkErrorHandlingConsent(
			ctx,
			"mcp.errorHandling.troubleshooting",
			fmt.Sprintf("Generate troubleshooting steps using %s?", agentName),
			fmt.Sprintf("This action will run AI tools to generate troubleshooting steps."+
				" Edit permissions for AI tools anytime by running %s.",
				output.WithHighLightFormat("azd mcp consent")),
			true,
		)
		if err != nil {
			span.SetStatus(codes.Error, "agent.consent.failed")
			return nil, fmt.Errorf("prompting to provide troubleshooting steps: %w", err)
		}

		if confirmTroubleshoot {
			// Provide manual steps for troubleshooting
			agentOutput, err := azdAgent.SendMessage(ctx, fmt.Sprintf(
				`Steps to follow:
			1. Use available tool including azd_error_troubleshooting tool to identify and explain the error.
			Diagnose its root cause when running azd command.
			2. Provide actionable troubleshooting steps. Do not perform any file changes.
			Error details: %s`, errorInput))

			if err != nil {
				if agentOutput != "" {
					e.console.Message(ctx, AIDisclaimer)
					e.console.Message(ctx, output.WithMarkdown(agentOutput))
				}

				span.SetStatus(codes.Error, "agent.send_message.failed")
				return nil, err
			}

			e.console.Message(ctx, AIDisclaimer)
			e.console.Message(ctx, "")
			e.console.Message(ctx, fmt.Sprintf("%s:", output.AzdAgentLabel()))
			e.console.Message(ctx, output.WithMarkdown(agentOutput))
			e.console.Message(ctx, "")
		}

		// Ask user if they want to let AI fix the
		confirmFix, err := e.checkErrorHandlingConsent(
			ctx,
			"mcp.errorHandling.fix",
			fmt.Sprintf("Fix this error using %s?", agentName),
			fmt.Sprintf("This action will run AI tools to help fix the error."+
				" Edit permissions for AI tools anytime by running %s.",
				output.WithHighLightFormat("azd mcp consent")),
			false,
		)
		if err != nil {
			span.SetStatus(codes.Error, "agent.consent.failed")
			return nil, fmt.Errorf("prompting to fix error using %s: %w", agentName, err)
		}

		if !confirmFix {
			if confirmTroubleshoot {
				span.SetStatus(codes.Ok, "agent.troubleshoot.only")
			} else {
				span.SetStatus(codes.Error, "agent.fix.declined")
			}
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

			span.SetStatus(codes.Error, "agent.send_message.failed")
			return nil, err
		}

		// Ask the user to add feedback
		if err := e.collectAndApplyFeedback(ctx, azdAgent, AIDisclaimer); err != nil {
			span.SetStatus(codes.Error, "agent.collect_feedback.failed")
			return nil, err
		}

		// Clear check cache to prevent skip of tool related error
		ctx = tools.WithInstalledCheckCache(ctx)

		actionResult, err = next(ctx)
		originalError = err
	}

	return actionResult, err
}

// collectAndApplyFeedback prompts for user feedback and applies it using the agent
func (e *ErrorMiddleware) collectAndApplyFeedback(
	ctx context.Context,
	azdAgent agent.Agent,
	AIDisclaimer string,
) error {
	collector := feedback.NewFeedbackCollector(e.console, feedback.FeedbackCollectorOptions{
		EnableLoop:      false,
		FeedbackPrompt:  "Any changes you'd like to make?",
		FeedbackHint:    "Describe your changes or press enter to skip.",
		RequireFeedback: false,
		AIDisclaimer:    AIDisclaimer,
	})

	return collector.CollectFeedbackAndApply(ctx, azdAgent, AIDisclaimer)
}

func (e *ErrorMiddleware) checkErrorHandlingConsent(
	ctx context.Context,
	promptName string,
	message string,
	helpMessage string,
	skip bool,
) (bool, error) {
	userConfig, err := e.userConfigManager.Load()
	if err != nil {
		return false, fmt.Errorf("failed to load user config: %w", err)
	}

	if exists, ok := userConfig.GetString(promptName); !ok && exists == "" {
		choice, err := promptForErrorHandlingConsent(ctx, message, helpMessage, skip)
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
	helpMessage string,
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
		Message:         message,
		HelpMessage:     helpMessage,
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
