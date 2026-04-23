// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ioc

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------- helper types for tests ----------

type greeter interface {
	Greet() string
}

type englishGreeter struct{ name string }

func (g *englishGreeter) Greet() string { return "Hello, " + g.name }

type spanishGreeter struct{ name string }

func (g *spanishGreeter) Greet() string { return "Hola, " + g.name }

type counterService struct {
	calls int
}

func newCounterService() *counterService { return &counterService{} }

type depService struct {
	counter *counterService
}

func newDepService(c *counterService) *depService { return &depService{counter: c} }

// fillTarget is used to test Fill().
type fillTarget struct {
	Greeter greeter `container:"type"`
}

// ---------- Named registration & resolution ----------

func Test_Named_Singleton_Register_Resolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		regName   string
		resolver  any
		wantGreet string
	}{
		{
			name:      "EnglishGreeter",
			regName:   "english",
			resolver:  func() greeter { return &englishGreeter{name: "World"} },
			wantGreet: "Hello, World",
		},
		{
			name:      "SpanishGreeter",
			regName:   "spanish",
			resolver:  func() greeter { return &spanishGreeter{name: "Mundo"} },
			wantGreet: "Hola, Mundo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := NewNestedContainer(nil)
			err := c.RegisterNamedSingleton(tt.regName, tt.resolver)
			require.NoError(t, err)

			var resolved greeter
			err = c.ResolveNamed(tt.regName, &resolved)
			require.NoError(t, err)
			require.Equal(t, tt.wantGreet, resolved.Greet())

			// Singleton: second resolve returns same pointer
			var resolved2 greeter
			err = c.ResolveNamed(tt.regName, &resolved2)
			require.NoError(t, err)
			require.Same(t, resolved, resolved2)
		})
	}
}

func Test_MustRegisterNamedSingleton(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		c.MustRegisterNamedSingleton("en", func() greeter {
			return &englishGreeter{name: "test"}
		})

		var g greeter
		err := c.ResolveNamed("en", &g)
		require.NoError(t, err)
		require.Equal(t, "Hello, test", g.Greet())
	})

	t.Run("PanicsOnInvalidResolver", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		require.Panics(t, func() {
			c.MustRegisterNamedSingleton("bad", "not-a-func")
		})
	})
}

func Test_Named_Transient_Register_Resolve(t *testing.T) {
	t.Parallel()

	c := NewNestedContainer(nil)
	err := c.RegisterNamedTransient("counter", func() *counterService {
		return newCounterService()
	})
	require.NoError(t, err)

	var inst1 *counterService
	err = c.ResolveNamed("counter", &inst1)
	require.NoError(t, err)
	require.NotNil(t, inst1)

	var inst2 *counterService
	err = c.ResolveNamed("counter", &inst2)
	require.NoError(t, err)
	require.NotNil(t, inst2)

	// Transient: different instances each time
	require.NotSame(t, inst1, inst2)
}

func Test_MustRegisterNamedTransient(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		c.MustRegisterNamedTransient("svc", func() *counterService {
			return newCounterService()
		})

		var inst *counterService
		err := c.ResolveNamed("svc", &inst)
		require.NoError(t, err)
		require.NotNil(t, inst)
	})

	t.Run("PanicsOnInvalidResolver", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		require.Panics(t, func() {
			c.MustRegisterNamedTransient("bad", 42)
		})
	})
}

// ---------- RegisterTransient (error-returning) ----------

func Test_RegisterTransient(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		err := c.RegisterTransient(func() *counterService {
			return newCounterService()
		})
		require.NoError(t, err)

		var inst *counterService
		err = c.Resolve(&inst)
		require.NoError(t, err)
		require.NotNil(t, inst)
	})

	t.Run("ErrorOnInvalidResolver", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		err := c.RegisterTransient("not-a-function")
		require.Error(t, err)
	})
}

// ---------- RegisterNamedInstance ----------

