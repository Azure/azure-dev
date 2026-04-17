// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"text/template"
	"time"

	surveyterm "github.com/AlecAivazis/survey/v2/terminal"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	agentcopilot "github.com/azure/azure-dev/cli/azd/internal/agent/copilot"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/pack"
	"github.com/azure/azure-dev/cli/azd/pkg/update"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"go.opentelemetry.io/otel/codes"
)

//go:embed templates/explain.tmpl
var explainTmpl string

//go:embed templates/guidance.tmpl
var guidanceTmpl string

//go:embed templates/troubleshoot_manual.tmpl
var troubleshootManualTmpl string

//go:embed templates/fix.tmpl
var fixTmpl string

// agentCallTimeout is the maximum time to wait for a single LLM agent call.
const agentCallTimeout = 5 * time.Minute

var (
	explainTemplate            = template.Must(template.New("explain").Parse(explainTmpl))
	guidanceTemplate           = template.Must(template.New("guidance").Parse(guidanceTmpl))
	troubleshootManualTemplate = template.Must(template.New("troubleshoot").Parse(troubleshootManualTmpl))
	fixTemplate                = template.Must(template.New("fix").Parse(fixTmpl))
)

type ErrorMiddleware struct {
	options           *Options
	console           input.Console
	agentFactory      agent.AgentFactory
	global            *internal.GlobalCommandOptions
	featuresManager   *alpha.FeatureManager
	userConfigManager config.UserConfigManager
	errorPipeline     *errorhandler.ErrorHandlerPipeline
}

// shouldSkipAgentHandling returns true for errors that should not be processed
// by the AI agent — either because they are normal control-flow signals or
// because they belong to error types the agent cannot fix.
func shouldSkipAgentHandling(err error) bool {
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, surveyterm.InterruptErr) ||
		errors.Is(err, azdcontext.ErrNoProject) ||
		errors.Is(err, consent.ErrToolExecutionDenied) ||
		errors.Is(err, consent.ErrElicitationDenied) ||
		errors.Is(err, consent.ErrSamplingDenied) ||
		errors.Is(err, internal.ErrAbortedByUser) ||

		errors.Is(err, environment.ErrNotFound) ||
		errors.Is(err, environment.ErrNameNotSpecified) ||
		errors.Is(err, environment.ErrDefaultEnvironmentNotFound) ||
		errors.Is(err, environment.ErrAccessDenied) ||
		errors.Is(err, pipeline.ErrAuthNotSupported) ||
		errors.Is(err, pipeline.ErrRemoteHostIsNotAzDo) ||
		errors.Is(err, pipeline.ErrSSHNotSupported) ||
		errors.Is(err, pipeline.ErrRemoteHostIsNotGitHub) ||
		errors.Is(err, project.ErrNoDefaultService) {
		return true
	}

	_, extRunErr := errors.AsType[*extensions.ExtensionRunError](err)
	_, envErr := errors.AsType[*environment.EnvironmentInitError](err)
	_, updateErr := errors.AsType[*update.UpdateError](err)
	_, packStatusErr := errors.AsType[*pack.StatusCodeError](err)
	_, missingInputsErr := errors.AsType[*bicep.MissingInputsError](err)
	_, configValidErr := errors.AsType[*project.ConfigValidationError](err)

	if extRunErr || packStatusErr || missingInputsErr || configValidErr || updateErr || envErr {
		return true
	}

	return false
}

// troubleshootCategory represents the user's chosen troubleshooting scope.
type troubleshootCategory string

const (
	// categoryExplain shows only the error explanation.
	categoryExplain troubleshootCategory = "explain"
	// categoryGuidance shows only the step-by-step fix guidance.
	categoryGuidance troubleshootCategory = "guidance"
	// categoryTroubleshoot shows both explanation and guidance.
	categoryTroubleshoot troubleshootCategory = "troubleshoot"
	// categoryFix skips explanation and jumps directly to agent-driven fix.
	categoryFix troubleshootCategory = "fix"
	// categorySkip skips troubleshooting entirely.
	categorySkip troubleshootCategory = "skip"
)

// nextAction represents what the user wants after an explanation/guidance.
type nextAction string

const (
	// actionFixAndRetry fixes the error and retries the command.
	actionFixAndRetry nextAction = "fix_and_retry"
	// actionFixOnly fixes the error but does not retry.
	actionFixOnly nextAction = "fix_only"
	// actionExit exits without fixing.
	actionExit nextAction = "exit"
)

