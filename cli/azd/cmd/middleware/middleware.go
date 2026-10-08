// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/spf13/pflag"
)

// Registration function that returns a constructed middleware
type ResolveFn func() Middleware

// Defines a middleware component
type Middleware interface {
	Run(ctx context.Context, nextFn NextFn) (*actions.ActionResult, error)
}

type CancellationMiddleware struct{}

func NewCancellationMiddleware() Middleware {
	return &CancellationMiddleware{}
}

func (m *CancellationMiddleware) Run(
	ctx context.Context,
	nextFn NextFn,
) (*actions.ActionResult, error) {
	if IsChildAction(ctx) {
		return nextFn(ctx)
	}

	commandCtx, cancel := context.WithCancel(ctx)
	controller := &commandInterruptController{cancel: cancel}
	controller.popHandler = input.PushInterruptHandler(controller.handle)
	defer controller.close()

	result, err := nextFn(commandCtx)
	// A nested controller includes its context termination cause after it owns
	// the interrupt, so only a bare interrupted exit can have a host signal pending.
	awaitHostInterrupt := processWasInterrupted(err) &&
		!errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded)
	controller.finish(awaitHostInterrupt)
	ctxErr := commandCtx.Err()

	if ctxErr == nil || errors.Is(err, ctxErr) {
		return result, err
	}
	if err == nil {
		return result, ctxErr
	}

	return result, errors.Join(err, ctxErr)
}

type commandInterruptController struct {
	mu                    sync.Mutex
	cancel                context.CancelFunc
	cancellationRequested bool
	finished              bool
	awaitingHostInterrupt bool
	handledAfterFinish    bool
	popHandler            func()
	popOnce               sync.Once
}

func (c *commandInterruptController) handle() bool {
	c.mu.Lock()
	switch {
	case c.finished && c.awaitingHostInterrupt:
		c.awaitingHostInterrupt = false
		c.mu.Unlock()
		c.pop()
		return true
	case c.finished && !c.handledAfterFinish:
		c.handledAfterFinish = true
		c.mu.Unlock()
		return true
	case c.finished || c.cancellationRequested:
		c.mu.Unlock()
		return false
	default:
		c.cancellationRequested = true
		c.mu.Unlock()
		c.cancel()
		return true
	}
}

func (c *commandInterruptController) finish(awaitHostInterrupt bool) {
	c.mu.Lock()
	c.finished = true
	if awaitHostInterrupt && !c.cancellationRequested {
		// A console child can exit before azd's signal goroutine runs. Keep the
		// handler registered until that pending host interrupt is consumed.
		c.cancellationRequested = true
		c.awaitingHostInterrupt = true
	}
	cancellationRequested := c.cancellationRequested
	awaitingHostInterrupt := c.awaitingHostInterrupt
	c.mu.Unlock()

	if cancellationRequested {
		c.cancel()
	}
	if !awaitingHostInterrupt {
		c.pop()
	}
}

func (c *commandInterruptController) close() {
	c.cancel()
	c.finish(false)
}

func (c *commandInterruptController) pop() {
	c.popOnce.Do(func() {
		if c.popHandler != nil {
			c.popHandler()
		}
	})
}

func processWasInterrupted(err error) bool {
	type interruptedError interface {
		error
		Interrupted() bool
	}

	interruptErr, ok := errors.AsType[interruptedError](err)
	return ok && interruptErr.Interrupted()
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
	Annotations map[string]string
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
				return nil, err
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

// IsChildAction checks if the given context was created by WithChildAction.
// This is used to determine if a command is being executed as part of a workflow step.
func IsChildAction(ctx context.Context) bool {
	value, ok := ctx.Value(childActionKey).(bool)
	return ok && value
}
