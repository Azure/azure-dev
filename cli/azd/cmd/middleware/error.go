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
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/tidwall/gjson"
	"go.opentelemetry.io/otel/codes"
)

type ErrorMiddleware struct {
	options           *Options
	console           input.Console
	agentFactory      *agent.AgentFactory
	global            *internal.GlobalCommandOptions
	featuresManager   *alpha.FeatureManager
	userConfigManager config.UserConfigManager
	errorPipeline     *errorhandler.ErrorHandlerPipeline
}

func NewErrorMiddleware(
	options *Options, console input.Console,
	agentFactory *agent.AgentFactory,
	global *internal.GlobalCommandOptions,
	featuresManager *alpha.FeatureManager,
	userConfigManager config.UserConfigManager,
	errorPipeline *errorhandler.ErrorHandlerPipeline,
) Middleware {
	return &ErrorMiddleware{
		options:           options,
		console:           console,
		agentFactory:      agentFactory,
		global:            global,
		featuresManager:   featuresManager,
		userConfigManager: userConfigManager,
		errorPipeline:     errorPipeline,
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

	// Stop the spinner always to un-hide cursor
	e.console.StopSpinner(ctx, "", input.Step)

	if err == nil || IsChildAction(ctx) {
		return actionResult, err
	}

	// Check if error already has a suggestion
	var suggestionErr *internal.ErrorWithSuggestion
	if errors.As(err, &suggestionErr) {
		// Already has a suggestion, return as-is
		return actionResult, err
	}

	// Try to match error against known patterns and wrap with suggestion
	if suggestion := e.errorPipeline.Process(ctx, err); suggestion != nil {
		return actionResult, suggestion
	}

	// Short-circuit agentic error handling in non-interactive scenarios:
	// - LLM feature is disabled
	// - User specified --no-prompt (non-interactive mode)
	// - Running in CI/CD environment where user interaction is not possible
	if !e.featuresManager.IsEnabled(llm.FeatureLlm) || e.global.NoPrompt || resource.IsRunningOnCI() {
		return actionResult, err
	}

	// Skip control-flow errors that don't benefit from AI analysis
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
			"Generate Troubleshooting Steps",
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
			2. Provide actionable troubleshooting steps in natural language format with clear sections.
			DO NOT return JSON. Use readable narrative text with markdown formatting.
			Do not perform any file changes.
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
			"Brainstorm Solutions",
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
			1. Check if the error is included in azd_provision_common_error tool. 
			If not, jump to step 2.
			If so, jump to step 3 and only use the solution azd_provision_common_error provided.
            2. Use available tools to identify, explain and diagnose this error when running azd command and its root cause.
            3. Return ONLY the following JSON object as your final response. Do not add any text before or after. 
			Do not use markdown code blocks. Return raw JSON only:
            {
              "analysis": "Brief explanation of the error and its root cause",
              "solutions": [
                "Solution 1 Short description (one sentence)",
                "Solution 2 Short description (one sentence)",
                "Solution 3 Short description (one sentence)"
              ]
            }
            Provide up to 3 solutions. Each solution must be concise (one sentence).
            IMPORTANT: Your response must be ONLY the JSON object above, nothing else.
            Error details: %s`, errorInput))

		// Extract solutions from agent output even if there's a parsing error
		// The agent may return valid content
		solutions := extractSuggestedSolutions(agentOutput)

		// If no solutions found in output, try extracting from the error message
		// LangChain may fail to parse but errors include the valid JSON
		if len(solutions) == 0 && err != nil {
			solutions = extractSuggestedSolutions(err.Error())
		}

		// Only fail if we got an error AND couldn't extract any solutions
		if err != nil && len(solutions) == 0 {
			e.displayAgentResponse(ctx, agentOutput, AIDisclaimer)
			span.SetStatus(codes.Error, "agent.send_message.failed")
			return nil, fmt.Errorf("failed to generate solutions: %w", err)
		}

		e.console.Message(ctx, "")
		selectedSolution, continueWithFix, err := promptUserForSolution(ctx, solutions, agentName)
		if err != nil {
			return nil, fmt.Errorf("prompting for solution selection: %w", err)
		}

		if continueWithFix {
			agentOutput, err := azdAgent.SendMessage(ctx, fmt.Sprintf(
				`Steps to follow:
			1. Check if the error is included in azd_provision_common_error tool. 
			If so, jump to step 3 and only use the solution azd_provision_common_error provided.
			If not, continue to step 2.
            2. Use available tools to identify, explain and diagnose this error when running azd command and its root cause.
            3. Resolve the error by making the minimal, targeted change required to the code or configuration.
            Avoid unnecessary modifications and focus only on what is essential to restore correct functionality.
            4. Remove any changes that were created solely for validation and are not part of the actual error fix.
			5. You are currently in the middle of executing '%s'. Never run this command.
            Error details: %s`, e.options.CommandPath, errorInput))

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
					`Steps to follow:
						1. Perform the following actions to resolve the error: %s. 
						During this, make minimal changes and avoid unnecessary modifications.
						2. Remove any changes that were created solely for validation and
						 are not part of the actual error fix.
						3. You are currently in the middle of executing '%s'. Never run this command.
						Error details: %s`, selectedSolution, e.options.CommandPath, errorInput))

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
	consentMessage string,
	helpMessage string,
	skip bool,
) (bool, error) {
	userConfig, err := e.userConfigManager.Load()
	if err != nil {
		return false, fmt.Errorf("failed to load user config: %w", err)
	}

	if exists, ok := userConfig.GetString(promptName); ok && exists == "allow" {
		e.console.Message(ctx, output.WithWarningFormat(
			"%s option is currently set to \"allow\" meaning this action will run automatically. "+
				"To disable this, please run %s.\n",
			consentMessage,
			output.WithHighLightFormat(fmt.Sprintf("azd config unset %s", promptName)),
		))

	} else {
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

// extractSuggestedSolutions extracts solutions from the LLM response.
// It expects a JSON response with the structure: {"analysis": "...", "solutions": ["...", "...", "..."]}
// The response may be wrapped in a "text" field by the agent framework:
// {"text": "{\"analysis\": ..., \"solutions\": [...]}"}
// If JSON parsing fails, it returns an empty slice.
func extractSuggestedSolutions(llmResponse string) []string {
	// First, check if response is wrapped in a "text" field (agent framework wrapper)
	textResult := gjson.Get(llmResponse, "text")
	if textResult.Exists() && textResult.Type == gjson.String {
		// Unwrap the text field - it contains the actual JSON as a string
		llmResponse = textResult.String()
	}

	// Now extract solutions from the unwrapped response
	result := gjson.Get(llmResponse, "solutions")
	if !result.Exists() {
		return []string{}
	}

	var solutions []string
	for _, solution := range result.Array() {
		solutions = append(solutions, solution.String())
	}
	return solutions
}

// promptUserForSolution displays extracted solutions to the user and prompts them to select which solution to try.
// Returns the selected solution text, a flag indicating if user wants to continue with AI fix, and error if any.
func promptUserForSolution(ctx context.Context, solutions []string, agentName string) (string, bool, error) {
	choices := make([]*uxlib.SelectChoice, len(solutions)+2)

	if len(solutions) > 0 {
		// Add the three solutions
		for i, solution := range solutions {
			choices[i] = &uxlib.SelectChoice{
				Value: solution,
				Label: "Yes. " + solution,
			}
		}
	}

	choices[len(solutions)] = &uxlib.SelectChoice{
		Value: "continue",
		Label: fmt.Sprintf("Yes, let %s choose the best approach", agentName),
	}

	choices[len(solutions)+1] = &uxlib.SelectChoice{
		Value: "cancel",
		Label: "No, cancel",
	}

	selector := uxlib.NewSelect(&uxlib.SelectOptions{
		Message:         fmt.Sprintf("Allow %s to fix the error?", agentName),
		HelpMessage:     "Select a suggested fix, or let AI decide",
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
