// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
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

		// For specific errors, we silent the output display here and let the caller handle it
		var unsupportedErr *project.UnsupportedServiceHostError
		var extensionRunErr *extensions.ExtensionRunError
		if errors.As(err, &extensionRunErr) {
			return actionResult, err
		}

		// Use ErrorWithSuggestion for errors with suggestions (better UX)
		if errors.As(err, &suggestionErr) {
			displayErr := &ux.ErrorWithSuggestion{
				Err:        suggestionErr.Err,
				Message:    suggestionErr.Message,
				Suggestion: suggestionErr.Suggestion,
				Links:      suggestionErr.Links,
			}
			m.console.MessageUxItem(ctx, displayErr)
			return actionResult, err
		}

		// Build error message for errors without suggestions
		errorMessage := &strings.Builder{}
		errorMessage.WriteString(output.WithErrorFormat("\nERROR: %s", err.Error()))

		if errors.As(err, &errorWithTraceId) {
			errorMessage.WriteString(output.WithErrorFormat("\nTraceID: %s", errorWithTraceId.TraceId))
		}

		errMessage := errorMessage.String()

		if errors.As(err, &unsupportedErr) {
			// set the error message so the caller can use it if needed
			unsupportedErr.ErrorMessage = errMessage
			return actionResult, err
		}

		m.console.Message(ctx, errMessage)
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
