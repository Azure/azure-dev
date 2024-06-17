package middleware

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

type HooksMiddleware struct {
	envManager    environment.Manager
	env           *environment.Environment
	projectConfig *project.ProjectConfig
	importManager *project.ImportManager
	commandRunner exec.CommandRunner
	console       input.Console
	options       *Options
}

// Creates a new instance of the Hooks middleware
func NewHooksMiddleware(
	envManager environment.Manager,
	env *environment.Environment,
	projectConfig *project.ProjectConfig,
	importManager *project.ImportManager,
	commandRunner exec.CommandRunner,
	console input.Console,
	options *Options,
) Middleware {
	return &HooksMiddleware{
		envManager:    envManager,
		env:           env,
		projectConfig: projectConfig,
		importManager: importManager,
		commandRunner: commandRunner,
		console:       console,
		options:       options,
	}
}

// Runs the Hooks middleware
func (m *HooksMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	if err := m.registerServiceHooks(ctx); err != nil {
		return nil, fmt.Errorf("failed registering service hooks, %w", err)
	}

	return m.registerCommandHooks(ctx, next)
}

// Register command level hooks for the executing cobra command & action
// Invokes the middleware next function
func (m *HooksMiddleware) registerCommandHooks(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	if m.projectConfig.Hooks == nil || len(m.projectConfig.Hooks) == 0 {
		log.Println(
			"azd project does not contain any command hooks, skipping command hook registrations.",
		)
		return next(ctx)
	}

	hooksManager := ext.NewHooksManager(m.projectConfig.Path)
	hooksRunner := ext.NewHooksRunner(
		hooksManager,
		m.commandRunner,
		m.envManager,
		m.console,
		m.projectConfig.Path,
		m.projectConfig.Hooks,
		m.env,
	)

	var actionResult *actions.ActionResult

	commandNames := []string{m.options.CommandPath}
	commandNames = append(commandNames, m.options.Aliases...)

	err := hooksRunner.Invoke(ctx, commandNames, func() error {
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

// Registers event handlers for all services within the project configuration
// Runs hooks for each matching event handler
func (m *HooksMiddleware) registerServiceHooks(ctx context.Context) error {
	stableServices, err := m.importManager.ServiceStable(ctx, m.projectConfig)
	if err != nil {
		return fmt.Errorf("failed getting services: %w", err)
	}

	for _, service := range stableServices {
		serviceName := service.Name
		// If the service hasn't configured any hooks we can continue on.
		if service.Hooks == nil || len(service.Hooks) == 0 {
			log.Printf("service '%s' does not require any command hooks.\n", serviceName)
			continue
		}

		serviceHooksManager := ext.NewHooksManager(service.Path())
		serviceHooksRunner := ext.NewHooksRunner(
			serviceHooksManager,
			m.commandRunner,
			m.envManager,
			m.console,
			service.Path(),
			service.Hooks,
			m.env,
		)

		for hookName := range service.Hooks {
			hookType, eventName := ext.InferHookType(hookName)
			// If not a pre or post hook we can continue on.
			if hookType == ext.HookTypeNone {
				continue
			}

			if err := service.AddHandler(
				ext.Event(hookName),
				m.createServiceEventHandler(ctx, hookType, eventName, serviceHooksRunner),
			); err != nil {
				return fmt.Errorf(
					"failed registering event handler for service '%s' and event '%s', %w",
					serviceName,
					hookName,
					err,
				)
			}
		}
	}

	return nil
}

// Creates an event handler for the specified service config and event name
func (m *HooksMiddleware) createServiceEventHandler(
	ctx context.Context,
	hookType ext.HookType,
	hookName string,
	hooksRunner *ext.HooksRunner,
) ext.EventHandlerFn[project.ServiceLifecycleEventArgs] {
	return func(ctx context.Context, eventArgs project.ServiceLifecycleEventArgs) error {
		return hooksRunner.RunHooks(ctx, hookType, nil, hookName)
	}
}
