// This package wraps the golobby/container package to provide support for the following:
// 1. Easier usage of lazy type resolvers and ability to register specific type instances
// 2. Support for hierarchical/nested containers to resolve types from parent containers
// 3. Helper methods for easier/streamlined usage of of the IoC container
package ioc

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/golobby/container/v3"
)

var (
	// The golobby project does not support types errors,
	// but all the error messages are prefixed with `container:`
	containerErrorRegex *regexp.Regexp = regexp.MustCompile("container:")

	// The global/root level container
	Global *NestedContainer = &NestedContainer{
		inner:  container.Global,
		parent: nil,
	}

	ErrResolveInstance error = errors.New("failed resolving instance from container")
)

// NestedContainer is an IoC container that support nested containers
// Used for more complex registration scenarios such as scop based registration/resolution.
type NestedContainer struct {
	inner          container.Container
	parent         *NestedContainer
	scopedBindings []*binding
}

// Creates a new nested container from the specified parent container
func NewNestedContainer(parent *NestedContainer) *NestedContainer {
	current := container.New()
	if parent != nil {
		// Copy the bindings to the new container
		// The bindings hold the concrete instance of singleton registrations
		for key, value := range parent.inner {
			current[key] = value
		}
	}

	instance := &NestedContainer{
		inner:  current,
		parent: parent,
	}

	instance.RegisterScoped(func() ServiceLocator { return instance })

	return instance
}

// Registers a resolver with a singleton lifetime
// Panics if the resolver is not valid
func (c *NestedContainer) RegisterSingleton(resolveFn any) {
	container.MustSingletonLazy(c.inner, resolveFn)
}

// Registers a resolver with a singleton lifetime and instantiates the instance
// Instance is stored in container cache is used for future resolutions
// Returns an error if the resolver cannot instantiate the type
func (c *NestedContainer) RegisterSingletonAndInvoke(resolveFn any) error {
	return c.inner.Singleton(resolveFn)
}

// Registers a named resolver with a singleton lifetime
// Returns an error if the resolver is not valid
func (c *NestedContainer) RegisterNamedSingleton(name string, resolveFn any) {
	container.MustNamedSingletonLazy(c.inner, name, resolveFn)
}

// Registers a resolver with a transient lifetime (instance per resolution)
// Returns an error if the resolver is not valid
func (c *NestedContainer) RegisterTransient(resolveFn any) {
	container.MustTransientLazy(c.inner, resolveFn)
}

// Registers a named resolver with a transient lifetime (instance per resolution)
// Returns an error if the resolver is not valid
func (c *NestedContainer) RegisterNamedTransient(name string, resolveFn any) {
	container.MustNamedTransientLazy(c.inner, name, resolveFn)
}

// Registers a resolver with a scoped lifetime (instance per container)
// Scoped registrations are added as singletons in the current container then are reset in any new child containers
func (c *NestedContainer) RegisterScoped(resolveFn any) {
	container.MustSingletonLazy(c.inner, resolveFn)

	c.scopedBindings = append(c.scopedBindings, &binding{
		lifetime: Scoped,
		resolver: resolveFn,
	})
}

// Registers a named resolver with a scoped lifetime (instance per container)
// Scoped registrations are added as singletons in the current container then are reset in any new child containers
func (c *NestedContainer) RegisterNamedScoped(name string, resolveFn any) {
	container.MustNamedSingletonLazy(c.inner, name, resolveFn)

	c.scopedBindings = append(c.scopedBindings, &binding{
		name:     name,
		lifetime: Scoped,
		resolver: resolveFn,
	})
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

// Registers a constructed instance of the specified type
// Panics if the registration fails
func RegisterInstance[F any](c *NestedContainer, instance F) {
	container.MustSingletonLazy(c.inner, func() F {
		return instance
	})
}

// Registers a named constructed instance of the specified type
// Panics if the registration fails
func RegisterNamedInstance[F any](c *NestedContainer, name string, instance F) {
	container.MustNamedSingletonLazy(c.inner, name, func() F {
		return instance
	})
}

// NewScope creates a new nested container with a relationship back to the parent container
// Scope registrations are converted to singleton registrations within the new nested container.
func (c *NestedContainer) NewScope() *NestedContainer {
	childContainer := NewNestedContainer(c)

	for _, binding := range c.scopedBindings {
		if binding.name == "" {
			childContainer.RegisterSingleton(binding.resolver)
		} else {
			childContainer.RegisterNamedSingleton(binding.name, binding.resolver)
		}

		childContainer.scopedBindings = append(childContainer.scopedBindings, binding)
	}

	return childContainer
}

// Inspects the specified error to determine whether the error is a
// developer container registration error or an error that was
// returned while instantiating a dependency.
func inspectResolveError(err error) error {
	if containerErrorRegex.Match([]byte(err.Error())) {
		return fmt.Errorf("%w: %w", ErrResolveInstance, err)
	}

	return err
}