func NewErrorMiddleware(
	options *Options, console input.Console,
	agentFactory agent.AgentFactory,
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

func (e *ErrorMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	actionResult, err := next(ctx)

	// Stop the spinner always to un-hide cursor
	e.console.StopSpinner(ctx, "", input.Step)

	if err == nil || IsChildAction(ctx) {
		return actionResult, err
	}

	// Check if error already has a suggestion
	if _, ok := errors.AsType[*internal.ErrorWithSuggestion](err); ok {
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
	// - Errors that don't benefit from agent handling
	if !e.featuresManager.IsEnabled(agentcopilot.FeatureCopilot) ||
		e.global.NoPrompt ||
		resource.IsRunningOnCI() ||
		shouldSkipAgentHandling(err) {
		return actionResult, err
	}

	// Warn user that this is an alpha feature
	e.console.WarnForFeature(ctx, agentcopilot.FeatureCopilot)

	ctx, span := tracing.Start(ctx, events.AgentTroubleshootEvent)
	defer span.End()

	originalError := err
	createCtx, createCancel := context.WithTimeout(ctx, agentCallTimeout)
	defer createCancel()
	azdAgent, err := e.agentFactory.Create(
		createCtx,
		agent.WithMode(agent.AgentModePlan),
		agent.WithDebug(e.global.EnableDebugLogging),
	)
	if err != nil {
		span.SetStatus(codes.Error, "agent.creation.failed")
		return actionResult, fmt.Errorf(
			"%w\n\nAgent error: %s", originalError, err.Error())
	}

	defer azdAgent.Stop()

	attempt := 0
	var previousError error

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
				e.console.Message(ctx, "Please review the error and fix it manually, "+
					"the agent was unable to resolve the error after multiple attempts.")
				span.SetStatus(codes.Error, "agent.fix.max_attempts_reached")
				return actionResult, originalError
			}
		}

		if errorWithTraceId, ok := errors.AsType[*internal.ErrorWithTraceId](originalError); ok {
			e.console.Message(ctx, output.WithErrorFormat("TraceID: %s", errorWithTraceId.TraceId))
		}

		// Step 1: Category selection — user chooses the troubleshooting scope
		category, err := e.promptTroubleshootCategory(ctx)
		if err != nil {
			span.SetStatus(codes.Error, "agent.category.failed")
			return actionResult, fmt.Errorf(
				"%w\n\nPrompted for troubleshoot category: %s", originalError, err.Error())
		}

		if category == categorySkip {
			span.SetStatus(codes.Error, "agent.troubleshoot.skip")
			return actionResult, originalError
		}

		// Step 2: Execute the selected category prompt
		categoryPrompt := e.buildPromptForCategory(category, originalError)
		e.console.Message(ctx, output.WithHintFormat(
			"Preparing %s to %s error...", agentcopilot.DisplayTitle, category))

		callCtx, callCancel := context.WithTimeout(ctx, agentCallTimeout)
		defer callCancel()
		agentResult, err := azdAgent.SendMessageWithRetry(callCtx, categoryPrompt)
		if err != nil {
			span.SetStatus(codes.Error, "agent.send_message.failed")
			return actionResult, fmt.Errorf(
				"%w\n\nAgent error: %s", originalError, err.Error())
		}

		span.SetStatus(codes.Ok, fmt.Sprintf("agent.%s.completed", category))
		e.displayUsageMetrics(ctx, agentResult)

		previousError = originalError

		if category != categoryFix {
			// Step 3: Ask user how to proceed after analysis
			action, err := e.promptNextAction(ctx)
			if err != nil {
				span.SetStatus(codes.Error, "agent.prompt.failed")
				return actionResult, fmt.Errorf(
					"%w\n\nPrompted for next action: %s", originalError, err.Error())
			}

			if action == actionExit {
				span.SetStatus(codes.Ok, "agent.fix.declined")
				return actionResult, originalError
			}

			// Step 4: Agent applies the fix
			fixPrompt, err := e.buildFixPrompt(originalError)
			if err != nil {
				span.SetStatus(codes.Error, "agent.fix.template_failed")
				return actionResult, fmt.Errorf(
					"%w\n\nFailed to build fix prompt: %s", originalError, err.Error())
			}
			e.console.Message(ctx, output.WithHintFormat(
				"Preparing %s to fix error...", agentcopilot.DisplayTitle))

			fixCtx, fixCancel := context.WithTimeout(ctx, agentCallTimeout)
			defer fixCancel()
			fixResult, err := azdAgent.SendMessageWithRetry(fixCtx, fixPrompt)
			if err != nil {
				span.SetStatus(codes.Error, "agent.fix.failed")
				return actionResult, fmt.Errorf(
					"%w\n\nAgent error: %s", originalError, err.Error())
			}

			span.SetStatus(codes.Ok, "agent.fix.completed")
			e.displayUsageMetrics(ctx, fixResult)

			// Fix-only: skip retry
			if action == actionFixOnly {
				return actionResult, originalError
			}
			// actionFixAndRetry: user explicitly chose to retry, so fall
			// through to re-run the command without an extra confirmation prompt.
		} else {
			// Category "fix" already applied the fix in step 2.
			// Unlike the explain/guidance path (which auto-retries when the user
			// explicitly chose actionFixAndRetry), the category-fix path asks for
			// retry confirmation because the user only chose "fix" — not "fix and retry".
			shouldRetry, err := e.promptRetryAfterFix(ctx)
			if err != nil {
				span.SetStatus(codes.Error, "agent.retry.failed")
				return actionResult, fmt.Errorf(
					"%w\n\nRetry prompt failed: %s", originalError, err.Error())
			}
			if !shouldRetry {
				return actionResult, originalError
			}
		}

		// Re-run the original command to check if the fix worked
		ctx = tools.WithInstalledCheckCache(ctx)
		actionResult, err = next(ctx)
		originalError = err

		if shouldSkipAgentHandling(err) {
			return actionResult, err
		}
	}

	return actionResult, err
}

