package middleware

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

type contextKey string

var serviceHooksRegisteredContextKey contextKey = "service-hooks-registered"

type HooksMiddleware struct {
	lazyEnvManager    *lazy.Lazy[environment.Manager]
	lazyEnv           *lazy.Lazy[*environment.Environment]
	lazyProjectConfig *lazy.Lazy[*project.ProjectConfig]
	commandRunner     exec.CommandRunner
	console           input.Console
	options           *Options
}

// Creates a new instance of the Hooks middleware
func NewHooksMiddleware(
	lazyEnvManager *lazy.Lazy[environment.Manager],
	lazyEnv *lazy.Lazy[*environment.Environment],
	lazyProjectConfig *lazy.Lazy[*project.ProjectConfig],
	commandRunner exec.CommandRunner,
	console input.Console,
	options *Options,
) Middleware {
	return &HooksMiddleware{
		lazyEnvManager:    lazyEnvManager,
		lazyEnv:           lazyEnv,
		lazyProjectConfig: lazyProjectConfig,
		commandRunner:     commandRunner,
		console:           console,
		options:           options,
	}
}

// Runs the Hooks middleware
func (m *HooksMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	ctx, _ = getServiceHooksRegistered(ctx)

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
	if projectConfig.Hooks == nil || len(projectConfig.Hooks) == 0 {
		log.Println(
			"azd project is not available or does not contain any command hooks, skipping command hook registrations.",
		)
		return next(ctx)
	}

	envManager, err := m.lazyEnvManager.GetValue()
	if err != nil {
		return nil, fmt.Errorf("failed getting environment manager, %w", err)
	}

	hooksManager := ext.NewHooksManager(projectConfig.Path)
	hooksRunner := ext.NewHooksRunner(
		hooksManager,
		m.commandRunner,
		envManager,
		m.console,
		projectConfig.Path,
		projectConfig.Hooks,
		env,
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
	// Check if service hooks have already been registered higher up the chain
	ctx, serviceHooksRegistered := getServiceHooksRegistered(ctx)
	if *serviceHooksRegistered {
		return nil
	}

	envManager, err := m.lazyEnvManager.GetValue()
	if err != nil {
		return fmt.Errorf("failed getting environment manager, %w", err)
	}

	for serviceName, service := range projectConfig.Services {
		// If the service hasn't configured any hooks we can continue on.
		if service.Hooks == nil || len(service.Hooks) == 0 {
			log.Printf("service '%s' does not require any command hooks.\n", serviceName)
			continue
		}

		serviceHooksManager := ext.NewHooksManager(service.Path())
		serviceHooksRunner := ext.NewHooksRunner(
			serviceHooksManager,
			m.commandRunner,
			envManager,
			m.console,
			service.Path(),
			service.Hooks,
			env,
		)

		for hookName, hookConfig := range service.Hooks {
			hookType, eventName, err := inferHookType(hookName, hookConfig)
			if err != nil {
				return fmt.Errorf(
					//nolint:lll
					"%w for service '%s'. Hooks must start with 'pre' or 'post' and end in a valid service event name. Examples: restore, package, deploy",
					err,
					serviceName,
				)
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

	// Set context value that the service hooks have been registered
	*serviceHooksRegistered = true

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
		return hooksRunner.RunHooks(ctx, hookType, hookName)
	}
}

func inferHookType(name string, config *ext.HookConfig) (ext.HookType, string, error) {
	// Validate name length so go doesn't PANIC for string slicing below
	if len(name) < 4 {
		return "", "", fmt.Errorf("unable to infer hook '%s'", name)
	} else if name[:3] == "pre" {
		return ext.HookTypePre, name[3:], nil
	} else if name[:4] == "post" {
		return ext.HookTypePost, name[4:], nil
	}

	return "", "", fmt.Errorf("unable to infer hook '%s'", name)
}

// Gets a value that returns whether or not service hooks have already been registered
// for the current project config
// Optionally constructs a new go context that stores a pointer to this value
func getServiceHooksRegistered(ctx context.Context) (context.Context, *bool) {
	serviceHooksRegistered, ok := ctx.Value(serviceHooksRegisteredContextKey).(*bool)
	if !ok {
		serviceHooksRegistered = convert.RefOf(false)
		ctx = context.WithValue(ctx, serviceHooksRegisteredContextKey, serviceHooksRegistered)
	}

	return ctx, serviceHooksRegistered
}
