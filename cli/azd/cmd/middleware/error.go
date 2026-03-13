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
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
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
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/pack"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"go.opentelemetry.io/otel/codes"
)

//go:embed templates/troubleshoot_fixable.tmpl
var troubleshootFixableTmpl string

//go:embed templates/troubleshoot_manual.tmpl
var troubleshootManualTmpl string

var (
	troubleshootFixableTemplate = template.Must(template.New("fixable").Parse(troubleshootFixableTmpl))
	troubleshootManualTemplate  = template.Must(template.New("manual").Parse(troubleshootManualTmpl))
)

type ErrorMiddleware struct {
	options           *Options
	console           input.Console
	agentFactory      *agent.CopilotAgentFactory
	global            *internal.GlobalCommandOptions
	featuresManager   *alpha.FeatureManager
	userConfigManager config.UserConfigManager
	errorPipeline     *errorhandler.ErrorHandlerPipeline
}

// ErrorCategory represents the classification of an error for determining
// the appropriate agent mode behavior.
type ErrorCategory int

const (
	// AzureContextAndOtherError represents errors originating from Azure service interactions
	// or any other unclassified errors. These are eligible for full agentic analysis and automated fix.
	AzureContextAndOtherError ErrorCategory = iota

	// MachineContextError represents errors caused by the local machine environment,
	// such as missing tools, incompatible tool versions, extension failures, or build-tool issues.
	MachineContextError

	// UserContextError represents errors caused by user actions or configuration,
	// such as authentication failures, missing credentials, or invalid project/environment settings.
	UserContextError
)

// classifyError categorizes an error into one of three buckets:
// AzureContextAndOtherError, MachineContextError, or UserContextError
func classifyError(err error) ErrorCategory {
	// --- Machine context: typed errors ---
	_, toolCheckErr := errors.AsType[*tools.MissingToolErrors](err)
	_, semverErr := errors.AsType[*tools.ErrSemver](err)
	_, extRunErr := errors.AsType[*extensions.ExtensionRunError](err)
	_, packStatusErr := errors.AsType[*pack.StatusCodeError](err)

	if toolCheckErr || semverErr || extRunErr || packStatusErr {
		return MachineContextError
	}

	if errors.Is(err, maven.ErrPropertyNotFound) {
		return MachineContextError
	}

	// --- User context: typed errors ---
	_, loginErr := errors.AsType[*auth.ReLoginRequiredError](err)
	_, authFailedErr := errors.AsType[*auth.AuthFailedError](err)

	if loginErr || authFailedErr {
		return UserContextError
	}

	if errors.Is(err, auth.ErrNoCurrentUser) ||
		errors.Is(err, azapi.ErrAzCliNotLoggedIn) ||
		errors.Is(err, azapi.ErrAzCliRefreshTokenExpired) ||
		errors.Is(err, github.ErrGitHubCliNotLoggedIn) ||
		errors.Is(err, github.ErrUserNotAuthorized) ||
		errors.Is(err, github.ErrRepositoryNameInUse) ||
		errors.Is(err, environment.ErrNotFound) ||
		errors.Is(err, environment.ErrNameNotSpecified) ||
		errors.Is(err, environment.ErrDefaultEnvironmentNotFound) ||
		errors.Is(err, environment.ErrAccessDenied) ||
		errors.Is(err, pipeline.ErrAuthNotSupported) ||
		errors.Is(err, pipeline.ErrRemoteHostIsNotAzDo) ||
		errors.Is(err, pipeline.ErrSSHNotSupported) ||
		errors.Is(err, pipeline.ErrRemoteHostIsNotGitHub) ||
		errors.Is(err, project.ErrNoDefaultService) {
		return UserContextError
	}

	return AzureContextAndOtherError
}

// shouldSkipErrorAnalysis returns true for control-flow errors that should not
// be sent to AI analysis
func shouldSkipErrorAnalysis(err error) bool {
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, surveyterm.InterruptErr) ||
		errors.Is(err, azdcontext.ErrNoProject) ||
		errors.Is(err, consent.ErrToolExecutionDenied) ||
		errors.Is(err, consent.ErrElicitationDenied) ||
		errors.Is(err, consent.ErrSamplingDenied) {
		return true
	}

	// Environment was already initialized
	_, ok := errors.AsType[*environment.EnvironmentInitError](err)
	return ok
}