func Test_RegisterNamedInstance(t *testing.T) {
	t.Parallel()

	c := NewNestedContainer(nil)
	eng := &englishGreeter{name: "Named"}
	spa := &spanishGreeter{name: "Nombrado"}
	RegisterNamedInstance[greeter](c, "english", eng)
	RegisterNamedInstance[greeter](c, "spanish", spa)

	var resolvedEn greeter
	err := c.ResolveNamed("english", &resolvedEn)
	require.NoError(t, err)
	require.Equal(t, "Hello, Named", resolvedEn.Greet())

	var resolvedEs greeter
	err = c.ResolveNamed("spanish", &resolvedEs)
	require.NoError(t, err)
	require.Equal(t, "Hola, Nombrado", resolvedEs.Greet())
}

// ---------- ResolveNamed error cases ----------

func Test_ResolveNamed_Errors(t *testing.T) {
	t.Parallel()

	t.Run("UnregisteredName", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		var g greeter
		err := c.ResolveNamed("nope", &g)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrResolveInstance))
	})

	t.Run("ResolverReturnsError", func(t *testing.T) {
		t.Parallel()
		sentinel := fmt.Errorf("custom resolution error")
		c := NewNestedContainer(nil)
		c.MustRegisterNamedSingleton("fail", func() (greeter, error) {
			return nil, sentinel
		})

		var g greeter
		err := c.ResolveNamed("fail", &g)
		require.Error(t, err)
		// The underlying error should propagate, not be wrapped as container error
		require.ErrorIs(t, err, sentinel)
		require.False(t, errors.Is(err, ErrResolveInstance))
	})
}

// ---------- Invoke ----------

func Test_Invoke(t *testing.T) {
	t.Parallel()

	t.Run("InjectsRegisteredDeps", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		c.MustRegisterSingleton(func() *counterService {
			return &counterService{calls: 42}
		})

		var captured *counterService
		err := c.Invoke(func(cs *counterService) {
			captured = cs
		})
		require.NoError(t, err)
		require.NotNil(t, captured)
		require.Equal(t, 42, captured.calls)
	})

	t.Run("ErrorWhenDepMissing", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		err := c.Invoke(func(cs *counterService) {
			t.Fatal("should not be called")
		})
		require.Error(t, err)
	})
}

// ---------- Fill ----------

func Test_Fill(t *testing.T) {
	t.Parallel()

	t.Run("FillByType", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		c.MustRegisterSingleton(func() greeter {
			return &englishGreeter{name: "Fill"}
		})

		target := &fillTarget{}
		err := c.Fill(target)
		require.NoError(t, err)
		require.NotNil(t, target.Greeter)
		require.Equal(t, "Hello, Fill", target.Greeter.Greet())
	})

	t.Run("ErrorWhenUnregistered", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		target := &fillTarget{}
		err := c.Fill(target)
		require.Error(t, err)
	})
}

// ---------- RegisterSingletonAndInvoke ----------

func Test_RegisterSingletonAndInvoke(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		err := c.RegisterSingletonAndInvoke(func() *counterService {
			return &counterService{calls: 7}
		})
		require.NoError(t, err)

		var inst *counterService
		err = c.Resolve(&inst)
		require.NoError(t, err)
		require.Equal(t, 7, inst.calls)
	})

	t.Run("ErrorOnInvalidResolver", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		err := c.RegisterSingletonAndInvoke("not-a-func")
		require.Error(t, err)
	})
}

// ---------- RegisterSingleton (error-returning) ----------

func Test_RegisterSingleton_Error(t *testing.T) {
	t.Parallel()
	c := NewNestedContainer(nil)
	err := c.RegisterSingleton("not-a-function")
	require.Error(t, err)
}

// ---------- Named Scoped ----------

func Test_RegisterNamedScoped(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		err := c.RegisterNamedScoped("svc", func() *counterService {
			return newCounterService()
		})
		require.NoError(t, err)

		var inst *counterService
		err = c.ResolveNamed("svc", &inst)
		require.NoError(t, err)
		require.NotNil(t, inst)
	})

	t.Run("ErrorOnInvalidResolver", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		err := c.RegisterNamedScoped("bad", 123)
		require.Error(t, err)
	})
}

func Test_MustRegisterNamedScoped(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		c.MustRegisterNamedScoped("svc", func() *counterService {
			return newCounterService()
		})

		var inst *counterService
		err := c.ResolveNamed("svc", &inst)
		require.NoError(t, err)
		require.NotNil(t, inst)
	})

	t.Run("PanicsOnInvalidResolver", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)
		require.Panics(t, func() {
			c.MustRegisterNamedScoped("bad", 99)
		})
	})
}

