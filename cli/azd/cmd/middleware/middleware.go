package middleware

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"

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

type childActionKeyType string

var childActionKey childActionKeyType = "child-action"

// Middleware Run options
type Options struct {
	container *ioc.NestedContainer

	CommandPath string
	Name        string
	Aliases     []string
	Flags       *pflag.FlagSet
	Args        []string
}

func (o *Options) IsChildAction(ctx context.Context) bool {
	value, ok := ctx.Value(childActionKey).(bool)
	return ok && value
}

// Sets the container to be used for resolving middleware components
func (o *Options) WithContainer(container *ioc.NestedContainer) {
	o.container = container
}

// Executes the next middleware in the command chain
type NextFn func(ctx context.Context) (*actions.ActionResult, error)

// Middleware runner stores middleware registrations and orchestrates the
// invocation of middleware components and actions.
type MiddlewareRunner struct {
	chain     []string
	container *ioc.NestedContainer
}

// Creates a new middleware runner
func NewMiddlewareRunner(container *ioc.NestedContainer) *MiddlewareRunner {
	return &MiddlewareRunner{
		chain:     []string{},
		container: container,
	}
}

// Executes the middleware chain for the specified action
func (r *MiddlewareRunner) RunAction(
	ctx context.Context,
	runOptions *Options,
	actionName string,
) (*actions.ActionResult, error) {
	chainLength := len(r.chain)
	index := 0

	var nextFn NextFn

	// We need to get the actionContainer for the current executing scope
	actionContainer := runOptions.container
	if actionContainer == nil {
		actionContainer = r.container
	}

	// Create a new context with the child container which will be leveraged on any child command/actions
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
			var action actions.Action

			if err := actionContainer.ResolveNamed(actionName, &action); err != nil {
				if errors.Is(err, ioc.ErrResolveInstance) {
					return nil, fmt.Errorf(
						//nolint:lll
						"failed resolving action '%s'. Ensure the ActionResolver is a valid go function that returns an `actions.Action` interface, %w",
						actionName,
						err,
					)
				}

				return nil, err
			}

			return action.Run(ctx)
		}
	}

	return nextFn(ctx)
}

// Registers middleware components that will be run for all actions
func (r *MiddlewareRunner) Use(name string, resolveFn any) error {
	if err := r.container.RegisterNamedTransient(name, resolveFn); err != nil {
		return err
	}

	if !slices.Contains(r.chain, name) {
		r.chain = append(r.chain, name)
	}

	return nil
}

func WithChildAction(ctx context.Context) context.Context {
	return context.WithValue(ctx, childActionKey, true)
}
