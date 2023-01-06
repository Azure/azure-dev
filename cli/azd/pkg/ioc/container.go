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
	inner  container.Container
	parent *NestedContainer
}

// Creates a new nested container from the specified parent container
func NewNestedContainer(parent *NestedContainer) *NestedContainer {
	current := container.New()
	if parent != nil {
		// Copy the resolvers to the new container
		for key, value := range parent.inner {
			current[key] = value
		}
	}

	return &NestedContainer{
		inner:  current,
		parent: parent,
	}
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
func (c *NestedContainer) RegisterNamedSingleton(name string, resolveFn any) error {
	return c.inner.NamedSingletonLazy(name, resolveFn)
}

// Registers a resolver with a transient lifetime (instance per resolution)
// Returns an error if the resolver is not valid
func (c *NestedContainer) RegisterTransient(resolveFn any) error {
	return c.inner.TransientLazy(resolveFn)
}

// Registers a named resolver with a transient lifetime (instance per resolution)
// Returns an error if the resolver is not valid
func (c *NestedContainer) RegisterNamedTransient(name string, resolveFn any) error {
	return c.inner.NamedTransientLazy(name, resolveFn)
}

// Resolves an instance for the specified type
// Returns an error if the resolution fails
func (c *NestedContainer) Resolve(instance any) error {
	current := c
	for {
		err := current.inner.Resolve(instance)
		if err == nil {
			return nil
		}

		if current.parent == nil {
			return inspectResolveError(err)
		}
		current = current.parent
	}
}

// Resolves a named instance for the specified type
// Returns an error if the resolution fails
func (c *NestedContainer) ResolveNamed(name string, instance any) error {
	current := c
	for {
		err := current.inner.NamedResolve(instance, name)
		if err == nil {
			return nil
		}

		if current.parent == nil {
			return inspectResolveError(err)
		}
		current = current.parent
	}
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

// Inspects the specified error to determine whether the error is a
// developer container registration error or an error that was
// returned while instantiating a dependency.
func inspectResolveError(err error) error {
	if containerErrorRegex.Match([]byte(err.Error())) {
		return fmt.Errorf("%w: %s", ErrResolveInstance, err.Error())
	}

	return err
}
