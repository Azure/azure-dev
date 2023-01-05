package middleware

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

// UxMiddleware composes output message of actions that return ActionResults
type UxMiddleware struct {
	options *Options
	console input.Console
}

// Creates a new Ux Middleware instance
func NewUxMiddleware(options *Options, console input.Console) Middleware {
	return &UxMiddleware{
		options: options,
		console: console,
	}
}

// Invokes the middleware and prints out and action result status after the
// underlying action completes
func (r *UxMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	result, err := next(ctx)

	// If the executing action is a nested/sub action we want to omit printing the final UX Item
	if r.options.IsChildAction() {
		return result, err
	}

	// It is valid for a command to return a nil action result and error. If we have a result or an error, display it,
	// otherwise don't print anything.
	if result != nil || err != nil {
		r.console.MessageUxItem(ctx, actions.ToUxItem(result, err))
	}

	return result, err
}
