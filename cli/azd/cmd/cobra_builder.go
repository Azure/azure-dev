package cmd

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/spf13/cobra"
)

// CobraBuilder manages the construction of the cobra command tree from nested ActionDescriptors
type CobraBuilder struct {
	container *ioc.NestedContainer
	runner    *middleware.MiddlewareRunner
}

// Creates a new instance of the Cobra builder
func NewCobraBuilder(container *ioc.NestedContainer) *CobraBuilder {
	return &CobraBuilder{
		container: container,
		runner:    middleware.NewMiddlewareRunner(container),
	}
}

// Builds a cobra Command for the specified action descriptor
func (cb *CobraBuilder) BuildCommand(descriptor *actions.ActionDescriptor) (*cobra.Command, error) {
	cmd := descriptor.Options.Command
	if cmd.Use == "" {
		cmd.Use = descriptor.Name
	}

	// Build the full command tree
	for _, childDescriptor := range descriptor.Children() {
		childCmd, err := cb.BuildCommand(childDescriptor)
		if err != nil {
			return nil, err
		}

		cmd.AddCommand(childCmd)
	}

	// Bind root command after command tree has been established
	// This ensures the command path is ready and consistent across all nested commands
	if descriptor.Parent() == nil {
		if err := cb.bindCommand(cmd, descriptor); err != nil {
			return nil, err
		}
	}

	// Configure action resolver for leaf commands
	if !cmd.HasSubCommands() {
		if err := cb.configureActionResolver(cmd, descriptor); err != nil {
			return nil, err
		}
	}

	return cmd, nil
}

// Configures the cobra command 'RunE' function to running the composed middleware and action for the
// current action descriptor
func (cb *CobraBuilder) configureActionResolver(cmd *cobra.Command, descriptor *actions.ActionDescriptor) error {
	// Dev Error: Either an action resolver or RunE must be set
	if descriptor.Options.ActionResolver == nil && cmd.RunE == nil {
		return fmt.Errorf(
			//nolint:lll
			"action descriptor for '%s' must be configured with either an ActionResolver or a Cobra RunE command",
			cmd.CommandPath(),
		)
	}

	// Dev Error: Both action resolver and RunE have been defined
	if descriptor.Options.ActionResolver != nil && cmd.RunE != nil {
		return fmt.Errorf(
			//nolint:lll
			"action descriptor for '%s' must be configured with either an ActionResolver or a Cobra RunE command but NOT both",
			cmd.CommandPath(),
		)
	}

	// Only bind command to action if an action resolver had been defined
	// and when a RunE hasn't already been set
	if descriptor.Options.ActionResolver == nil || cmd.RunE != nil {
		return nil
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		ctx = tools.WithInstalledCheckCache(ctx)

		// Registers the following to enable injection into actions that require them
		ioc.RegisterInstance(ioc.Global, cb.runner)
		ioc.RegisterInstance(ioc.Global, middleware.MiddlewareContext(cb.runner))
		ioc.RegisterInstance(ioc.Global, ctx)
		ioc.RegisterInstance(ioc.Global, cmd)
		ioc.RegisterInstance(ioc.Global, args)

		if err := cb.registerMiddleware(descriptor); err != nil {
			return err
		}

		actionName := createActionName(cmd)
		var action actions.Action
		if err := cb.container.ResolveNamed(actionName, &action); err != nil {
			if errors.Is(err, ioc.ErrResolveInstance) {
				return fmt.Errorf(
					//nolint:lll
					"failed resolving action '%s'. Ensure the ActionResolver is a valid go function that returns an `actions.Action` interface, %w",
					actionName,
					err,
				)
			}

			return err
		}

		runOptions := &middleware.Options{
			Name:        cmd.Name(),
			CommandPath: cmd.CommandPath(),
			Aliases:     cmd.Aliases,
		}

		// Run the middleware chain with action
		log.Printf("Resolved action '%s'\n", actionName)
		actionResult, err := cb.runner.RunAction(ctx, runOptions, action)

		// At this point, we know that there might be an error, so we can silence cobra from showing it after us.
		cmd.SilenceErrors = true

		// TODO: Consider refactoring to move the UX writing to a middleware
		invokeErr := ioc.Global.Invoke(func(console input.Console) {
			// It is valid for a command to return a nil action result and error.
			// If we have a result or an error, display it, otherwise don't print anything.
			if actionResult != nil || err != nil {
				console.MessageUxItem(ctx, actions.ToUxItem(actionResult, err))
			}
		})

		if invokeErr != nil {
			return invokeErr
		}

		return err
	}

	return nil
}

