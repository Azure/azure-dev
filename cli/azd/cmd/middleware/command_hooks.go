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
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func UseCommandHooks() actions.MiddlewareFn {
	return func(ctx context.Context, options *actions.ActionOptions, next actions.NextFn) (*actions.ActionResult, error) {
		azdContext, err := azdcontext.NewAzdContext()
		if err != nil {
			log.Printf("failing creating AzdContext for command hooks, %s", err.Error())
			return nil, err
		}

		commandOptions := internal.GetCommandOptions(ctx)
		environmentName := &commandOptions.EnvironmentName
		if *environmentName == "" {
			*environmentName, err = azdContext.GetDefaultEnvironmentName()
			if err != nil {
				log.Printf(
					"failing retrieving default environment name for command hooks. Command hooks will not run, %s\n",
					err.Error(),
				)
				return next(ctx)
			}
		}

		env, err := environment.GetEnvironment(azdContext, *environmentName)
		if err != nil {
			log.Printf("failing loading environment for command hooks. Command hooks will not run, %s\n", err.Error())
			return next(ctx)
		}

		projectConfig, err := project.LoadProjectConfig(azdContext.ProjectPath(), env)
		if err != nil {
			log.Printf("failing loading project for command hooks. Command hooks will not run, %s\n", err.Error())
			return next(ctx)
		}

		if projectConfig.Scripts == nil || len(projectConfig.Scripts) == 0 {
			log.Println("project does not contain any command hooks.")
			return next(ctx)
		}

		commandRunner := exec.GetCommandRunner(ctx)
		console := input.GetConsole(ctx)
		formatter := console.GetFormatter()
		interactive := formatter == nil || formatter.Kind() == output.NoneFormat
		hooks := ext.NewCommandHooks(
			commandRunner,
			console,
			projectConfig.Scripts,
			azdContext.ProjectDirectory(),
			env.ToSlice(),
			interactive,
		)

		var actionResult *actions.ActionResult

		err = hooks.InvokeAction(ctx, options.Name, func() error {
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
}
