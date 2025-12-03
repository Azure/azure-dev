// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent"
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

func (e *ErrorMiddleware) displayAgentResponse(ctx context.Context, response string, disclaimer string) {
	if response != "" {
		e.console.Message(ctx, disclaimer)
		e.console.Message(ctx, "")
		e.console.Message(ctx, fmt.Sprintf("%s:", output.AzdAgentLabel()))
		e.console.Message(ctx, output.WithMarkdown(response))
		e.console.Message(ctx, "")
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
				e.displayAgentResponse(ctx, agentOutput, AIDisclaimer)
				span.SetStatus(codes.Error, "agent.send_message.failed")
				return nil, err
			}

			e.displayAgentResponse(ctx, agentOutput, AIDisclaimer)
		}

		// Ask user if they want to let AI fix the error
		confirmFix, err := e.checkErrorHandlingConsent(
			ctx,
			"mcp.errorHandling.fix",
			fmt.Sprintf("Brainstorm solutions using %s?", agentName),
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
            2. Include a section called "Brainstorm Solutions" to list out at least one and up to three solutions user could try out to fix the error (use "1. ", "2. ", "3. ").
            Each solution needs to be short and clear (one sentence).
            Error details: %s`, errorInput))

		if err != nil {
			e.displayAgentResponse(ctx, agentOutput, AIDisclaimer)
			span.SetStatus(codes.Error, "agent.send_message.failed")
			return nil, err
		}

		// Extract solutions from agent output
		solutions := extractSuggestedSolutions(agentOutput)

		if len(solutions) > 0 {
			e.console.Message(ctx, "")
			selectedSolution, continueWithFix, err := promptUserForSolution(ctx, solutions, agentName)
			if err != nil {
				return nil, fmt.Errorf("prompting for solution selection: %w", err)
			}

			if continueWithFix {
				agentOutput, err := azdAgent.SendMessage(ctx, fmt.Sprintf(
					`Steps to follow:
            1. Use available tool to identify, explain and diagnose this error when running azd command and its root cause.
            2. Resolve the error by making the minimal, targeted change required to the code or configuration.
            Avoid unnecessary modifications and focus only on what is essential to restore correct functionality.
            3. Remove any changes that were created solely for validation and are not part of the actual error fix.
            Error details: %s`, errorInput))

				if err != nil {
					e.displayAgentResponse(ctx, agentOutput, AIDisclaimer)
					span.SetStatus(codes.Error, "agent.send_message.failed")
					return nil, err
				}

				span.SetStatus(codes.Ok, "agent.fix.agent")
			} else {
				if selectedSolution != "" {
					// User selected a solution
					agentOutput, err = azdAgent.SendMessage(ctx, fmt.Sprintf(
						`Perform the following actions to resolve the error: %s
						Error details: %s`, selectedSolution, errorInput))

					if err != nil {
						e.displayAgentResponse(ctx, agentOutput, AIDisclaimer)
						span.SetStatus(codes.Error, "agent.send_message.failed")
						return nil, err
					}
					span.SetStatus(codes.Ok, "agent.fix.solution")
				} else {
					// User selected cancel
					span.SetStatus(codes.Error, "agent.fix.cancelled")
					return actionResult, originalError
				}
			}
		}

		// Clear check cache to prevent skip of tool related error
		ctx = tools.WithInstalledCheckCache(ctx)

		actionResult, err = next(ctx)
		originalError = err
	}

	return actionResult, err
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
		DisplayCount:    len(choices),
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

// extractSuggestedSolutions extracts the three solutions from the "Brainstorm Solutions" section
// in the LLM response. Returns a slice of solution strings.
func extractSuggestedSolutions(llmResponse string) []string {
	var solutions []string

	// Find the "Brainstorm Solutions" section (case-insensitive, with optional markdown headers)
	lines := strings.Split(llmResponse, "\n")
	inSolutionsSection := false

	// Regex to match numbered list items: "1. ", "2. ", "3. "
	numberPattern := regexp.MustCompile(`^\s*(\d+)\.\s+(.+)$`)

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Check if we're entering the "Brainstorm Solutions" section
		if strings.Contains(strings.ToLower(trimmedLine), "brainstorm solutions") {
			inSolutionsSection = true
			continue
		}
		// If we're in the solutions section, extract numbered items
		if inSolutionsSection {
			// Stop if we hit another section header
			if strings.HasPrefix(trimmedLine, "#") && !strings.Contains(strings.ToLower(trimmedLine), "brainstorm solutions") {
				break
			}

			// Match numbered list items
			matches := numberPattern.FindStringSubmatch(trimmedLine)
			if len(matches) == 3 {
				solutionText := strings.TrimSpace(matches[2])
				if solutionText != "" {
					solutions = append(solutions, solutionText)
				}
			}

			// Stop after collecting 3 solutions
			if len(solutions) >= 3 {
				break
			}
		}
	}

	return solutions
}

// promptUserForSolution displays extracted solutions to the user and prompts them to select which solution to try.
// Returns the selected solution text, a flag indicating if user wants to continue with AI fix, and error if any.
func promptUserForSolution(ctx context.Context, solutions []string, agentName string) (string, bool, error) {
	choices := make([]*uxlib.SelectChoice, len(solutions)+2)

	// Add the three solutions
	for i, solution := range solutions {
		choices[i] = &uxlib.SelectChoice{
			Value: solution,
			Label: "Yes. " + solution,
		}
	}

	choices[len(solutions)] = &uxlib.SelectChoice{
		Value: "continue",
		Label: fmt.Sprintf("Yes, allowing %s to fix the error independently", agentName),
	}

	choices[len(solutions)+1] = &uxlib.SelectChoice{
		Value: "cancel",
		Label: "No, cancel",
	}

	selector := uxlib.NewSelect(&uxlib.SelectOptions{
		Message:         fmt.Sprintf("Allow %s to fix the error?", agentName),
		HelpMessage:     "Select a solution to proceed, continue with AI fix, or cancel",
		Choices:         choices,
		EnableFiltering: uxlib.Ptr(false),
		DisplayCount:    len(choices),
	})

	choiceIndex, err := selector.Ask(ctx)
	if err != nil {
		return "", false, err
	}

	if choiceIndex == nil || *choiceIndex < 0 || *choiceIndex >= len(choices) {
		return "", false, fmt.Errorf("invalid choice selected")
	}

	selectedValue := choices[*choiceIndex].Value

	// Handle different selections
	switch selectedValue {
	case "continue":
		return "", true, nil // Continue to AI fix
	case "cancel":
		return "", false, nil // Cancel and return error
	default:
		return selectedValue, false, nil // User selected a solution
	}
}
