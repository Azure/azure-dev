package middleware

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

func UseDebug(console input.Console) actions.MiddlewareFn {
	return func(ctx context.Context, options *actions.ActionOptions, next actions.NextFn) (*actions.ActionResult, error) {
		debug, err := strconv.ParseBool(os.Getenv("AZD_DEBUG"))
		if err != nil {
			log.Printf("failed converting AZD_DEBUG to boolean: %s", err.Error())
		}

		if !debug {
			return next(ctx)
		}

		for {
			isReady, err := console.Confirm(ctx, input.ConsoleOptions{
				Message:      "Debugger Ready?",
				DefaultValue: true,
			})

			if err != nil {
				console.Message(ctx, fmt.Sprintf("confirmation failed! %s", err.Error()))
			}

			if isReady {
				return next(ctx)
			}
		}
	}
}
