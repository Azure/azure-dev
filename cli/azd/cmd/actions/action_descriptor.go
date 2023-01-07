package actions

import (
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

// MiddlewareRegistration allows middleware components to be registered at any level within the command hierarchy
type MiddlewareRegistration struct {
	// The name of the middleware used for logging purposes
	Name string
	// The constructor/resolver used to create the middleware instance
	Resolver any
	// An optional predicate to control when this middleware is registered
	Predicate UseMiddlewareWhenPredicate
}

// Action descriptors consolidates the registration for a cobra command and related flags & actions
type ActionDescriptor struct {
	// Name of the descriptor (also used for command name if not specified in options)
	Name string
	// Descriptor options
	Options         *ActionDescriptorOptions
	parent          *ActionDescriptor
	children        []*ActionDescriptor
	middleware      []*MiddlewareRegistration
	flagCompletions map[string]FlagCompletionFunc
}

// Creates a new action descriptor
func NewActionDescriptor(name string, options *ActionDescriptorOptions) *ActionDescriptor {
	if options == nil {
		options = &ActionDescriptorOptions{}
	}

	if options.Command == nil {
		options.Command = &cobra.Command{
			Use: name,
		}
	}

	return &ActionDescriptor{
		Name:            name,
		Options:         options,
		middleware:      []*MiddlewareRegistration{},
		children:        []*ActionDescriptor{},
		flagCompletions: map[string]FlagCompletionFunc{},
	}
}

// Gets the child descriptors of the current instance
func (ad *ActionDescriptor) Children() []*ActionDescriptor {
	return ad.children
}

// Gets the parent descriptor of the current instance
func (ad *ActionDescriptor) Parent() *ActionDescriptor {
	return ad.parent
}

// Gets the middleware registrations for the current instance
func (ad *ActionDescriptor) Middleware() []*MiddlewareRegistration {
	return ad.middleware
}

// Gets the cobra command flag completion registrations for the current instance
func (ad *ActionDescriptor) FlagCompletions() map[string]FlagCompletionFunc {
	return ad.flagCompletions
}

// Adds a child action descriptor with the specified name and options
func (ad *ActionDescriptor) Add(name string, options *ActionDescriptorOptions) *ActionDescriptor {
	descriptor := NewActionDescriptor(name, options)
	descriptor.parent = ad
	ad.children = append(ad.children, descriptor)

	return descriptor
}

// Registers a middleware component to be run for this action and all child actions
func (ad *ActionDescriptor) UseMiddleware(name string, middlewareResolver any) *ActionDescriptor {
	ad.middleware = append(ad.middleware, &MiddlewareRegistration{
		Name:     name,
		Resolver: middlewareResolver,
	})

	return ad
}

// Registers a middleware component to be run for this action and all child actions
// when the specified predicate returns a truthy value
func (ad *ActionDescriptor) UseMiddlewareWhen(
	name string,
	middlewareResolver any,
	predicate UseMiddlewareWhenPredicate,
) *ActionDescriptor {
	ad.middleware = append(ad.middleware, &MiddlewareRegistration{
		Name:      name,
		Resolver:  middlewareResolver,
		Predicate: predicate,
	})

	return ad
}

// Registers a cobra flag completion for the specified flag
// Flags are lazily evaluated and cannot be registered inline within the options
func (ad *ActionDescriptor) AddFlagCompletion(flagName string, flagCompletionFn FlagCompletionFunc) *ActionDescriptor {
	ad.flagCompletions[flagName] = flagCompletionFn
	return ad
}

// Predicate function used to evaluate middleware registrations
type UseMiddlewareWhenPredicate func(descriptor *ActionDescriptor) bool

// ActionDescriptionOptions specifies all options for a given azd command and action
type ActionDescriptorOptions struct {
	// Cobra command configuration
	*cobra.Command
	// Function to resolve / create the flags instance required for the action
	FlagsResolver any
	// Function to resolve / create the action instance
	ActionResolver any
	// Array of support output formats
	OutputFormats []output.Format
	// The default output format if omitted in the command flags
	DefaultFormat output.Format
	// Whether or not telemetry should be disabled for the current action
	DisableTelemetry bool
}

// Completion function used for cobra command flag completion
type FlagCompletionFunc func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)
