// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

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
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

type HooksMiddleware struct {
	lazyEnvManager    *lazy.Lazy[environment.Manager]
	lazyEnv           *lazy.Lazy[*environment.Environment]
	lazyProjectConfig *lazy.Lazy[*project.ProjectConfig]
	importManager     *project.ImportManager
	commandRunner     exec.CommandRunner
	console           input.Console
	options           *Options
	serviceLocator    ioc.ServiceLocator
}

// Creates a new instance of the Hooks middleware
func NewHooksMiddleware(
	lazyEnvManager *lazy.Lazy[environment.Manager],
	lazyEnv *lazy.Lazy[*environment.Environment],
	lazyProjectConfig *lazy.Lazy[*project.ProjectConfig],
	importManager *project.ImportManager,
	commandRunner exec.CommandRunner,
	console input.Console,
	options *Options,
	serviceLocator ioc.ServiceLocator,
) Middleware {
	return &HooksMiddleware{
		lazyEnvManager:    lazyEnvManager,
		lazyEnv:           lazyEnv,
		lazyProjectConfig: lazyProjectConfig,
		importManager:     importManager,
		commandRunner:     commandRunner,
		console:           console,
		options:           options,
		serviceLocator:    serviceLocator,
	}
}

// Runs the Hooks middleware
func (m *HooksMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	env, err := m.lazyEnv.GetValue()
	if err != nil {
		log.Println("azd environment is not available, skipping all hook registrations.")
		return next(ctx)
	}

	projectConfig, err := m.lazyProjectConfig.GetValue()
	if err != nil || projectConfig == nil {
		log.Println("azd project is not available, skipping all hook registrations.")
		return next(ctx)
	}

	// Validate hooks and display any warnings
	if !m.options.IsChildAction(ctx) {
		if err := m.validateHooks(ctx, projectConfig); err != nil {
			return nil, fmt.Errorf("failed validating hooks, %w", err)
		}
	}

	if err := m.registerServiceHooks(ctx, env, projectConfig); err != nil {
		return nil, fmt.Errorf("failed registering service hooks, %w", err)
	}

	return m.registerCommandHooks(ctx, env, projectConfig, next)
}

// Register command level hooks for the executing cobra command & action
// Invokes the middleware next function
func (m *HooksMiddleware) registerCommandHooks(
	ctx context.Context,
	env *environment.Environment,
	projectConfig *project.ProjectConfig,
	next NextFn,
) (*actions.ActionResult, error) {
	if len(projectConfig.Hooks) == 0 {
		log.Println(
			"azd project is not available or does not contain any command hooks, skipping command hook registrations.",
		)
		return next(ctx)
	}

	envManager, err := m.lazyEnvManager.GetValue()
	if err != nil {
		return nil, fmt.Errorf("failed getting environment manager, %w", err)
	}

	hooksManager := ext.NewHooksManager(projectConfig.Path, m.commandRunner)
	hooksRunner := ext.NewHooksRunner(
		hooksManager,
		m.commandRunner,
		envManager,
		m.console,
		projectConfig.Path,
		projectConfig.Hooks,
		env,
		m.serviceLocator,
	)

	var actionResult *actions.ActionResult

	commandNames := []string{m.options.CommandPath}
	commandNames = append(commandNames, m.options.Aliases...)

	err = hooksRunner.Invoke(ctx, commandNames, func() error {
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
func (m *HooksMiddleware) registerServiceHooks(
	ctx context.Context,
	env *environment.Environment,
	projectConfig *project.ProjectConfig,
) error {
	envManager, err := m.lazyEnvManager.GetValue()
	if err != nil {
		return fmt.Errorf("failed getting environment manager, %w", err)
	}

	stableServices, err := m.importManager.ServiceStable(ctx, projectConfig)
	if err != nil {
		return fmt.Errorf("failed getting services: %w", err)
	}

	for _, service := range stableServices {
		serviceName := service.Name
		// If the service hasn't configured any hooks we can continue on.
		if len(service.Hooks) == 0 {
			log.Printf("service '%s' does not require any command hooks.\n", serviceName)
			continue
		}

		serviceHooksManager := ext.NewHooksManager(service.Path(), m.commandRunner)
		serviceHooksRunner := ext.NewHooksRunner(
			serviceHooksManager,
			m.commandRunner,
			envManager,
			m.console,
			service.Path(),
			service.Hooks,
			env,
			m.serviceLocator,
		)

		for hookName := range service.Hooks {
			hookType, eventName := ext.InferHookType(hookName)
			// If not a pre or post hook we can continue on.
			if hookType == ext.HookTypeNone {
				continue
			}

			if err := service.AddHandler(
				ext.Event(hookName),
				m.createServiceEventHandler(hookType, eventName, serviceHooksRunner),
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
	hookType ext.HookType,
	hookName string,
	hooksRunner *ext.HooksRunner,
) ext.EventHandlerFn[project.ServiceLifecycleEventArgs] {
	return func(ctx context.Context, eventArgs project.ServiceLifecycleEventArgs) error {
		return hooksRunner.RunHooks(ctx, hookType, nil, hookName)
	}
}

// validateHooks validates hook configurations and displays any warnings
func (m *HooksMiddleware) validateHooks(ctx context.Context, projectConfig *project.ProjectConfig) error {
	// Get service hooks for validation
	var serviceHooks []map[string][]*ext.HookConfig
	stableServices, err := m.importManager.ServiceStable(ctx, projectConfig)
	if err != nil {
		return fmt.Errorf("failed getting services for hook validation: %w", err)
	}

	for _, service := range stableServices {
		serviceHooks = append(serviceHooks, service.Hooks)
	}

	// Combine project and service hooks into a single map
	allHooks := make(map[string][]*ext.HookConfig)

	// Add project hooks
	for hookName, hookConfigs := range projectConfig.Hooks {
		allHooks[hookName] = append(allHooks[hookName], hookConfigs...)
	}

	// Add service hooks
	for _, serviceHookMap := range serviceHooks {
		for hookName, hookConfigs := range serviceHookMap {
			allHooks[hookName] = append(allHooks[hookName], hookConfigs...)
		}
	}

	// Create hooks manager and validate
	hooksManager := ext.NewHooksManager(projectConfig.Path, m.commandRunner)
	validationResult := hooksManager.ValidateHooks(ctx, allHooks)

	// Display any warnings
	for _, warning := range validationResult.Warnings {
		m.console.MessageUxItem(ctx, &ux.WarningMessage{
			Description: warning.Message,
		})
		m.console.Message(ctx, "")
	}

	return nil
}
