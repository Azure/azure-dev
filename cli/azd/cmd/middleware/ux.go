package middleware

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

type UxMiddleware struct {
	console input.Console
}

func NewUxMiddleware(console input.Console) Middleware {
	return &UxMiddleware{
		console: console,
	}
}

func (r *UxMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	result, err := next(ctx)

	if result != nil {
		r.console.MessageUxItem(ctx, actions.ToActionResult(result, err))
	}

	return result, err
}
