// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// This package wraps the golobby/container package to provide support for the following:
// 1. Easier usage of lazy type resolvers and ability to register specific type instances
// 2. Support for hierarchical/nested containers to resolve types from parent containers
// 3. Helper methods for easier/streamlined usage of of the IoC container
package ioc

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp"

	"github.com/golobby/container/v3"

	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
)

var (
	// The golobby project does not support types errors,
	// but all the error messages are prefixed with `container:`
	containerErrorRegex *regexp.Regexp = regexp.MustCompile("container:")

	ErrResolveInstance error = errors.New("failed resolving instance from container")
)

// NestedContainer is an IoC container supporting nested scopes and registration lifetimes.
//
// NOTE: Concurrent resolution and scope creation are safe only AFTER registrations are configured.
type NestedContainer struct {
	inner container.Container

	// bindings is our state tracking (not instances), on top of golobby -
	// we keep it up to date in [NestedContainer.register].
	bindings map[bindingKey]binding
}

func newEmptyContainer() *NestedContainer {
	return &NestedContainer{
		inner:    container.New(),
		bindings: map[bindingKey]binding{},
	}
}

// NewNestedContainer inherits registrations and cached singletons from the parent container.
func NewNestedContainer(parent *NestedContainer) *NestedContainer {
	instance := newEmptyContainer()

	if parent != nil {
		for registeredType, bindings := range parent.inner {
			// clone `bindings` as well, otherwise changes in child scopes will
			// actually modify the map for the parent!
			instance.inner[registeredType] = maps.Clone(bindings)
		}

		// instance.bindings is empty, so we're not overwriting any bindings
		maps.Copy(instance.bindings, parent.bindings)
	}

	RegisterInstance[ServiceLocator](instance, instance)

	return instance
}

// NewRegistrationsOnly replays the original registrations onto a new container.
// The new container does not share any cached instances with the container in `from`.
func NewRegistrationsOnly(from *NestedContainer) (*NestedContainer, error) {
	instance := newEmptyContainer()

	if from != nil {
		for key, registration := range from.bindings {
			if err := instance.register(key.name, registration, false); err != nil {
				return nil, err
			}
		}
	}

	RegisterInstance[ServiceLocator](instance, instance)

	return instance, nil
}

// register has two important responsibilities:
//  1. Keeps our [NestedContainer.bindings] map up to date, in order to make spawning clones
//     of our container ([NestedContainer.NewRegistrationsOnly]), or children (via [NestedContainer.NewScope])
//     accurate.
//  2. Register our type with golobby's [container.Container]
func (c *NestedContainer) register(name string, registration binding, invoke bool) error {
	resolver := reflect.ValueOf(registration.resolver)
	if !resolver.IsValid() || resolver.Kind() != reflect.Func || resolver.IsNil() {
		return errors.New("container: the resolver must be a non-nil function")
	}

	// Let golobby validate that the resolver function matches the required signature (we're about to wrap it)
	// - Function returns one (R) or two results (E, error)
	// - No direct self-dependency (ie, the function returns type R, but also requires type R as a parameter)
	if err := container.New().SingletonLazy(registration.resolver); err != nil {
		return err
	}

	if registration.lifetime != transientLifetime {
		// Singletons use a custom wrapping resolverFn. This ensures that no matter what level you resolve the singleton
		// at (ie, root scope, nested scopes) it'll always pull its dependencies from the _original_ level you registered
		// it at.
		//
		// Without this, lazy instantiations of singletons at different scopes would actually affect sibling or root usages
		// of the singleton, which is the exact opposite of what you'd want!
		resolver = c.singletonResolver(resolver)

		if invoke {
			// NOTE: we know this resolver now returns an (<typed return>, error) because we wrapped it
			// in singletonResolver above.
			if err, ok := resolver.Call(nil)[1].Interface().(error); ok && err != nil {
				return err
			}
		}
	}

	// Use transient registration so golobby doesn't cache results.
	// Our singleton wrapper caches successful results and lets failed resolutions retry.
	if err := c.inner.NamedTransientLazy(name, resolver.Interface()); err != nil {
		return err
	}

	c.bindings[bindingKey{
		// Get the type of the first return value for resolver, ie the type we're constructing.
		serviceType: reflect.TypeOf(registration.resolver).Out(0),
		name:        name,
	}] = registration
	return nil
}

