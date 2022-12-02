package middleware

import (
	"context"
	"log"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

var middlewareChain []ResolveFn = []ResolveFn{}

type ResolveFn func() Middleware

type Middleware interface {
	Run(ctx context.Context, options Options, nextFn NextFn) (*actions.ActionResult, error)
}

type BuildFn func(
	commandOptions *internal.GlobalCommandOptions,
	actionOptions *actions.ActionOptions,
	console input.Console) (Middleware, error)

type Options struct {
	Name    string
	Aliases []string
}

func Build(
	commandOptions *internal.GlobalCommandOptions,
	actionOptions *actions.ActionOptions,
	console input.Console,
	buildFn BuildFn,
) ResolveFn {
	return func() Middleware {
		middleware, err := buildFn(commandOptions, actionOptions, console)
		if err != nil {
			log.Printf("Unable to create middleware: %s\n", err.Error())
			return nil
		}

		return middleware
	}
}

// Executes the next middleware in the command chain
type NextFn func(ctx context.Context) (*actions.ActionResult, error)

// Executes the middleware chain for the specified action
func RunAction(
	ctx context.Context,
	options Options,
	action actions.Action,
) (*actions.ActionResult, error) {
	chainLength := len(middlewareChain)
	index := 0

	var nextFn NextFn

	nextFn = func(nextContext context.Context) (*actions.ActionResult, error) {
		if index < chainLength {
			resolver := middlewareChain[index]
			index++

			middleware := resolver()
			if middleware == nil {
				return nil, nil
			}

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

func Use(resolveMiddleware ResolveFn) {
	middlewareChain = append(middlewareChain, resolveMiddleware)
}