// Binds the intersection of cobra command options and action descriptor options
func (cb *CobraBuilder) bindCommand(cmd *cobra.Command, descriptor *actions.ActionDescriptor) error {
	actionName := createActionName(cmd)

	// Automatically adds a consistent help flag
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))

	// Consistently registers output formats for the descriptor
	if len(descriptor.Options.OutputFormats) > 0 {
		output.AddOutputParam(cmd, descriptor.Options.OutputFormats, descriptor.Options.DefaultFormat)
	}

	// Create, register and bind flags when required
	if descriptor.Options.FlagsResolver != nil {
		log.Printf("registering flags for action '%s'\n", actionName)
		ioc.RegisterInstance(cb.container, cmd)

		// The flags resolver is constructed and bound to the cobra command via dependency injection
		// This allows flags to be options and support any set of required dependencies
		if err := cb.container.RegisterSingletonAndInvoke(descriptor.Options.FlagsResolver); err != nil {
			return fmt.Errorf(
				//nolint:lll
				"failed registering FlagsResolver for action '%s'. Ensure the resolver is a valid go function and resolves without error. %w",
				actionName,
				err,
			)
		}
	}

	// Registers and bind action resolves when required
	// Action resolvers are essential go functions that create the instance of the required actions.Action
	// These functions are typically the constructor function for the action. ex) newDeployAction(...)
	// Action resolvers can take any number of dependencies and instantiated via the IoC container
	if descriptor.Options.ActionResolver != nil {
		log.Printf("registering resolver for action '%s'\n", actionName)
		if err := cb.container.RegisterNamedSingleton(actionName, descriptor.Options.ActionResolver); err != nil {
			return fmt.Errorf(
				//nolint:lll
				"failed registering ActionResolver for action'%s'. Ensure the resolver is a valid go function and resolves without error. %w",
				actionName,
				err,
			)
		}
	}

	// Bind flag completions
	// Since flags are lazily loaded we need to wait until after command flags are wired up before
	// any flag completion functions are registered
	for flag, completionFn := range descriptor.FlagCompletions() {
		if err := cmd.RegisterFlagCompletionFunc(flag, completionFn); err != nil {
			return fmt.Errorf("failed registering flag completion function for '%s', %w", flag, err)
		}
	}

	// Bind the child commands for the current descriptor
	for _, childDescriptor := range descriptor.Children() {
		childCmd := childDescriptor.Options.Command
		if err := cb.bindCommand(childCmd, childDescriptor); err != nil {
			return err
		}
	}

	return nil
}

// Registers all middleware components for the current command and any parent descriptors
// Middleware components are insure to run in the order that they were registered from the
// root registration, down through action groups and ultimately individual actions
func (cb *CobraBuilder) registerMiddleware(descriptor *actions.ActionDescriptor) error {
	chain := []*actions.MiddlewareRegistration{}
	current := descriptor

	// Recursively loop through any action describer and their parents
	for {
		middleware := current.Middleware()

		for i := len(middleware) - 1; i > -1; i-- {
			registration := middleware[i]

			// Only use the middleware when the predicate resolves truthy or if not defined
			// Registration predicates are useful for when you want to selectively want to
			// register a middleware based on the descriptor options
			// Ex) Telemetry middleware registered for all actions except 'version'
			if registration.Predicate == nil || registration.Predicate(descriptor) {
				chain = append(chain, middleware[i])
			}
		}

		if current.Parent() == nil {
			break
		}

		current = current.Parent()
	}

	// Register middleware in reverse order so middleware registered
	// higher up the command structure are resolved before lower registrations
	for i := len(chain) - 1; i > -1; i-- {
		registration := chain[i]
		if err := cb.runner.Use(registration.Name, registration.Resolver); err != nil {
			return err
		}
	}

	return nil
}

// Composes a consistent action name for the specified cobra command
// ex) azd config list becomes 'azd-config-list-action'
func createActionName(cmd *cobra.Command) string {
	actionName := cmd.CommandPath()
	actionName = strings.TrimSpace(actionName)
	actionName = strings.ReplaceAll(actionName, " ", "-")
	actionName = fmt.Sprintf("%s-action", actionName)

	return strings.ToLower(actionName)
}