// singletonResolver wraps a resolver so its dependencies always come from c, even when a child requests it.
// Singleton initialization and retrieval are synchronized; successful results are cached.
// A later resolution retries if resolving dependencies or invoking the resolver returns an error.
func (c *NestedContainer) singletonResolver(resolver reflect.Value) reflect.Value {
	resultType := resolver.Type().Out(0)
	errorType := reflect.TypeFor[error]()
	// Golobby sees func() (T, error): the original result type, but no arguments for a child scope to supply.
	wrapperType := reflect.FuncOf(nil, []reflect.Type{resultType, errorType}, false)

	lazyResolvedInstance := lazy.NewLazy(func() (reflect.Value, error) {
		// This receiver matches the original resolver's inputs, but captures its outputs in results.
		// Calling it through c.inner makes the registering container supply those inputs, not the requesting child.
		var results []reflect.Value
		receiver := newResolverReceiver(resolver, func(values []reflect.Value) {
			results = values
		})

		// this is the key bit - using c.inner, explicitly, means we'll resolve using the container instance at the
		// correct scope (ie, this container!)
		if err := c.inner.Call(receiver.Interface()); err != nil {
			return reflect.Value{}, err
		}

		if len(results) == 2 { // ie, (R, error)
			if err, ok := results[1].Interface().(error); ok && err != nil {
				return reflect.Value{}, err
			}
		}

		return results[0], nil
	})

	return reflect.MakeFunc(wrapperType, func(_ []reflect.Value) []reflect.Value {
		value, err := lazyResolvedInstance.GetValue()
		if err != nil {
			return []reflect.Value{reflect.Zero(resultType), reflect.ValueOf(err)}
		}
		return []reflect.Value{value, reflect.Zero(errorType)}
	})
}

// newResolverReceiver creates a function with the resolver's input types and no return values.
// Each call invokes the resolver and passes its results to capture.
func newResolverReceiver(resolver reflect.Value, capture func([]reflect.Value)) reflect.Value {
	resolverType := resolver.Type()

	// copy all the function params types
	funcParamsCount := resolverType.NumIn()
	funcParams := make([]reflect.Type, funcParamsCount)
	for index := range funcParamsCount {
		funcParams[index] = resolverType.In(index)
	}

	receiverType := reflect.FuncOf(funcParams, nil, false)

	return reflect.MakeFunc(receiverType, func(args []reflect.Value) []reflect.Value {
		// when this wrapper resolver is called, we'll call the inner resolver (ie, the _real_ creator of the value)
		// and pass all the args, whcih matched the original resolverFn's args.
		capture(resolver.Call(args))
		return nil
	})
}

// Fill takes a structure and resolves fields with the tag `container:"type" or `container:"name"`.
func (c *NestedContainer) Fill(structure any) error {
	return c.inner.Fill(structure)
}

// RegisterSingleton registers a lazy singleton whose dependencies are resolved in this container.
// It returns an error if the resolver is not valid.
//
// Singleton initialization is synchronized. Dependency cycles, including resolutions made inside the resolver,
// can deadlock. Resolvers must not wait for work that depends on their own initialization completing.
func (c *NestedContainer) RegisterSingleton(resolveFn any) error {
	return c.register("", binding{resolver: resolveFn, lifetime: singletonLifetime}, false)
}

// MustRegisterSingleton registers a lazy singleton and panics if the resolver is not valid.
// See [NestedContainer.RegisterSingleton] for resolver deadlock risks.
func (c *NestedContainer) MustRegisterSingleton(resolveFn any) {
	if err := c.RegisterSingleton(resolveFn); err != nil {
		panic(err)
	}
}

// RegisterSingletonAndInvoke registers and instantiates a singleton in this container.
// It returns an error if the resolver is invalid or construction fails.
// See [NestedContainer.RegisterSingleton] for resolver deadlock risks.
func (c *NestedContainer) RegisterSingletonAndInvoke(resolveFn any) error {
	return c.register("", binding{resolver: resolveFn, lifetime: singletonLifetime}, true)
}

// RegisterNamedSingleton registers a named lazy singleton whose dependencies are resolved in this container.
// It returns an error if the resolver is not valid.
// See [NestedContainer.RegisterSingleton] for resolver deadlock risks.
func (c *NestedContainer) RegisterNamedSingleton(name string, resolveFn any) error {
	return c.register(name, binding{resolver: resolveFn, lifetime: singletonLifetime}, false)
}

// MustRegisterNamedSingleton registers a named lazy singleton and panics if the resolver is not valid.
// See [NestedContainer.RegisterSingleton] for resolver deadlock risks.
func (c *NestedContainer) MustRegisterNamedSingleton(name string, resolveFn any) {
	if err := c.RegisterNamedSingleton(name, resolveFn); err != nil {
		panic(err)
	}
}

