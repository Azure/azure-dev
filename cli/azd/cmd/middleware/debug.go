package middleware

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

func UseDebug() actions.MiddlewareFn {
	return func(ctx context.Context, options *actions.ActionOptions, next actions.NextFn) (*actions.ActionResult, error) {
		console := input.GetConsole(ctx)
		console.Confirm(ctx, input.ConsoleOptions{
			Message:      "Debugger Ready?",
			DefaultValue: true,
		})

		return next(ctx)
	}
}
