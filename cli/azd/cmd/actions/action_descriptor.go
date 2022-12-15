package actions

import (
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

type MiddlewareRegistration struct {
	Name      string
	Resolver  any
	Predicate UseMiddlewareWhenPredicate
}

type ActionDescriptor struct {
	Name            string
	Options         *ActionDescriptorOptions
	parent          *ActionDescriptor
	children        []*ActionDescriptor
	middleware      []*MiddlewareRegistration
	flagCompletions map[string]FlagCompletionFunc
}

func NewActionDescriptor(name string, options *ActionDescriptorOptions) *ActionDescriptor {
	return &ActionDescriptor{
		Name:            name,
		Options:         options,
		middleware:      []*MiddlewareRegistration{},
		children:        []*ActionDescriptor{},
		flagCompletions: map[string]FlagCompletionFunc{},
	}
}

func (ad *ActionDescriptor) Children() []*ActionDescriptor {
	return ad.children
}

func (ad *ActionDescriptor) Parent() *ActionDescriptor {
	return ad.parent
}

func (ad *ActionDescriptor) Middleware() []*MiddlewareRegistration {
	return ad.middleware
}

func (ad *ActionDescriptor) FlagCompletions() map[string]FlagCompletionFunc {
	return ad.flagCompletions
}

func (ad *ActionDescriptor) Add(name string, options *ActionDescriptorOptions) *ActionDescriptor {
	descriptor := NewActionDescriptor(name, options)
	descriptor.parent = ad
	ad.children = append(ad.children, descriptor)

	return descriptor
}

func (ad *ActionDescriptor) UseMiddleware(name string, middlewareResolver any) *ActionDescriptor {
	ad.middleware = append(ad.middleware, &MiddlewareRegistration{
		Name:     name,
		Resolver: middlewareResolver,
	})

	return ad
}

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

func (ad *ActionDescriptor) AddFlagCompletion(flagName string, flagCompletionFn FlagCompletionFunc) *ActionDescriptor {
	ad.flagCompletions[flagName] = flagCompletionFn
	return ad
}

type UseMiddlewareWhenPredicate func(descriptor *ActionDescriptor) bool

type ActionDescriptorOptions struct {
	*cobra.Command
	FlagsResolver    any
	ActionResolver   any
	OutputFormats    []output.Format
	DefaultFormat    output.Format
	DisableTelemetry bool
}

type FlagCompletionFunc func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)