// RegisterTransient registers a constructor invoked for each resolution in the requesting container.
// It returns an error if the resolver is not valid.
func (c *NestedContainer) RegisterTransient(resolveFn any) error {
	return c.register("", binding{resolver: resolveFn, lifetime: transientLifetime}, false)
}

// MustRegisterTransient registers a transient constructor and panics if the resolver is not valid.
func (c *NestedContainer) MustRegisterTransient(resolveFn any) {
	if err := c.RegisterTransient(resolveFn); err != nil {
		panic(err)
	}
}

// RegisterNamedTransient registers a named constructor invoked for each resolution in the requesting container.
// It returns an error if the resolver is not valid.
func (c *NestedContainer) RegisterNamedTransient(name string, resolveFn any) error {
	return c.register(name, binding{resolver: resolveFn, lifetime: transientLifetime}, false)
}

// MustRegisterNamedTransient registers a named transient constructor and panics if the resolver is not valid.
func (c *NestedContainer) MustRegisterNamedTransient(name string, resolveFn any) {
	if err := c.RegisterNamedTransient(name, resolveFn); err != nil {
		panic(err)
	}
}

// RegisterScoped registers a singleton that is recreated by NewScope using the new scope's dependencies.
// It returns an error if the resolver is not valid.
func (c *NestedContainer) RegisterScoped(resolveFn any) error {
	return c.register("", binding{resolver: resolveFn, lifetime: scopedLifetime}, false)
}

// MustRegisterScoped registers a scoped constructor and panics if the resolver is not valid.
func (c *NestedContainer) MustRegisterScoped(resolveFn any) {
	if err := c.RegisterScoped(resolveFn); err != nil {
		panic(err)
	}
}

// RegisterNamedScoped registers a named singleton recreated by NewScope using the new scope's dependencies.
// It returns an error if the resolver is not valid.
func (c *NestedContainer) RegisterNamedScoped(name string, resolveFn any) error {
	return c.register(name, binding{resolver: resolveFn, lifetime: scopedLifetime}, false)
}

// MustRegisterNamedScoped registers a named scoped constructor and panics if the resolver is not valid.
func (c *NestedContainer) MustRegisterNamedScoped(name string, resolveFn any) {
	if err := c.RegisterNamedScoped(name, resolveFn); err != nil {
		panic(err)
	}
}

// Resolves an instance for the specified type
// Returns an error if the resolution fails
func (c *NestedContainer) Resolve(instance any) error {
	if err := c.inner.Resolve(instance); err != nil {
		return inspectResolveError(err)
	}

	return nil
}

// Resolves a named instance for the specified type
// Returns an error if the resolution fails
func (c *NestedContainer) ResolveNamed(name string, instance any) error {
	if err := c.inner.NamedResolve(instance, name); err != nil {
		return inspectResolveError(err)
	}

	return nil
}

// Invokes the specified function and resolves any arguments specified
// from the container resolver registrations
func (c *NestedContainer) Invoke(resolver any) error {
	return c.inner.Call(resolver)
}

// RegisterInstance registers an existing value, also reused by registrations-only clones.
// It panics if registration fails.
func RegisterInstance[F any](c *NestedContainer, instance F) {
	c.MustRegisterSingleton(func() F {
		return instance
	})
}

// RegisterNamedInstance registers a named existing value, also reused by registrations-only clones.
// It panics if registration fails.
func RegisterNamedInstance[F any](c *NestedContainer, name string, instance F) {
	c.MustRegisterNamedSingleton(name, func() F {
		return instance
	})
}

// NewScope creates a new nested container with a relationship back to the parent container
func (c *NestedContainer) NewScope() (*NestedContainer, error) {
	childContainer := NewNestedContainer(c)

	for key, registration := range c.bindings {
		if registration.lifetime != scopedLifetime {
			continue
		}
		if err := childContainer.register(key.name, registration, false); err != nil {
			return nil, err
		}
	}

	return childContainer, nil
}

// NewScopeRegistrationsOnly creates a scope with fresh singleton and scoped caches.
// Original constructors are rebound to the new scope, including those inherited from ancestors.
func (c *NestedContainer) NewScopeRegistrationsOnly() (*NestedContainer, error) {
	return NewRegistrationsOnly(c)
}

// Inspects the specified error to determine whether the error is a
// developer container registration error or an error that was
// returned while instantiating a dependency.
func inspectResolveError(err error) error {
	// Unwrap the current error
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		err = unwrapped
	}

	// If the unwrapped error is still a container error then return ErrResolveInstance
	if containerErrorRegex.Match([]byte(err.Error())) {
		return fmt.Errorf("%w: %w", ErrResolveInstance, err)
	}

	return err
}
