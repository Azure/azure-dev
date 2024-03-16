// This package wraps the golobby/container package to provide support for the following:
// 1. Easier usage of lazy type resolvers and ability to register specific type instances
// 2. Support for hierarchical/nested containers to resolve types from parent containers
// 3. Helper methods for easier/streamlined usage of of the IoC container
package ioc

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"unsafe"

	"github.com/golobby/container/v3"
)

var (
	// The golobby project does not support types errors,
	// but all the error messages are prefixed with `container:`
	containerErrorRegex *regexp.Regexp = regexp.MustCompile("container:")

	ErrResolveInstance error = errors.New("failed resolving instance from container")
)

// NestedContainer is an IoC container that support nested containers
// Used for more complex registration scenarios such as scop based registration/resolution.
type NestedContainer struct {
	inner          container.Container
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
		inner: current,
	}

	RegisterInstance[ServiceLocator](instance, instance)

	return instance
}

// Creates a new container with only registrations from the given container.
func NewRegistrationsOnly(from *NestedContainer) *NestedContainer {
	current := container.New()

	if from != nil {
		// Reset all concrete instances by copying 'resolver' and 'isSingleton' fields
		// Reflection is necessary since *container.binding is unexported
		for key, value := range from.inner {
			var valueType = reflect.TypeOf(value)
			newValue := reflect.MakeMapWithSize(valueType, len(value))

			for name, binding := range value {
				bindingVal := reflect.ValueOf(binding).Elem()

				newBinding := reflect.New(reflect.TypeOf(binding).Elem())
				setUnexportedField(
					newBinding.Elem().FieldByName("resolver"),
					getUnexportedField(bindingVal.FieldByName("resolver")))
				setUnexportedField(
					newBinding.Elem().FieldByName("isSingleton"),
					getUnexportedField(bindingVal.FieldByName("isSingleton")))

				n := name
				newValue.SetMapIndex(reflect.ValueOf(n), newBinding)
			}

			reflect.ValueOf(current).SetMapIndex(reflect.ValueOf(key), newValue)
		}
	}

	instance := &NestedContainer{
		inner: current,
	}

	RegisterInstance[ServiceLocator](instance, instance)

	return instance
}

func getUnexportedField(field reflect.Value) interface{} {
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface()
}

func setUnexportedField(field reflect.Value, value interface{}) {
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).
		Elem().
		Set(reflect.ValueOf(value))
}

// Fill takes a structure and resolves fields with the tag `container:"type" or `container:"name"`.
func (c *NestedContainer) Fill(structure any) error {
	return c.inner.Fill(structure)
}

// Registers a resolver with a singleton lifetime
// Returns an error if the resolver is not valid
func (c *NestedContainer) RegisterSingleton(resolveFn any) error {
	return c.inner.SingletonLazy(resolveFn)
}

// Registers a resolver with a singleton lifetime
// Panics if the resolver is not valid
func (c *NestedContainer) MustRegisterSingleton(resolveFn any) {
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

// Registers a named resolver with a singleton lifetime
// Panics if the resolver is not valid
func (c *NestedContainer) MustRegisterNamedSingleton(name string, resolveFn any) {
	container.MustNamedSingletonLazy(c.inner, name, resolveFn)
}

// Registers a resolver with a transient lifetime (instance per resolution)
// Returns an error if the resolver is not valid
func (c *NestedContainer) RegisterTransient(resolveFn any) error {
	return c.inner.TransientLazy(resolveFn)
}

// Registers a named resolver with a singleton lifetime and instantiates the instance
// Panics if the resolver is not valid
func (c *NestedContainer) MustRegisterTransient(resolveFn any) {
	container.MustTransientLazy(c.inner, resolveFn)
}

// Registers a named resolver with a transient lifetime (instance per resolution)
// Returns an error if the resolver is not valid
func (c *NestedContainer) RegisterNamedTransient(name string, resolveFn any) error {
	return c.inner.NamedTransientLazy(name, resolveFn)
}

// Registers a named resolver with a transient lifetime (instance per resolution)
// Panics if the resolver is not valid
func (c *NestedContainer) MustRegisterNamedTransient(name string, resolveFn any) {
	container.MustNamedTransientLazy(c.inner, name, resolveFn)
}

// Registers a resolver with a scoped lifetime (instance per scope)
// Ex: Each new cobra command will create a new scope
// Scoped registrations are added as singletons in the current container then are reset in any new child containers
// Returns an error if the resolver is not valid
func (c *NestedContainer) RegisterScoped(resolveFn any) error {
	if err := c.inner.SingletonLazy(resolveFn); err != nil {
		return err
	}

	c.scopedBindings = append(c.scopedBindings, &binding{
		resolver: resolveFn,
	})

	return nil
}

// Registers a resolver with a scoped lifetime (instance per scope)
// Ex: Each new cobra command will create a new scope
// Scoped registrations are added as singletons in the current container then are reset in any new child containers
// Panics if the resolver is not valid
func (c *NestedContainer) MustRegisterScoped(resolveFn any) {
	if err := c.RegisterScoped(resolveFn); err != nil {
		panic(err)
	}
}

// Registers a named resolver with a scoped lifetime (instance per scope)
// Ex: Each new cobra command will create a new scope
// Scoped registrations are added as singletons in the current container then are reset in any new child containers
func (c *NestedContainer) RegisterNamedScoped(name string, resolveFn any) error {
	if err := c.inner.NamedSingletonLazy(name, resolveFn); err != nil {
		return err
	}

	c.scopedBindings = append(c.scopedBindings, &binding{
		name:     name,
		resolver: resolveFn,
	})

	return nil
}

// Registers a named resolver with a scoped lifetime (instance per scope)
// Ex: Each new cobra command will create a new scope
// Scoped registrations are added as singletons in the current container then are reset in any new child containers
// Panics if the resolver is not valid
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
// Scoped registrations are converted to singleton registrations within the new nested container.
func (c *NestedContainer) NewScope() (*NestedContainer, error) {
	childContainer := NewNestedContainer(c)

	for _, binding := range c.scopedBindings {
		if binding.name == "" {
			if err := childContainer.RegisterSingleton(binding.resolver); err != nil {
				return nil, err
			}
		} else {
			if err := childContainer.RegisterNamedSingleton(binding.name, binding.resolver); err != nil {
				return nil, err
			}
		}
		childContainer.scopedBindings = append(childContainer.scopedBindings, binding)
	}

	return childContainer, nil
}

// NewScopeRegistrationsOnly creates a new container with bindings deep copied from the container.
// Scoped registrations are then activated as singletons within the new nested container.
func (c *NestedContainer) NewScopeRegistrationsOnly() (*NestedContainer, error) {
	childContainer := NewRegistrationsOnly(c)

	for _, binding := range c.scopedBindings {
		if binding.name == "" {
			if err := childContainer.RegisterSingleton(binding.resolver); err != nil {
				return nil, err
			}
		} else {
			if err := childContainer.RegisterNamedSingleton(binding.name, binding.resolver); err != nil {
				return nil, err
			}
		}
		childContainer.scopedBindings = append(childContainer.scopedBindings, binding)
	}

	return childContainer, nil
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
