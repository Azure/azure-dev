package cmd

import (
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/spf13/cobra"
)

const cDocsFlagName = "docs"

// CobraBuilder manages the construction of the cobra command tree from nested ActionDescriptors
type CobraBuilder struct {
	container *ioc.NestedContainer
}

// Creates a new instance of the Cobra builder
func NewCobraBuilder(container *ioc.NestedContainer) *CobraBuilder {
	return &CobraBuilder{
		container: container,
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
		// Register root go context that will be used for resolving singleton dependencies
		ctx := tools.WithInstalledCheckCache(cmd.Context())
		ioc.RegisterInstance(cb.container, ctx)

		// Create new container scope for the current command
		cmdContainer, err := cb.container.NewScope()
		if err != nil {
			return fmt.Errorf("failed creating new scope for command, %w", err)
		}

		// Registers the following to enable injection into actions that require them
		ioc.RegisterInstance(cmdContainer, ctx)
		ioc.RegisterInstance(cmdContainer, cmd)
		ioc.RegisterInstance(cmdContainer, args)
		ioc.RegisterInstance(cmdContainer, cmdContainer)
		ioc.RegisterInstance[ioc.ServiceLocator](cmdContainer, cmdContainer)

		// Register any required middleware registered for the current action descriptor
		middlewareRunner := middleware.NewMiddlewareRunner(cmdContainer)
		if err := cb.registerMiddleware(middlewareRunner, descriptor); err != nil {
			return err
		}

		runOptions := &middleware.Options{
			Name:        cmd.Name(),
			CommandPath: cmd.CommandPath(),
			Aliases:     cmd.Aliases,
			Flags:       cmd.Flags(),
			Args:        args,
		}

		// Set the container that should be used for resolving middleware components
		runOptions.WithContainer(cmdContainer)

		// Run the middleware chain with action
		actionName := createActionName(cmd)
		_, err = middlewareRunner.RunAction(ctx, runOptions, actionName)

		// At this point, we know that there might be an error, so we can silence cobra from showing it after us.
		cmd.SilenceErrors = true

		return err
	}

	return nil
}

// docsFlag is a flag with a custom parsing implementation which changes the default behavior for printing help
// for all commands, when it is set as true.
// docsFlag keeps a reference to the cobra command where it belongs so it can update it.
// docsFlag also contains a callbacks to pull dependencies for the docs routine.
type docsFlag struct {
	// reference to the command where the flag was added.
	command       *cobra.Command
	consoleFn     func() input.Console
	value         bool
	defaultHelpFn func(*cobra.Command, []string)
}

// returns the flag value
func (df *docsFlag) String() string {
	return fmt.Sprintf("%t", df.value)
}

// define flag type
func (df *docsFlag) Type() string {
	return "bool"
}

// Set not only initialize the flag value, but it also turns the help flag true and defines the HelpFunc for the command.
// This wiring forces cobra to react as it the --help flag was provided and stop the command early to run the HelpFunc.
func (df *docsFlag) Set(value string) error {
	v, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("invalid value for boolean --docs parameter")
	}
	df.value = v
	if !df.value {
		return nil
	}

	// Setting help to true will make cobra to stop and call the HelpFunc
	if err = df.command.Flag("help").Value.Set("true"); err != nil {
		// dev-issue: help flag should be already been added when
		log.Panic("tried to set help after docs parameter: %w", err)
	}

	// keeping the default help function allows to set --help with higher priority and use it
	// in case of finding --docs and --help
	df.defaultHelpFn = df.command.HelpFunc()

	// set help func for doing docs
	df.command.SetHelpFunc(func(c *cobra.Command, args []string) {
		console := df.consoleFn()
		ctx := c.Context()
		ctx = tools.WithInstalledCheckCache(ctx)

		if slices.Contains(args, "--help") {
			df.defaultHelpFn(c, args)
			return
		}

		commandPath := strings.ReplaceAll(c.CommandPath(), " ", "-")
		commandDocsUrl := cReferenceDocumentationUrl + commandPath
		openWithDefaultBrowser(ctx, console, commandDocsUrl)
	})

	return nil
}

// Binds the intersection of cobra command options and action descriptor options
func (cb *CobraBuilder) bindCommand(cmd *cobra.Command, descriptor *actions.ActionDescriptor) error {
	actionName := createActionName(cmd)

	// Automatically adds a consistent help flag
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))
	// docs flags for all commands
	docsFlag := &docsFlag{
		command: cmd,
		consoleFn: func() input.Console {
			var console input.Console
			if err := cb.container.Resolve(&console); err != nil {
				log.Panic("creating docs flag: %w", err)
			}
			return console
		},
	}
	flag := cmd.Flags().VarPF(
		docsFlag, cDocsFlagName, "", fmt.Sprintf("Opens the documentation for %s in your web browser.", cmd.CommandPath()))
	flag.NoOptDefVal = "true"

	// Consistently registers output formats for the descriptor
	if len(descriptor.Options.OutputFormats) > 0 {
		output.AddOutputParam(cmd, descriptor.Options.OutputFormats, descriptor.Options.DefaultFormat)
	}

	// Create, register and bind flags when required
	if descriptor.Options.FlagsResolver != nil {
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
		if err := cb.container.RegisterNamedTransient(actionName, descriptor.Options.ActionResolver); err != nil {
			return fmt.Errorf(
				//nolint:lll
				"failed registering ActionResolver for action '%s'. Ensure the resolver is a valid go function and resolves without error. %w",
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

	if descriptor.Options.GroupingOptions.RootLevelHelp != actions.CmdGroupNone {
		if cmd.Annotations == nil {
			cmd.Annotations = make(map[string]string)
		}
		actions.SetGroupCommandAnnotation(cmd, descriptor.Options.GroupingOptions.RootLevelHelp)
	}

	// `generateCmdHelp` sets a default help section when `descriptor.Options.HelpOptions` is nil.
	// This call ensures all commands gets the same help formatting.
	cmd.SetHelpTemplate(generateCmdHelp(cmd, generateCmdHelpOptions{
		Description: cmdHelpGenerator(descriptor.Options.HelpOptions.Description),
		Usage:       cmdHelpGenerator(descriptor.Options.HelpOptions.Usage),
		Commands:    cmdHelpGenerator(descriptor.Options.HelpOptions.Commands),
		Flags:       cmdHelpGenerator(descriptor.Options.HelpOptions.Flags),
		Footer:      cmdHelpGenerator(descriptor.Options.HelpOptions.Footer),
	}))

	return nil
}

// Registers all middleware components for the current command and any parent descriptors
// Middleware components are insure to run in the order that they were registered from the
// root registration, down through action groups and ultimately individual actions
func (cb *CobraBuilder) registerMiddleware(
	middlewareRunner *middleware.MiddlewareRunner,
	descriptor *actions.ActionDescriptor,
) error {
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
		if err := middlewareRunner.Use(registration.Name, registration.Resolver); err != nil {
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
