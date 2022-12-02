package middleware

import (
	"context"
	"log"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

type CommandHooksMiddleware struct {
	azdContext     *azdcontext.AzdContext
	commandOptions *internal.GlobalCommandOptions
	actionOptions  *actions.ActionOptions
	console        input.Console
	commandRunner  exec.CommandRunner
}

func NewCommandHooksMiddleware(
	azdContext *azdcontext.AzdContext,
	console input.Console,
	commandRunner exec.CommandRunner,
	actionOptions *actions.ActionOptions,
	commandOptions *internal.GlobalCommandOptions,
) *CommandHooksMiddleware {
	return &CommandHooksMiddleware{
		azdContext:     azdContext,
		console:        console,
		commandRunner:  commandRunner,
		actionOptions:  actionOptions,
		commandOptions: commandOptions,
	}
}

func (m *CommandHooksMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	environmentName := &m.commandOptions.EnvironmentName
	var err error

	if *environmentName == "" {
		*environmentName, err = m.azdContext.GetDefaultEnvironmentName()
		if err != nil {
			log.Printf(
				"failing retrieving default environment name for command hooks. Command hooks will not run, %s\n",
				err.Error(),
			)
			return next(ctx)
		}
	}

	env, err := environment.GetEnvironment(m.azdContext, *environmentName)
	if err != nil {
		log.Printf("failing loading environment for command hooks. Command hooks will not run, %s\n", err.Error())
		return next(ctx)
	}

	projectConfig, err := project.LoadProjectConfig(m.azdContext.ProjectPath(), env)
	if err != nil {
		log.Printf("failing loading project for command hooks. Command hooks will not run, %s\n", err.Error())
		return next(ctx)
	}

	if projectConfig.Scripts == nil || len(projectConfig.Scripts) == 0 {
		log.Println("project does not contain any command hooks.")
		return next(ctx)
	}

	hooks := ext.NewCommandHooks(
		m.commandRunner,
		m.console,
		projectConfig.Scripts,
		m.azdContext.ProjectDirectory(),
		env.ToSlice(),
	)

	var actionResult *actions.ActionResult

	err = hooks.InvokeAction(ctx, m.actionOptions.Name, func() error {
		actionResult, err = next(ctx)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return actionResult, nil
}
