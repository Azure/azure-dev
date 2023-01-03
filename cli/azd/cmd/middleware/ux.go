package middleware

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

// UxMiddleware composes output message of actions that return ActionResults
type UxMiddleware struct {
	console input.Console
}

// Creates a new Ux Middleware instance
func NewUxMiddleware(console input.Console) Middleware {
	return &UxMiddleware{
		console: console,
	}
}

// Invokes the middleware and prints out and action result status after the
// underlying action completes
func (r *UxMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	result, err := next(ctx)

	if result != nil {
		r.console.MessageUxItem(ctx, actions.ToUxItem(result, err))
	}

	return result, err
}