// errorPromptData is the data passed to the troubleshooting prompt templates.
type errorPromptData struct {
	Command      string
	ErrorMessage string
}

// buildPromptForCategory renders the prompt template for the selected troubleshooting category.
func (e *ErrorMiddleware) buildPromptForCategory(category troubleshootCategory, err error) string {
	data := errorPromptData{
		Command:      e.options.CommandPath,
		ErrorMessage: err.Error(),
	}

	var tmpl *template.Template
	switch category {
	case categoryExplain:
		tmpl = explainTemplate
	case categoryGuidance:
		tmpl = guidanceTemplate
	case categoryTroubleshoot:
		tmpl = troubleshootManualTemplate
	case categoryFix:
		tmpl = fixTemplate
	default:
		tmpl = troubleshootManualTemplate
	}

	var buf bytes.Buffer
	if execErr := tmpl.Execute(&buf, data); execErr != nil {
		log.Printf("[copilot] Failed to execute %s template: %v", category, execErr)
		return fmt.Sprintf("An error occurred while running `%s`: %s\n\nPlease diagnose and explain this error.",
			data.Command, data.ErrorMessage)
	}

	return buf.String()
}

// buildFixPrompt renders the fix prompt template.
func (e *ErrorMiddleware) buildFixPrompt(err error) (string, error) {
	data := errorPromptData{
		Command:      e.options.CommandPath,
		ErrorMessage: err.Error(),
	}

	var buf bytes.Buffer
	if execErr := fixTemplate.Execute(&buf, data); execErr != nil {
		return "", fmt.Errorf(
			"executing fix template: %w", execErr)
	}

	return buf.String(), nil
}

// displayUsageMetrics shows token usage metrics after an agent interaction.
func (e *ErrorMiddleware) displayUsageMetrics(ctx context.Context, result *agent.AgentResult) {
	if result != nil && result.Usage.TotalTokens() > 0 {
		e.console.Message(ctx, "")
		e.console.Message(ctx, result.Usage.String())
	}
}

// promptTroubleshootCategory asks the user to select a troubleshooting scope.
// Checks saved category preference; if set, auto-selects and prints a message.
// Otherwise presents: Diagnose and guide, Fix (default), Skip, Explain, Guidance.
func (e *ErrorMiddleware) promptTroubleshootCategory(ctx context.Context) (troubleshootCategory, error) {
	userConfig, err := e.userConfigManager.Load()
	if err != nil {
		return categorySkip, fmt.Errorf("failed to load user config: %w", err)
	}

	// Check for saved category preference
	if val, ok := userConfig.GetString(agentcopilot.ConfigKeyErrorHandlingCategory); ok && val != "" {
		saved := troubleshootCategory(val)
		switch saved {
		case categoryExplain, categoryGuidance, categoryTroubleshoot, categoryFix, categorySkip:
			e.console.Message(ctx, output.WithWarningFormat(
				"\n%s troubleshooting is set to always use '%s'. To change, run %s.",
				agentcopilot.DisplayTitle,
				string(saved),
				output.WithHighLightFormat(
					fmt.Sprintf("azd config unset %s", agentcopilot.ConfigKeyErrorHandlingCategory)),
			))
			return saved, nil
		}
		// Invalid saved value — fall through to prompt
	}

	choices := []*uxlib.SelectChoice{
		{Value: string(categoryTroubleshoot), Label: "Diagnose and guide"},
		{Value: string(categoryFix), Label: "Fix this error"},
		{Value: string(categorySkip), Label: "Skip"},
		{Value: string(categoryExplain), Label: "Explain this error"},
		{Value: string(categoryGuidance), Label: "Show fix guidance"},
	}
	selector := uxlib.NewSelect(&uxlib.SelectOptions{
		Message: fmt.Sprintf(
			"How would you like %s to help?",
			agentcopilot.DisplayTitle),
		HelpMessage: fmt.Sprintf(
			"Choose the level of assistance. "+
				"To always use a specific choice, run %s.",
			output.WithHighLightFormat(
				fmt.Sprintf(
					"azd config set %s <category>",
					agentcopilot.ConfigKeyErrorHandlingCategory))),
		Choices:         choices,
		SelectedIndex:   new(1),
		EnableFiltering: new(false),
		DisplayCount:    len(choices),
	})

	e.console.Message(ctx, "")
	choiceIndex, err := selector.Ask(ctx)
	if err != nil {
		return categorySkip, err
	}

	if choiceIndex == nil || *choiceIndex < 0 || *choiceIndex >= len(choices) {
		return categorySkip, fmt.Errorf("invalid choice selected")
	}

	selected := troubleshootCategory(choices[*choiceIndex].Value)

	// Print hint about persisting the choice
	e.console.Message(ctx, output.WithGrayFormat(
		"Tip: To always use this choice, run: %s",
		output.WithHighLightFormat(
			fmt.Sprintf("azd config set %s %s", agentcopilot.ConfigKeyErrorHandlingCategory, string(selected))),
	))

	return selected, nil
}

