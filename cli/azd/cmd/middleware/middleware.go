package middleware

import (
	"context"
	"log"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/golobby/container/v3"
)

var ioc container.Container = container.Global
var middlewareChain []string = []string{}

// Registration function that returns a constructed middleware
type ResolveFn func() Middleware

// Defines a middleware component
type Middleware interface {
	Run(ctx context.Context, options Options, nextFn NextFn) (*actions.ActionResult, error)
}

// Middleware Run options
type Options struct {
	Name    string
	Aliases []string
}

// Executes the next middleware in the command chain
type NextFn func(ctx context.Context) (*actions.ActionResult, error)

func SetContainer(c container.Container) {
	ioc = c
}

// Executes the middleware chain for the specified action
func RunAction(
	ctx context.Context,
	options Options,
	action actions.Action,
) (*actions.ActionResult, error) {
	chainLength := len(middlewareChain)
	index := 0

	var nextFn NextFn

	// This recursive function executes the middleware chain in the order that
	// the middlewares were registered. nextFn is passed into the middleware run
	// allowing the middleware to choose to execute logic before and/or after
	// the action. After we have executed all of the middlewares the action is run
	// and the chain is unwrapped back out through the call stack.
	nextFn = func(nextContext context.Context) (*actions.ActionResult, error) {
		if index < chainLength {
			middlewareName := middlewareChain[index]
			index++

			var middleware Middleware
			err := ioc.NamedResolve(&middleware, middlewareName)
			if err != nil {
				log.Printf("failed resolving middleware '%s' : %s\n", middlewareName, err.Error())
			}

			// It is an expected scenario that the middleware cannot be resolved
			// due to missing dependency or other project configuration.
			// In this case simply continue the chain with `nextFn`
			if middleware == nil {
				return nextFn(nextContext)
			}

			log.Printf("running middleware '%s'\n", middlewareName)
			return middleware.Run(nextContext, options, nextFn)
		} else {
			return action.Run(ctx)
		}
	}

	result, err := nextFn(ctx)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// Registers middleware components that will be run for all actions
func Use(name string, resolveFn any) {
	container.MustNamedSingletonLazy(ioc, name, resolveFn)
	middlewareChain = append(middlewareChain, name)
}
