// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

type ErrorMiddleware struct {
	options      *Options
	console      input.Console
	agentFactory *agent.AgentFactory
	global       *internal.GlobalCommandOptions
}

func NewErrorMiddleware(options *Options, console input.Console, agentFactory *agent.AgentFactory, global *internal.GlobalCommandOptions) Middleware {
	return &ErrorMiddleware{
		options:      options,
		console:      console,
		agentFactory: agentFactory,
		global:       global,
	}
}

func (e *ErrorMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	// if m.options.IsChildAction(ctx) {
	// 	return next(ctx)
	// }
	var actionResult *actions.ActionResult
	var err error

	for {
		actionResult, err = next(ctx)
		originalError := err

		if err == nil {
			break
		}

		e.console.StopSpinner(ctx, "", input.Step)
		e.console.Message(ctx, output.WithErrorFormat("\nERROR: %s", err.Error()))

		// Explicitly call the troubleshooting tool
		azdAgent, cleanup, err := e.agentFactory.Create(agent.WithDebug(e.global.EnableDebugLogging))
		if err != nil {
			return nil, err
		}

		defer cleanup()

		// TODO: Check the prompt with copilot
		agentOutput, err := azdAgent.SendMessage(ctx, fmt.Sprintf(
			`Steps to follow:
			1. Use available tool to explain and diagnose this error when running azd command.
			2. Resolve the error by iterating and attempting to solve all error until they're working.
			This is the error messages: %s`, originalError.Error()))
		if err != nil {
			if agentOutput != "" {
				e.console.Message(ctx, output.WithMarkdown(agentOutput))
			}

			return nil, err
		}

		e.console.Message(ctx, "Test")
		e.console.Message(ctx, fmt.Sprintf("%s:", output.AzdAgentLabel()))
		e.console.Message(ctx, output.WithMarkdown(agentOutput))
		e.console.Message(ctx, "")
	}

	if actionResult != nil && actionResult.Message != nil {
		displayResult := &ux.ActionResult{
			SuccessMessage: actionResult.Message.Header,
			FollowUp:       actionResult.Message.FollowUp,
		}

		e.console.Message(ctx, "test")
		e.console.MessageUxItem(ctx, displayResult)
	}

	return actionResult, err
}
