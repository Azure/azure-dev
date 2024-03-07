package middleware

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

// Adds support to easily debug and attach a debugger to AZD for development purposes
type DebugMiddleware struct {
	options *Options
	console input.Console
}

// Creates a new instance of the Debug middleware
func NewDebugMiddleware(options *Options, console input.Console) Middleware {
	return &DebugMiddleware{
		options: options,
		console: console,
	}
}

// Invokes the debug middleware. When AZD_DEBUG is set will prompt the user to attach
// a debugger before continuing invocation of the action
func (m *DebugMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	// Don't run for sub actions
	if m.options.IsChildAction(ctx) {
		return next(ctx)
	}

	envName := "AZD_DEBUG"

	if strings.Contains(m.options.CommandPath, "telemetry") {
		// Use a different flag for telemetry commands. This avoids stopping telemetry background upload processes
		// unintentionally by default when debugging interactive commands.
		// AZD_DEBUG_TELEMETRY can be used instead to debug any background telemetry processes.
		envName = "AZD_DEBUG_TELEMETRY"
	}

	debugStr := os.Getenv(envName)
	if debugStr == "" {
		return next(ctx)
	}

	debug, err := strconv.ParseBool(debugStr)
	if err != nil {
		log.Printf("failed converting AZD_DEBUG to boolean: %s", err.Error())
	}

	if !debug {
		return next(ctx)
	}

	for {
		isReady, err := m.console.Confirm(ctx, input.ConsoleOptions{
			Message:      fmt.Sprintf("Debugger Ready? (pid: %d)", os.Getpid()),
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