func NewErrorMiddleware(
	options *Options, console input.Console,
	agentFactory *agent.CopilotAgentFactory,
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

		// Single consent prompt — user decides whether to engage the agent
		consent, err := e.promptTroubleshootConsent(ctx)
		if err != nil {
			span.SetStatus(codes.Error, "agent.consent.failed")
			return nil, fmt.Errorf("prompting for troubleshoot consent: %w", err)
		}

		if !consent {
			span.SetStatus(codes.Error, "agent.troubleshoot.declined")
			return actionResult, originalError
		}

		// Single agent interaction — the agent explains, proposes a fix,
		// asks the user, and applies it (or exits) via interactive mode.
		// The AgentDisplay streams all output to the console in real-time.
		troubleshootPrompt := e.buildTroubleshootingPrompt(originalError)

		previousError = originalError
		e.console.Message(ctx, color.MagentaString("Preparing Copilot to troubleshoot error..."))
		agentResult, err := azdAgent.SendMessage(ctx, troubleshootPrompt)

		if err != nil {
			span.SetStatus(codes.Error, "agent.send_message.failed")
			return nil, err
		}

		span.SetStatus(codes.Ok, "agent.troubleshoot.completed")

		// Display usage metrics if available
		if agentResult != nil && agentResult.Usage.TotalTokens() > 0 {
			e.console.Message(ctx, "")
			e.console.Message(ctx, agentResult.Usage.Format())
		}

		// Ask user if the agent applied a fix and they want to retry the command
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

// troubleshootPromptData is the data passed to the troubleshooting prompt templates.
type troubleshootPromptData struct {
	Command      string
	ErrorMessage string
}

// buildTroubleshootingPrompt renders the appropriate embedded template
// based on the error category.
func (e *ErrorMiddleware) buildTroubleshootingPrompt(err error) string {
	data := troubleshootPromptData{
		Command:      e.options.CommandPath,
		ErrorMessage: err.Error(),
	}

	tmpl := troubleshootFixableTemplate
	if classifyError(err) != AzureContextAndOtherError {
		tmpl = troubleshootManualTemplate
	}

	var buf bytes.Buffer
	if execErr := tmpl.Execute(&buf, data); execErr != nil {
		log.Printf("[copilot] Failed to execute troubleshooting template: %v", execErr)
		return fmt.Sprintf("An error occurred while running `%s`: %s\n\nPlease diagnose and explain this error.",
			data.Command, data.ErrorMessage)
	}

	return buf.String()
}

// promptTroubleshootConsent asks the user whether to engage the agent for troubleshooting.
// Checks saved preferences for "always allow" and "always skip" persistence.
func (e *ErrorMiddleware) promptTroubleshootConsent(ctx context.Context) (bool, error) {
	userConfig, err := e.userConfigManager.Load()
	if err != nil {
		return false, fmt.Errorf("failed to load user config: %w", err)
	}

	// Check for saved "always allow" preference
	if val, ok := userConfig.GetString(agentcopilot.ConfigKeyErrorHandlingFix); ok && val == "allow" {
		e.console.Message(ctx, output.WithWarningFormat(
			"Agent troubleshooting is set to always allow. To change, run %s.\n",
			output.WithHighLightFormat(
				fmt.Sprintf("azd config unset %s", agentcopilot.ConfigKeyErrorHandlingFix)),
		))
		return true, nil
	}

	// Check for saved "always skip" preference
	if val, ok := userConfig.GetString(agentcopilot.ConfigKeyErrorHandlingTroubleshootSkip); ok && val == "allow" {
		e.console.Message(ctx, output.WithWarningFormat(
			"Agent troubleshooting is set to always skip. To change, run %s.\n",
			output.WithHighLightFormat(
				fmt.Sprintf("azd config unset %s", agentcopilot.ConfigKeyErrorHandlingTroubleshootSkip)),
		))
		return false, nil
	}

	choices := []*uxlib.SelectChoice{
		{Value: "once", Label: "Yes, troubleshoot this error"},
		{Value: "always", Label: "Yes, always troubleshoot errors"},
		{Value: "no", Label: "No, skip"},
		{Value: "never", Label: "No, always skip"},
	}

	selector := uxlib.NewSelect(&uxlib.SelectOptions{
		Message: "Would you like the agent to troubleshoot this error?",
		HelpMessage: fmt.Sprintf(
			"The agent will explain the error and offer to fix it. "+
				"Edit permissions anytime by running %s.",
			output.WithHighLightFormat("azd copilot consent")),
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
		return false, fmt.Errorf("invalid choice selected")
	}

	selected := choices[*choiceIndex].Value

	switch selected {
	case "always":
		if err := userConfig.Set(agentcopilot.ConfigKeyErrorHandlingFix, "allow"); err != nil {
			return false, fmt.Errorf("failed to set config: %w", err)
		}
		if err := e.userConfigManager.Save(userConfig); err != nil {
			return false, fmt.Errorf("failed to save config: %w", err)
		}
		return true, nil
	case "never":
		if err := userConfig.Set(agentcopilot.ConfigKeyErrorHandlingTroubleshootSkip, "allow"); err != nil {
			return false, fmt.Errorf("failed to set config: %w", err)
		}
		if err := e.userConfigManager.Save(userConfig); err != nil {
			return false, fmt.Errorf("failed to save config: %w", err)
		}
		return false, nil
	case "no":
		return false, nil
	default:
		return true, nil
	}
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
		return false, nil
	}

	return choices[*choiceIndex].Value == "retry", nil
}
