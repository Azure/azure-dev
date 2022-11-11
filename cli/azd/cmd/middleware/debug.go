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

type DebugMiddleware struct {
	console input.Console
}

func NewDebugMiddleware(console input.Console) *DebugMiddleware {
	return &DebugMiddleware{
		console: console,
	}
}

func (m *DebugMiddleware) Run(ctx context.Context, _ Options, next NextFn) (*actions.ActionResult, error) {
	debug, err := strconv.ParseBool(os.Getenv("AZD_DEBUG"))
	if err != nil {
		log.Printf("failed converting AZD_DEBUG to boolean: %s", err.Error())
	}

	if !debug {
		return next(ctx)
	}

	for {
		isReady, err := m.console.Confirm(ctx, input.ConsoleOptions{
			Message:      "Debugger Ready?",
			DefaultValue: true,
		})

		if err != nil {
			m.console.Message(ctx, fmt.Sprintf("confirmation failed! %s", err.Error()))
		}

		if isReady {
			return next(ctx)
		}
	}
}
