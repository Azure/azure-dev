package middleware

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
)

// Defines a middleware component
type Middleware interface {
	Run(ctx context.Context, nextFn NextFn) (*actions.ActionResult, error)
}

type MiddlewareRunner struct {
	middlewareChain []Middleware
	action          actions.Action
}

func NewMiddlewareRunner(
	action actions.Action,
	// Middleware registration
	debug *DebugMiddleware,
	telemetry *TelemetryMiddleware,
) *MiddlewareRunner {
	return &MiddlewareRunner{
		// Middleware chain, in order of execution
		middlewareChain: []Middleware{
			debug,
			telemetry},
		action: action,
	}
}

func (r *MiddlewareRunner) Run(ctx context.Context) (*actions.ActionResult, error) {
	chainLength := len(r.middlewareChain)
	index := 0

	var nextFn NextFn

	// This recursive function executes the middleware chain in the order that
	// the middlewares were registered. nextFn is passed into the middleware run
	// allowing the middleware to choose to execute logic before and/or after
	// the action. After we have executed all of the middlewares the action is run
	// and the chain is unwrapped back out through the call stack.
	nextFn = func(nextContext context.Context) (*actions.ActionResult, error) {
		if index < chainLength {
			middleware := r.middlewareChain[index]
			index++

			return middleware.Run(nextContext, nextFn)
		} else {
			return r.action.Run(ctx)
		}
	}

	result, err := nextFn(ctx)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// Executes the next middleware in the command chain
type NextFn func(ctx context.Context) (*actions.ActionResult, error)
