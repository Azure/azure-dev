package middleware

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
)

// SkipMiddleware is used in select testing scenarios where we
// need to skip the invocation of the middleware & action pipeline
// and just return a value
type SkipMiddleware struct {
}

// Creates a new Skip Middleware
func NewSkipMiddleware() Middleware {
	return &SkipMiddleware{}
}

// Skips the middleware pipeline and returns a nil value
func (r *SkipMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	return nil, nil
}
