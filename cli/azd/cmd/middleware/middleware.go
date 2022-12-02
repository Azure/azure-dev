package middleware

import (
	"context"
	"log"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

var middlewareChain []Middleware = []Middleware{}

type Middleware interface {
	Run(ctx context.Context, nextFn NextFn) (*actions.ActionResult, error)
}

type BuildFn func(
	commandOptions *internal.GlobalCommandOptions,
	actionOptions *actions.ActionOptions,
	console input.Console) (Middleware, error)

func Build(
	commandOptions *internal.GlobalCommandOptions,
	actionOptions *actions.ActionOptions,
	console input.Console,
	buildFn BuildFn,
) Middleware {
	middleware, err := buildFn(commandOptions, actionOptions, console)
	if err != nil {
		log.Printf("Unable to create middleware: %s\n", err.Error())
		return nil
	}

	return middleware
}

// Executes the next middleware in the command chain
type NextFn func(ctx context.Context) (*actions.ActionResult, error)

// Executes the middleware chain for the specified action
func RunAction(
	ctx context.Context,
	action actions.Action,
) (*actions.ActionResult, error) {
	chainLength := len(middlewareChain)
	index := 0

	var nextFn NextFn

	nextFn = func(nextContext context.Context) (*actions.ActionResult, error) {
		if index < chainLength {
			middleware := middlewareChain[index]
			index++
			return middleware.Run(nextContext, nextFn)
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

func Use(middleware Middleware) {
	if middleware != nil {
		middlewareChain = append(middlewareChain, middleware)
	}
}
