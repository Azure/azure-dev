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

func fixableError(err error) bool {
	// --- Machine context: typed errors ---
	_, extRunErr := errors.AsType[*extensions.ExtensionRunError](err)
	_, packStatusErr := errors.AsType[*pack.StatusCodeError](err)

	if extRunErr || packStatusErr {
		return false
	}
	if errors.Is(err, environment.ErrNotFound) ||
		errors.Is(err, environment.ErrNameNotSpecified) ||
		errors.Is(err, environment.ErrDefaultEnvironmentNotFound) ||
		errors.Is(err, environment.ErrAccessDenied) ||
		errors.Is(err, pipeline.ErrAuthNotSupported) ||
		errors.Is(err, pipeline.ErrRemoteHostIsNotAzDo) ||
		errors.Is(err, pipeline.ErrSSHNotSupported) ||
		errors.Is(err, pipeline.ErrRemoteHostIsNotGitHub) ||
		errors.Is(err, project.ErrNoDefaultService) {
		return false
	}

	return true
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

// shouldSkipErrorAnalysis returns true for control-flow errors that should not
// be sent to AI analysis
func shouldSkipErrorAnalysis(err error) bool {
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, surveyterm.InterruptErr) ||
		errors.Is(err, azdcontext.ErrNoProject) ||
		errors.Is(err, consent.ErrToolExecutionDenied) ||
		errors.Is(err, consent.ErrElicitationDenied) ||
		errors.Is(err, consent.ErrSamplingDenied) ||
		errors.Is(err, internal.ErrAbortedByUser) {
		return true
	}

	// Environment was already initialized
	if _, ok := errors.AsType[*environment.EnvironmentInitError](err); ok {
		return true
	}

	// Update errors have their own user-facing messages and suggestions
	if _, ok := errors.AsType[*update.UpdateError](err); ok {
		return true
	}

	return false
}

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
	if !e.featuresManager.IsEnabled(agentcopilot.FeatureCopilot) || e.global.NoPrompt || resource.IsRunningOnCI() {
		return actionResult, err
	}

	// Skip control-flow errors that don't benefit from AI analysis
	if shouldSkipErrorAnalysis(err) {
		return actionResult, err
	}

	// Warn user that this is an alpha feature
	e.console.WarnForFeature(ctx, agentcopilot.FeatureCopilot)

	ctx, span := tracing.Start(ctx, events.AgentTroubleshootEvent)
	defer span.End()

	originalError := err
	azdAgent, err := e.agentFactory.Create(
		ctx,
		agent.WithMode(agent.AgentModePlan),
		agent.WithDebug(e.global.EnableDebugLogging),
	)
	if err != nil {
		span.SetStatus(codes.Error, "agent.creation.failed")
		return nil, err
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

		// Skip agent troubleshooting for errors that are not classified as fixable
		if !fixableError(originalError) {
			return actionResult, originalError
		}

		// Step 1: Category selection — user chooses the troubleshooting scope
		category, err := e.promptTroubleshootCategory(ctx)
		if err != nil {
			span.SetStatus(codes.Error, "agent.category.failed")
			return nil, fmt.Errorf("prompting for troubleshoot category: %w", err)
		}

		if category == categorySkip {
			span.SetStatus(codes.Error, "agent.troubleshoot.skip")
			return actionResult, originalError
		}

		// Step 2: Execute the selected category prompt
		categoryPrompt := e.buildPromptForCategory(category, originalError)
		e.console.Message(ctx, output.WithHintFormat(
			"Preparing %s to %s error...", agentcopilot.DisplayTitle, category))
		agentResult, err := azdAgent.SendMessageWithRetry(ctx, categoryPrompt)
		if err != nil {
			span.SetStatus(codes.Error, "agent.send_message.failed")
			return nil, err
		}

		span.SetStatus(codes.Ok, fmt.Sprintf("agent.%s.completed", category))
		e.displayUsageMetrics(ctx, agentResult)

		previousError = originalError

		if category != categoryFix {
			// Step 3: Ask if user wants the agent to fix the error
			// (only if they didn't already choose the fix category)
			wantFix, err := e.promptForFix(ctx)
			if err != nil {
				return nil, fmt.Errorf("prompting for fix: %w", err)
			}

			if !wantFix {
				span.SetStatus(codes.Ok, "agent.fix.declined")
				return actionResult, originalError
			}

			// Step 4: Agent applies the fix
			fixPrompt := e.buildFixPrompt(originalError)
			e.console.Message(ctx, output.WithHintFormat(
				"Preparing %s to fix error...", agentcopilot.DisplayTitle))
			fixResult, err := azdAgent.SendMessageWithRetry(ctx, fixPrompt)
			if err != nil {
				span.SetStatus(codes.Error, "agent.fix.failed")
				return nil, err
			}

			span.SetStatus(codes.Ok, "agent.fix.completed")
			e.displayUsageMetrics(ctx, fixResult)
		}

		// Step 5: Ask user if they want to retry the command
		shouldRetry, err := e.promptRetryAfterFix(ctx)
		if err != nil || !shouldRetry {
			return actionResult, originalError
		}

		// Re-run the original command to check if the fix worked
		ctx = tools.WithInstalledCheckCache(ctx)
		actionResult, err = next(ctx)
		originalError = err

		if shouldSkipErrorAnalysis(err) {
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
func (e *ErrorMiddleware) buildFixPrompt(err error) string {
	data := errorPromptData{
		Command:      e.options.CommandPath,
		ErrorMessage: err.Error(),
	}

	var buf bytes.Buffer
	if execErr := fixTemplate.Execute(&buf, data); execErr != nil {
		log.Printf("[copilot] Failed to execute fix template: %v", execErr)
		return fmt.Sprintf("An error occurred while fixing `%s`: %v\n",
			data.ErrorMessage, execErr)
	}

	return buf.String()
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
// Otherwise presents: Explain, Guidance, Troubleshoot (explain + guidance), Skip.
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
		{Value: string(categoryExplain), Label: "Explain this error"},
		{Value: string(categoryGuidance), Label: "Show fix guidance"},
		{Value: string(categoryTroubleshoot), Label: "Troubleshoot with explanation and guidance"},
		{Value: string(categoryFix), Label: "Fix this error"},
		{Value: string(categorySkip), Label: "Skip"},
	}
	selector := uxlib.NewSelect(&uxlib.SelectOptions{
		Message: fmt.Sprintf("How would you like %s to help?", agentcopilot.DisplayTitle),
		HelpMessage: fmt.Sprintf(
			"Choose the level of assistance. "+
				"To always use a specific choice, run %s.",
			output.WithHighLightFormat(
				fmt.Sprintf("azd config set %s <category>", agentcopilot.ConfigKeyErrorHandlingCategory))),
		Choices:         choices,
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

// promptForFix asks the user if they want the agent to attempt to fix the error.
// Checks saved preferences for auto-approval.
func (e *ErrorMiddleware) promptForFix(ctx context.Context) (bool, error) {
	userConfig, err := e.userConfigManager.Load()
	if err != nil {
		return false, fmt.Errorf("failed to load user config: %w", err)
	}

	// Check for saved "always fix" preference
	if val, ok := userConfig.GetString(agentcopilot.ConfigKeyErrorHandlingFix); ok && val == "allow" {
		e.console.Message(ctx, output.WithWarningFormat(
			"\n%s auto-fix is enabled. To change, run %s.",
			agentcopilot.DisplayTitle,
			output.WithHighLightFormat(
				fmt.Sprintf("azd config unset %s", agentcopilot.ConfigKeyErrorHandlingFix)),
		))
		return true, nil
	}

	choices := []*uxlib.SelectChoice{
		{Value: "yes", Label: fmt.Sprintf("Yes, let %s fix it", agentcopilot.DisplayTitle)},
		{Value: "no", Label: "No, I'll fix it myself"},
	}

	selector := uxlib.NewSelect(&uxlib.SelectOptions{
		Message: fmt.Sprintf("Would you like %s to fix this error?", agentcopilot.DisplayTitle),
		HelpMessage: fmt.Sprintf(
			"The agent will fix the error. "+
				"To always allow fixes, run %s.",
			output.WithHighLightFormat(
				fmt.Sprintf("azd config set %s allow", agentcopilot.ConfigKeyErrorHandlingFix))),
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
		return false, fmt.Errorf("invalid fix choice selected")
	}

	return choices[*choiceIndex].Value == "yes", nil
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
