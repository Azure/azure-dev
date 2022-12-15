package cmd

import (
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/spf13/cobra"
)

type CobraBuilder struct {
	container *ioc.NestedContainer
	runner    *middleware.MiddlewareRunner
}

func NewCobraBuilder(container *ioc.NestedContainer) *CobraBuilder {
	return &CobraBuilder{
		container: container,
		runner:    middleware.NewMiddlewareRunner(container),
	}
}

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
	// This ensures the command path is ready
	if descriptor.Parent() == nil {
		err := cb.bindCommand(cmd, descriptor)
		if err != nil {
			return nil, err
		}
	}

	cb.configureActionResolver(cmd, descriptor)

	return cmd, nil
}

func (cb *CobraBuilder) configureActionResolver(cmd *cobra.Command, descriptor *actions.ActionDescriptor) {
	// Only bind command to action if an action resolver had been defined
	// and when a RunE hasn't already been set
	if descriptor.Options.ActionResolver == nil || cmd.RunE != nil {
		return
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		ctx = tools.WithInstalledCheckCache(ctx)

		ioc.RegisterInstance(ioc.Global, ctx)
		ioc.RegisterInstance(ioc.Global, cmd)
		ioc.RegisterInstance(ioc.Global, args)

		err := cb.registerMiddleware(descriptor)
		if err != nil {
			return err
		}

		actionName := createActionName(cmd)
		var action actions.Action
		err = cb.container.ResolveNamed(actionName, &action)
		if err != nil {
			return fmt.Errorf(
				//nolint:lll
				"failed resolving action '%s'. Ensure the ActionResolver is a valid go function that returns an `actions.Action` interface, %w",
				actionName,
				err,
			)
		}

		runOptions := &middleware.Options{
			Name:    cmd.CommandPath(),
			Aliases: cmd.Aliases,
		}

		// Run the middleware chain with action
		_, err = cb.runner.RunAction(ctx, runOptions, action)

		return err
	}
}

func (cb *CobraBuilder) bindCommand(cmd *cobra.Command, descriptor *actions.ActionDescriptor) error {
	actionName := createActionName(cmd)

	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", descriptor.Name))

	if len(descriptor.Options.OutputFormats) > 0 {
		output.AddOutputParam(cmd, descriptor.Options.OutputFormats, descriptor.Options.DefaultFormat)
	}

	// Create, register and bind flags
	if descriptor.Options.FlagsResolver != nil {
		log.Printf("registering flags for action '%s'\n", actionName)
		ioc.RegisterInstance(cb.container, cmd)
		err := cb.container.RegisterSingletonAndInvoke(descriptor.Options.FlagsResolver)
		if err != nil {
			return fmt.Errorf(
				"failed registering FlagsResolver for action '%s'. Ensure the resolver is a valid go function. %w",
				actionName,
				err,
			)
		}
	}

	// Bind action resolvers
	if descriptor.Options.ActionResolver != nil {
		log.Printf("registering resolver for action '%s'\n", actionName)
		err := cb.container.RegisterNamedSingleton(actionName, descriptor.Options.ActionResolver)
		if err != nil {
			return fmt.Errorf(
				"failed registering ActionResolver for action'%s'. Ensure the resolver is a valid go function. %w",
				actionName,
				err,
			)
		}
	}

	// Bind flag completions
	for flag, completionFn := range descriptor.FlagCompletions() {
		if err := cmd.RegisterFlagCompletionFunc(flag, completionFn); err != nil {
			panic(err)
		}
	}

	// Bind the child commands
	for _, childDescriptor := range descriptor.Children() {
		childCmd := childDescriptor.Options.Command
		err := cb.bindCommand(childCmd, childDescriptor)
		if err != nil {
			return err
		}
	}

	return nil
}

func (cb *CobraBuilder) registerMiddleware(descriptor *actions.ActionDescriptor) error {
	chain := []*actions.MiddlewareRegistration{}
	current := descriptor

	for {
		middleware := current.Middleware()

		for i := len(middleware) - 1; i > -1; i-- {
			registration := middleware[i]

			// Only use the middleware when the predicate resolves truthy or if not defined
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
		err := cb.runner.Use(registration.Name, registration.Resolver)
		if err != nil {
			return err
		}
	}

	return nil
}

func createActionName(cmd *cobra.Command) string {
	actionName := cmd.CommandPath()
	actionName = strings.TrimSpace(actionName)
	actionName = strings.ReplaceAll(actionName, " ", "-")
	actionName = fmt.Sprintf("%s-action", actionName)

	return strings.ToLower(actionName)
}
