// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

// InteractiveGuardMiddleware rejects commands that require interactive input when prompts are disabled.
type InteractiveGuardMiddleware struct {
	console input.Console
	options *Options
}

// NewInteractiveGuardMiddleware creates a new instance of the InteractiveGuardMiddleware.
func NewInteractiveGuardMiddleware(console input.Console, options *Options) Middleware {
	return &InteractiveGuardMiddleware{
		console: console,
		options: options,
	}
}

// Run returns an error in no-prompt mode, otherwise it runs the next middleware or action.
func (m *InteractiveGuardMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	if m.console.IsNoPromptMode() {
		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"%s requires interactive input and cannot be run with --no-prompt: %w",
				m.options.CommandPath,
				internal.ErrInteractiveRequired,
			),
			Message: fmt.Sprintf(
				"%s requires interactive input and cannot be run with --no-prompt. No changes were made.",
				m.options.CommandPath,
			),
		}
	}

	return next(ctx)
}
