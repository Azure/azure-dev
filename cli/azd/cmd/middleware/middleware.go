package middleware

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/spf13/pflag"
)

// Registration function that returns a constructed middleware
type ResolveFn func() Middleware

// Defines a middleware component
type Middleware interface {
	Run(ctx context.Context, nextFn NextFn) (*actions.ActionResult, error)
}

// MiddlewareContext allow composite actions to orchestrate invoking child actions
type MiddlewareContext interface {
	// Executes the middleware chain for the specified child action
	RunChildAction(
		ctx context.Context,
		runOptions *Options,
		action actions.Action,
	) (*actions.ActionResult, error)
}

// Middleware Run options
type Options struct {
	CommandPath   string
	Name          string
	Aliases       []string
	Flags         *pflag.FlagSet
	Args          []string
	isChildAction bool
}

func (o *Options) IsChildAction() bool {
	return o.isChildAction
}

// Executes the next middleware in the command chain
type NextFn func(ctx context.Context) (*actions.ActionResult, error)

// Middleware runner stores middleware registrations and orchestrates the
// invocation of middleware components and actions.
type MiddlewareRunner struct {
	chain       []string
	container   *ioc.NestedContainer
	actionCache map[actions.Action]*actions.ActionResult
}

// Creates a new middleware runner
func NewMiddlewareRunner(container *ioc.NestedContainer) *MiddlewareRunner {
	return &MiddlewareRunner{
		container:   container,
		chain:       []string{},
		actionCache: map[actions.Action]*actions.ActionResult{},
	}
}

// Executes the middleware chain for the specified child action
func (r *MiddlewareRunner) RunChildAction(
	ctx context.Context,
	runOptions *Options,
	action actions.Action,
) (*actions.ActionResult, error) {
	// If we have previously run this action then return the cached result
	if cachedActionResult, has := r.actionCache[action]; has {
		return cachedActionResult, nil
	}

	// If we have not previously run this action then execute it
	runOptions.isChildAction = true
	result, err := r.RunAction(ctx, runOptions, action)

	// Cache the result on action success
	if err == nil {
		r.actionCache[action] = result
	}

	return result, err
}

// Executes the middleware chain for the specified action
func (r *MiddlewareRunner) RunAction(
	ctx context.Context,
	runOptions *Options,
	action actions.Action,
) (*actions.ActionResult, error) {
	chainLength := len(r.chain)
	index := 0

	var nextFn NextFn

	actionContainer := ioc.NewNestedContainer(r.container)
	ioc.RegisterInstance(actionContainer, runOptions)

	// This recursive function executes the middleware chain in the order that
	// the middlewares were registered. nextFn is passed into the middleware run
	// allowing the middleware to choose to execute logic before and/or after
	// the action. After we have executed all of the middlewares the action is run
	// and the chain is unwrapped back out through the call stack.
	nextFn = func(ctx context.Context) (*actions.ActionResult, error) {
		if index < chainLength {
			middlewareName := r.chain[index]
			index++

			var middleware Middleware
			if err := actionContainer.ResolveNamed(middlewareName, &middleware); err != nil {
				log.Printf("failed resolving middleware '%s' : %s\n", middlewareName, err.Error())
			}

			// It is an expected scenario that the middleware cannot be resolved
			// due to missing dependency or other project configuration.
			// In this case simply continue the chain with `nextFn`
			if middleware == nil {
				return nextFn(ctx)
			}

			log.Printf("running middleware '%s'\n", middlewareName)
			return middleware.Run(ctx, nextFn)
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
func (r *MiddlewareRunner) Use(name string, resolveFn any) error {
	if err := r.container.RegisterNamedTransient(name, resolveFn); err != nil {
		return fmt.Errorf("failed registering middleware '%s'. Ensure the resolver is a go function. %w", name, err)
	}

	r.chain = append(r.chain, name)

	return nil
}
