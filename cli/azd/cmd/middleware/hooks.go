package middleware

import (
	"context"
	"log"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

type CommandHooksMiddleware struct {
	projectConfig *project.ProjectConfig
	options       *Options
	hooksRunner   *ext.HooksRunner
}

func NewCommandHooksMiddleware(
	projectConfig *project.ProjectConfig,
	options *Options,
	hooksRunner *ext.HooksRunner,
) Middleware {
	return &CommandHooksMiddleware{
		projectConfig: projectConfig,
		options:       options,
		hooksRunner:   hooksRunner,
	}
}

func (m *CommandHooksMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	if m.projectConfig.Hooks == nil || len(m.projectConfig.Hooks) == 0 {
		log.Println("project does not contain any command hooks.")
		return next(ctx)
	}

	var actionResult *actions.ActionResult

	commandNames := []string{m.options.Name}
	commandNames = append(commandNames, m.options.Aliases...)
	err := m.hooksRunner.Invoke(ctx, commandNames, func() error {
		result, err := next(ctx)
		if err != nil {
			return err
		}

		actionResult = result
		return nil
	})

	if err != nil {
		return nil, err
	}

	return actionResult, nil
}