// ---------- MustRegisterScoped panics ----------

func Test_MustRegisterScoped_Panics(t *testing.T) {
	t.Parallel()
	c := NewNestedContainer(nil)
	require.Panics(t, func() {
		c.MustRegisterScoped("not-a-function")
	})
}

// ---------- NewScope with named scoped bindings ----------

func Test_NewScope_NamedScopedBindings(t *testing.T) {
	t.Parallel()

	root := NewNestedContainer(nil)
	root.MustRegisterNamedScoped("greeter", func() greeter {
		return &englishGreeter{name: "scoped"}
	})

	scope1, err := root.NewScope()
	require.NoError(t, err)

	var g1 greeter
	err = scope1.ResolveNamed("greeter", &g1)
	require.NoError(t, err)
	require.Equal(t, "Hello, scoped", g1.Greet())

	// Same scope resolves same singleton
	var g1again greeter
	err = scope1.ResolveNamed("greeter", &g1again)
	require.NoError(t, err)
	require.Same(t, g1, g1again)

	// Different scope gets different instance
	scope2, err := root.NewScope()
	require.NoError(t, err)

	var g2 greeter
	err = scope2.ResolveNamed("greeter", &g2)
	require.NoError(t, err)
	require.NotSame(t, g1, g2)
}

// ---------- NewScopeRegistrationsOnly ----------

func Test_NewScopeRegistrationsOnly(t *testing.T) {
	t.Parallel()

	t.Run("ResetsSingletonInstances", func(t *testing.T) {
		t.Parallel()
		root := NewNestedContainer(nil)
		root.MustRegisterSingleton(func() *counterService {
			return newCounterService()
		})

		// Resolve in root to cache the singleton
		var rootInst *counterService
		err := root.Resolve(&rootInst)
		require.NoError(t, err)
		require.NotNil(t, rootInst)

		// Create scope from registrations only - resets cached instances
		scope, err := root.NewScopeRegistrationsOnly()
		require.NoError(t, err)

		var scopeInst *counterService
		err = scope.Resolve(&scopeInst)
		require.NoError(t, err)
		require.NotNil(t, scopeInst)

		// Instances should differ because the scope got fresh registrations
		require.NotSame(t, rootInst, scopeInst)
	})

	t.Run("WithScopedBindings", func(t *testing.T) {
		t.Parallel()
		root := NewNestedContainer(nil)
		root.MustRegisterScoped(func() *counterService {
			return newCounterService()
		})

		scope1, err := root.NewScopeRegistrationsOnly()
		require.NoError(t, err)

		var inst1 *counterService
		err = scope1.Resolve(&inst1)
		require.NoError(t, err)

		scope2, err := root.NewScopeRegistrationsOnly()
		require.NoError(t, err)

		var inst2 *counterService
		err = scope2.Resolve(&inst2)
		require.NoError(t, err)

		// Different scopes, different instances
		require.NotSame(t, inst1, inst2)
	})

	t.Run("WithNamedScopedBindings", func(t *testing.T) {
		t.Parallel()
		root := NewNestedContainer(nil)
		root.MustRegisterNamedScoped("counter", func() *counterService {
			return newCounterService()
		})

		scope, err := root.NewScopeRegistrationsOnly()
		require.NoError(t, err)

		var inst *counterService
		err = scope.ResolveNamed("counter", &inst)
		require.NoError(t, err)
		require.NotNil(t, inst)
	})

	t.Run("NilParent", func(t *testing.T) {
		t.Parallel()
		// NewRegistrationsOnly(nil) should produce a working empty container
		c := NewRegistrationsOnly(nil)
		require.NotNil(t, c)

		// ServiceLocator should still self-register
		var sl ServiceLocator
		err := c.Resolve(&sl)
		require.NoError(t, err)
		require.NotNil(t, sl)
	})
}

// ---------- ServiceLocator self-registration ----------

