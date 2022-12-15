package ioc

import (
	"github.com/golobby/container/v3"
)

var Global *NestedContainer = &NestedContainer{
	inner:  container.Global,
	parent: nil,
}

type NestedContainer struct {
	inner  container.Container
	parent *NestedContainer
}

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

func (c *NestedContainer) RegisterSingleton(resolveFn any) {
	container.MustSingletonLazy(c.inner, resolveFn)
}

func (c *NestedContainer) RegisterSingletonAndInvoke(resolveFn any) error {
	return c.inner.Singleton(resolveFn)
}

func (c *NestedContainer) RegisterNamedSingleton(name string, resolveFn any) error {
	return c.inner.NamedSingletonLazy(name, resolveFn)
}

func (c *NestedContainer) RegisterTransient(resolveFn any) {
	container.MustTransientLazy(c.inner, resolveFn)
}

func (c *NestedContainer) RegisterNamedTransient(name string, resolveFn any) error {
	return c.inner.NamedTransientLazy(name, resolveFn)
}

func RegisterInstance[F any](c *NestedContainer, instance F) {
	container.MustSingletonLazy(c.inner, func() F {
		return instance
	})
}

func RegisterNamedInstance[F any](c *NestedContainer, name string, instance F) {
	container.MustNamedSingletonLazy(c.inner, name, func() F {
		return instance
	})
}

func (c *NestedContainer) Resolve(instance any) error {
	current := c
	for {
		err := current.inner.Resolve(instance)
		if err == nil {
			return nil
		}

		if current.parent == nil {
			return err
		}
		current = current.parent
	}
}

func (c *NestedContainer) ResolveNamed(name string, instance any) error {
	current := c
	for {
		err := current.inner.NamedResolve(instance, name)
		if err == nil {
			return nil
		}

		if current.parent == nil {
			return err
		}
		current = current.parent
	}
}