// promptNextAction asks the user how to proceed after an explanation or guidance.
// Offers fix-and-retry, fix-only, or exit. Checks saved fix preference for auto-approval.
func (e *ErrorMiddleware) promptNextAction(
	ctx context.Context,
) (nextAction, error) {
	userConfig, err := e.userConfigManager.Load()
	if err != nil {
		return actionExit, fmt.Errorf(
			"failed to load user config: %w", err)
	}

	// Saved "always fix" preference → auto-approve fix and rerun the command.
	if val, ok := userConfig.GetString(
		agentcopilot.ConfigKeyErrorHandlingFix); ok && val == "allow" {
		e.console.Message(ctx, output.WithWarningFormat(
			"\n%s auto-fix and retry is enabled. To change, run %s.",
			agentcopilot.DisplayTitle,
			output.WithHighLightFormat(
				fmt.Sprintf("azd config unset %s",
					agentcopilot.ConfigKeyErrorHandlingFix)),
		))
		return actionFixAndRetry, nil
	}

	choices := []*uxlib.SelectChoice{
		{Value: string(actionFixAndRetry),
			Label: fmt.Sprintf(
				"Fix with %s and retry command",
				agentcopilot.DisplayTitle)},
		{Value: string(actionFixOnly),
			Label: fmt.Sprintf(
				"Fix with %s",
				agentcopilot.DisplayTitle)},
		{Value: string(actionExit),
			Label: "Exit"},
	}

	selector := uxlib.NewSelect(&uxlib.SelectOptions{
		Message: "How would you like to proceed?",
		HelpMessage: fmt.Sprintf(
			"To always auto-fix and retry, run %s.",
			output.WithHighLightFormat(
				fmt.Sprintf("azd config set %s allow",
					agentcopilot.ConfigKeyErrorHandlingFix))),
		Choices:         choices,
		EnableFiltering: new(false),
		DisplayCount:    len(choices),
	})

	e.console.Message(ctx, "")
	choiceIndex, err := selector.Ask(ctx)
	if err != nil {
		return actionExit, err
	}

	if choiceIndex == nil ||
		*choiceIndex < 0 ||
		*choiceIndex >= len(choices) {
		return actionExit, fmt.Errorf("invalid choice selected")
	}

	return nextAction(choices[*choiceIndex].Value), nil
}

// promptRetryAfterFix asks the user if the agent applied a fix and they want to retry the command.
func (e *ErrorMiddleware) promptRetryAfterFix(ctx context.Context) (bool, error) {
	choices := []*uxlib.SelectChoice{
		{Value: "retry", Label: "Retry the command"},
		{Value: "exit", Label: "Exit"},
	}

	selector := uxlib.NewSelect(&uxlib.SelectOptions{
		Message:         "How would you like to proceed?",
		Choices:         choices,
		EnableFiltering: new(false),
		DisplayCount:    len(choices),
	})

	e.console.Message(ctx, "")
	choiceIndex, err := selector.Ask(ctx)
	if err != nil {
		return false, err
	}

	if choiceIndex == nil || *choiceIndex < 0 || *choiceIndex >= len(choices) {
		return false, fmt.Errorf("invalid retry choice selected")
	}

	return choices[*choiceIndex].Value == "retry", nil
}
