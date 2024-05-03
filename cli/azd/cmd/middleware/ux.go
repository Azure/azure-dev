package middleware

import (
	"context"
	"errors"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

type UxMiddleware struct {
	options *Options
	console input.Console
}

func NewUxMiddleware(options *Options, console input.Console) Middleware {
	return &UxMiddleware{
		options: options,
		console: console,
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
		m.console.Message(ctx, output.WithErrorFormat("\nERROR: %s", err.Error()))

		if errors.As(err, &errorWithTraceId) {
			m.console.Message(ctx, output.WithErrorFormat("TraceID: %s", errorWithTraceId.TraceId))
		}

		if errors.As(err, &suggestionErr) {
			m.console.Message(ctx, suggestionErr.Suggestion)
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
