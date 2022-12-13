package middleware

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

type CommandHooksMiddleware struct {
	actionOptions *actions.BuildOptions
	runner        exec.CommandRunner
	console       input.Console
}

func NewCommandHooksMiddleware(
	actionOptions *actions.BuildOptions,
	runner exec.CommandRunner,
	console input.Console,
) *CommandHooksMiddleware {
	return &CommandHooksMiddleware{
		actionOptions: actionOptions,
		runner:        runner,
		console:       console,
	}
}

func (m *CommandHooksMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	res, err := m.runner.Run(ctx, exec.NewRunArgs("git", "version"))
	if err != nil {
		return nil, fmt.Errorf("error running pre-hook: %w", err)
	}

	m.console.Message(ctx, res.Stdout)

	return next(ctx)
}
