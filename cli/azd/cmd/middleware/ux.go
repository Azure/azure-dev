package middleware

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/operations"
)

// UxMiddleware is a middleware that handles the UX of the CLI
type UxMiddleware struct {
	options          *Options
	operationPrinter operations.Printer
}

// Creates a new instance of the UX middleware
func NewUxMiddleware(options *Options, operationPrinter operations.Printer) Middleware {
	return &UxMiddleware{
		options:          options,
		operationPrinter: operationPrinter,
	}
}

func (m *UxMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	// Don't run for sub actions
	if m.options.IsChildAction() {
		return next(ctx)
	}

	if err := m.operationPrinter.Start(ctx); err != nil {
		return nil, err
	}
	defer m.operationPrinter.Stop(ctx)

	return next(ctx)
}
