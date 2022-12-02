package middleware

import (
	"context"
	"log"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

type CommandHooksMiddleware struct {
	actionOptions *actions.ActionOptions
	projectConfig *project.ProjectConfig
	commandHooks  *ext.CommandHooks
}

func NewCommandHooksMiddleware(
	actionOptions *actions.ActionOptions,
	projectConfig *project.ProjectConfig,
	commandHooks *ext.CommandHooks,
) *CommandHooksMiddleware {
	return &CommandHooksMiddleware{
		actionOptions: actionOptions,
		projectConfig: projectConfig,
		commandHooks:  commandHooks,
	}
}

func (m *CommandHooksMiddleware) Run(ctx context.Context, options Options, next NextFn) (*actions.ActionResult, error) {
	if m.projectConfig.Scripts == nil || len(m.projectConfig.Scripts) == 0 {
		log.Println("project does not contain any command hooks.")
		return next(ctx)
	}

	var actionResult *actions.ActionResult

	commandNames := []string{options.Name}
	commandNames = append(commandNames, options.Aliases...)
	err := m.commandHooks.Invoke(ctx, commandNames, func() error {
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