func Test_ServiceLocator_SelfRegistered(t *testing.T) {
	t.Parallel()

	t.Run("InNewNestedContainer", func(t *testing.T) {
		t.Parallel()
		c := NewNestedContainer(nil)

		var sl ServiceLocator
		err := c.Resolve(&sl)
		require.NoError(t, err)
		require.NotNil(t, sl)
		require.Same(t, c, sl)
	})

	t.Run("InNewRegistrationsOnly", func(t *testing.T) {
		t.Parallel()
		parent := NewNestedContainer(nil)
		child := NewRegistrationsOnly(parent)

		var sl ServiceLocator
		err := child.Resolve(&sl)
		require.NoError(t, err)
		require.Same(t, child, sl)
	})
}

// ---------- ServiceLocator interface methods ----------

func Test_ServiceLocator_Methods(t *testing.T) {
	t.Parallel()

	c := NewNestedContainer(nil)
	c.MustRegisterSingleton(func() *counterService {
		return &counterService{calls: 10}
	})
	RegisterNamedInstance[greeter](c, "en", &englishGreeter{name: "SL"})

	var sl ServiceLocator
	err := c.Resolve(&sl)
	require.NoError(t, err)

	t.Run("Resolve", func(t *testing.T) {
		var cs *counterService
		err := sl.Resolve(&cs)
		require.NoError(t, err)
		require.Equal(t, 10, cs.calls)
	})

	t.Run("ResolveNamed", func(t *testing.T) {
		var g greeter
		err := sl.ResolveNamed("en", &g)
		require.NoError(t, err)
		require.Equal(t, "Hello, SL", g.Greet())
	})

	t.Run("Invoke", func(t *testing.T) {
		var captured *counterService
		err := sl.Invoke(func(cs *counterService) {
			captured = cs
		})
		require.NoError(t, err)
		require.Equal(t, 10, captured.calls)
	})
}

// ---------- Must* panic on invalid resolver ----------

func Test_MustRegisterSingleton_Panics(t *testing.T) {
	t.Parallel()
	c := NewNestedContainer(nil)
	require.Panics(t, func() {
		c.MustRegisterSingleton("invalid")
	})
}

func Test_MustRegisterTransient_Panics(t *testing.T) {
	t.Parallel()
	c := NewNestedContainer(nil)
	require.Panics(t, func() {
		c.MustRegisterTransient(42)
	})
}

// ---------- RegisterNamedSingleton error path ----------

func Test_RegisterNamedSingleton_Error(t *testing.T) {
	t.Parallel()
	c := NewNestedContainer(nil)
	err := c.RegisterNamedSingleton("bad", "not-a-func")
	require.Error(t, err)
}

// ---------- RegisterNamedTransient error path ----------

func Test_RegisterNamedTransient_Error(t *testing.T) {
	t.Parallel()
	c := NewNestedContainer(nil)
	err := c.RegisterNamedTransient("bad", 123)
	require.Error(t, err)
}

// ---------- RegisterScoped error path ----------

func Test_RegisterScoped_Error(t *testing.T) {
	t.Parallel()
	c := NewNestedContainer(nil)
	err := c.RegisterScoped("not-a-func")
	require.Error(t, err)
}

// ---------- Dependency injection chain ----------

func Test_DependencyChain(t *testing.T) {
	t.Parallel()
	c := NewNestedContainer(nil)
	c.MustRegisterSingleton(func() *counterService {
		return &counterService{calls: 5}
	})
	c.MustRegisterSingleton(newDepService)

	var dep *depService
	err := c.Resolve(&dep)
	require.NoError(t, err)
	require.NotNil(t, dep)
	require.NotNil(t, dep.counter)
	require.Equal(t, 5, dep.counter.calls)
}

// ---------- Nested container inherits from parent ----------

func Test_NewNestedContainer_InheritsParent(t *testing.T) {
	t.Parallel()

	parent := NewNestedContainer(nil)
	parent.MustRegisterSingleton(func() *counterService {
		return &counterService{calls: 99}
	})

	// Resolve in parent first to cache the singleton
	var parentInst *counterService
	err := parent.Resolve(&parentInst)
	require.NoError(t, err)

	child := NewNestedContainer(parent)
	var childInst *counterService
	err = child.Resolve(&childInst)
	require.NoError(t, err)
	// Child inherits parent's cached singleton
	require.Same(t, parentInst, childInst)
}

