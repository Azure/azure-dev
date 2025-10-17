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
	if m.options.IsChildAction(ctx) {
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

		if errors.As(err, &suggestionErr) {
			errorMessage.WriteString("\n" + suggestionErr.Suggestion)
		}

		// UnsupportedServiceHostError is a special error which needs to float up without printing output here yet.
		// The error is bubble up for the caller to decide to show it or not
		var unsupportedErr *project.UnsupportedServiceHostError
		errMessage := errorMessage.String()
		if errors.As(err, &unsupportedErr) {
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
