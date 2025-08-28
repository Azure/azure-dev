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
)

type ErrorMiddleware struct {
	options         *Options
	console         input.Console
	agentFactory    *agent.AgentFactory
	global          *internal.GlobalCommandOptions
	featuresManager *alpha.FeatureManager
}

func NewErrorMiddleware(options *Options, console input.Console, agentFactory *agent.AgentFactory, global *internal.GlobalCommandOptions, featuresManager *alpha.FeatureManager) Middleware {
	return &ErrorMiddleware{
		options:         options,
		console:         console,
		agentFactory:    agentFactory,
		global:          global,
		featuresManager: featuresManager,
	}
}

func (e *ErrorMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	if e.featuresManager.IsEnabled(llm.FeatureLlm) {
		if e.options.IsChildAction(ctx) {
			return next(ctx)
		}

		actionResult, err := next(ctx)
		attempt := 0
		var previousError error
		originalError := err

		// skipAnalyzingErrors := []error{
		// context.Canceled,
		// }
		skipAnalyzingErrors := []string{
			"environment already initialized",
			"interrupt",
		}

		for {
			if err == nil {
				break
			}

			// for _, e := range skipAnalyzingErrors {
			// 	if errors.As(err, &e) || errors.Is(err, e) {
			// 		return actionResult, err
			// 	}
			// }

			for _, s := range skipAnalyzingErrors {
				if strings.Contains(err.Error(), s) {
					return actionResult, err
				}
			}

			if previousError != nil && errors.Is(originalError, previousError) {
				attempt++
				if attempt > 3 {
					e.console.Message(ctx, "AI was unable to resolve the error after multiple attempts. Please review the error and fix it manually.")
					return actionResult, err
				}
			}

			// e.console.Confirm(ctx, input.ConsoleOptions{
			// 	Message:      "Debugger Ready?",
			// 	DefaultValue: true,
			// })
			e.console.StopSpinner(ctx, "", input.Step)
			e.console.Message(ctx, output.WithErrorFormat("\nERROR: %s", originalError.Error()))

			// Warn user that this is an alpha feature
			e.console.WarnForFeature(ctx, llm.FeatureLlm)

			azdAgent, cleanup, err := e.agentFactory.Create(agent.WithDebug(e.global.EnableDebugLogging))
			if err != nil {
				return nil, err
			}

			defer cleanup()

			agentOutput, err := azdAgent.SendMessage(ctx, fmt.Sprintf(
				`Steps to follow:
			1. Use available tool to identify, explain and diagnose this error when running azd command and its root cause.
			2. Provide actionable troubleshooting steps.
			Error details: %s`, originalError.Error()))
			if err != nil {
				if agentOutput != "" {
					e.console.Message(ctx, output.WithMarkdown(agentOutput))
				}

				return nil, err
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
			case 0: // fix the error
				previousError = originalError
				agentOutput, err = azdAgent.SendMessage(ctx, fmt.Sprintf(
					`Steps to follow:
			1. Use available tool to identify, explain and diagnose this error when running azd command and its root cause.
			2. Resolve the error by iterating and attempting to solve all errors until the azd command succeeds.
			Error details: %s`, originalError.Error()))

				if err != nil {
					if agentOutput != "" {
						e.console.Message(ctx, output.WithMarkdown(agentOutput))
					}

					return nil, err
				}
			case 1:
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

				return actionResult, err
			}

			ctx = tools.WithInstalledCheckCache(ctx)
			actionResult, err = next(ctx)
			originalError = err
		}

		return actionResult, err
	}

	actionResult, err := next(ctx)

	return actionResult, err
}