// ---------- inspectResolveError ----------

func Test_InspectResolveError(t *testing.T) {
	t.Parallel()

	t.Run("ContainerError", func(t *testing.T) {
		t.Parallel()
		err := inspectResolveError(fmt.Errorf("container: no binding found"))
		require.ErrorIs(t, err, ErrResolveInstance)
	})

	t.Run("WrappedContainerError", func(t *testing.T) {
		t.Parallel()
		inner := fmt.Errorf("container: something broke")
		wrapped := fmt.Errorf("outer: %w", inner)
		err := inspectResolveError(wrapped)
		require.ErrorIs(t, err, ErrResolveInstance)
	})

	t.Run("NonContainerError", func(t *testing.T) {
		t.Parallel()
		sentinel := fmt.Errorf("custom app error")
		err := inspectResolveError(sentinel)
		require.Equal(t, sentinel, err)
		require.False(t, errors.Is(err, ErrResolveInstance))
	})

	t.Run("WrappedNonContainerError", func(t *testing.T) {
		t.Parallel()
		inner := fmt.Errorf("app error inside")
		wrapped := fmt.Errorf("outer: %w", inner)
		err := inspectResolveError(wrapped)
		// Should unwrap and return the inner non-container error
		require.Equal(t, inner, err)
	})
}

// ---------- Multiple named registrations for same type ----------

func Test_MultipleNamedRegistrations(t *testing.T) {
	t.Parallel()

	c := NewNestedContainer(nil)
	RegisterNamedInstance[greeter](c, "en", &englishGreeter{name: "A"})
	RegisterNamedInstance[greeter](c, "es", &spanishGreeter{name: "B"})

	var en greeter
	err := c.ResolveNamed("en", &en)
	require.NoError(t, err)
	require.Equal(t, "Hello, A", en.Greet())

	var es greeter
	err = c.ResolveNamed("es", &es)
	require.NoError(t, err)
	require.Equal(t, "Hola, B", es.Greet())
}

// ---------- Scoped with mixed named and unnamed ----------

func Test_NewScope_MixedScopedBindings(t *testing.T) {
	t.Parallel()

	root := NewNestedContainer(nil)
	// Unnamed scoped
	root.MustRegisterScoped(func() *counterService {
		return newCounterService()
	})
	// Named scoped
	root.MustRegisterNamedScoped("named-counter", func() *counterService {
		return newCounterService()
	})

	scope, err := root.NewScope()
	require.NoError(t, err)

	var unnamed *counterService
	err = scope.Resolve(&unnamed)
	require.NoError(t, err)
	require.NotNil(t, unnamed)

	var named *counterService
	err = scope.ResolveNamed("named-counter", &named)
	require.NoError(t, err)
	require.NotNil(t, named)

	// They're different registrations so different instances
	require.NotSame(t, unnamed, named)
}

// ---------- NewScopeRegistrationsOnly with mixed ----------

func Test_NewScopeRegistrationsOnly_MixedScopedBindings(t *testing.T) {
	t.Parallel()

	root := NewNestedContainer(nil)
	root.MustRegisterScoped(func() *counterService {
		return newCounterService()
	})
	root.MustRegisterNamedScoped("named-counter", func() *counterService {
		return newCounterService()
	})

	scope, err := root.NewScopeRegistrationsOnly()
	require.NoError(t, err)

	var unnamed *counterService
	err = scope.Resolve(&unnamed)
	require.NoError(t, err)
	require.NotNil(t, unnamed)

	var named *counterService
	err = scope.ResolveNamed("named-counter", &named)
	require.NoError(t, err)
	require.NotNil(t, named)
}

// ---------- Transient in nested scope ----------

func Test_TransientInNestedScope(t *testing.T) {
	t.Parallel()

	root := NewNestedContainer(nil)
	root.MustRegisterTransient(func() *counterService {
		return newCounterService()
	})

	scope, err := root.NewScope()
	require.NoError(t, err)

	var inst1 *counterService
	err = scope.Resolve(&inst1)
	require.NoError(t, err)

	var inst2 *counterService
	err = scope.Resolve(&inst2)
	require.NoError(t, err)

	require.NotSame(t, inst1, inst2)
}
