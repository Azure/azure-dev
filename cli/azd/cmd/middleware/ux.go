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
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
)

type UxMiddleware struct {
	options         *Options
	console         input.Console
	featuresManager *alpha.FeatureManager
}

func NewUxMiddleware(options *Options, console input.Console, featuresManager *alpha.FeatureManager) Middleware {
	return &UxMiddleware{
		options:         options,
		console:         console,
		featuresManager: featuresManager,
	}
}

func (m *UxMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	// Don't run for sub actions
	if IsChildAction(ctx) {
		return next(ctx)
	}

	actionResult, err := next(ctx)

	// Stop the spinner always to un-hide cursor
	m.console.StopSpinner(ctx, "", input.Step)

	if err != nil {
		var suggestionErr *internal.ErrorWithSuggestion
		var errorWithTraceId *internal.ErrorWithTraceId
		errorMessage := &strings.Builder{}
		// WriteString never returns an error
		errorMessage.WriteString(output.WithErrorFormat("\nERROR: %s", err.Error()))

		if errors.As(err, &errorWithTraceId) {
			errorMessage.WriteString(output.WithErrorFormat("\nTraceID: %s", errorWithTraceId.TraceId))
		}

		// Handle prompt timeout by adding a suggestion to use --no-prompt
		var promptTimeoutErr *uxlib.ErrPromptTimeout
		if errors.As(err, &promptTimeoutErr) {
			suggestion := fmt.Sprintf(
				"\nSuggestion: To run non-interactively without input prompts, use: %s --no-prompt\n"+
					"To disable prompt timeouts, set the AZD_PROMPT_TIMEOUT environment variable to 0.",
				m.options.CommandPath,
			)
			errorMessage.WriteString(suggestion)
		} else if errors.As(err, &suggestionErr) {
			errorMessage.WriteString("\n" + suggestionErr.Suggestion)
		}

		errMessage := errorMessage.String()

		// For specific errors, we silent the output display here and let the caller handle it
		var unsupportedErr *project.UnsupportedServiceHostError
		var extensionRunErr *extensions.ExtensionRunError
		if errors.As(err, &extensionRunErr) {
			return actionResult, err
		} else if errors.As(err, &unsupportedErr) {
			// set the error message so the caller can use it if needed
			unsupportedErr.ErrorMessage = errMessage
			return actionResult, err
		} else {
			m.console.Message(ctx, errMessage)
		}

	}

	if actionResult != nil && actionResult.Message != nil {
		displayResult := &ux.ActionResult{
			SuccessMessage: actionResult.Message.Header,
			FollowUp:       actionResult.Message.FollowUp,
		}

		m.console.MessageUxItem(ctx, displayResult)
	}

	return actionResult, err
}
