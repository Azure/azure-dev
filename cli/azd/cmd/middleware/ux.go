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
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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

	// User intentionally aborted — not a failure.
	// The action already printed a message; swallow the error so the CLI exits with code 0.
	if errors.Is(err, internal.ErrAbortedByUser) {
		return actionResult, nil
	}

	if err != nil {
		// Use ErrorWithSuggestion for errors with suggestions (better UX).
		// This catches errors wrapped by the error pipeline's YAML rules
		// or other host code that already created an ErrorWithSuggestion.
		if suggestionErr, ok := errors.AsType[*internal.ErrorWithSuggestion](err); ok {
			displayErr := &ux.ErrorWithSuggestion{
				Err:        suggestionErr.Err,
				Message:    suggestionErr.Message,
				Suggestion: suggestionErr.Suggestion,
				Links:      suggestionErr.Links,
			}
			m.console.Message(ctx, "")
			m.console.MessageUxItem(ctx, displayErr)
			return actionResult, err
		}

		// Bridge extension errors (LocalError/ServiceError) with suggestions to rich UX.
		// Covers both CLI extension commands and gRPC service target errors.
		if suggestion := azdext.ErrorSuggestion(err); suggestion != "" {
			message := azdext.ErrorMessage(err)
			if message == "" {
				message = err.Error()
			}
			displayErr := &ux.ErrorWithSuggestion{
				Message:    message,
				Suggestion: suggestion,
			}
			m.console.Message(ctx, "")
			m.console.MessageUxItem(ctx, displayErr)
			return actionResult, err
		}

		// ExtensionRunError without suggestion
		if _, ok := errors.AsType[*extensions.ExtensionRunError](err); ok {
			if message := azdext.ErrorMessage(err); message != "" {
				m.console.Message(ctx, output.WithErrorFormat("\nERROR: %s", message))
			}
			return actionResult, err
		}

		// Build error message for errors without suggestions
		errorMessage := &strings.Builder{}
		errorMessage.WriteString(output.WithErrorFormat("\nERROR: %s", err.Error()))

		if errorWithTraceId, ok := errors.AsType[*internal.ErrorWithTraceId](err); ok {
			errorMessage.WriteString(output.WithErrorFormat("\nTraceID: %s", errorWithTraceId.TraceId))
		}

		errMessage := errorMessage.String()

		if unsupportedErr, ok := errors.AsType[*project.UnsupportedServiceHostError](err); ok {
			// set the error message so the caller can use it if needed
			unsupportedErr.ErrorMessage = errMessage
			return actionResult, err
		}

		m.console.Message(ctx, errMessage)

		// Print out additional text for errors that have it.
		if uxItemErr, ok := errors.AsType[interface {
			error
			ux.UxItem
		}](err); ok {
			m.console.Message(ctx, "")
			m.console.MessageUxItem(ctx, uxItemErr)
			return actionResult, err
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
