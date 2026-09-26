// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ioc

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Container_EmptyParent(t *testing.T) {
	t.Parallel()

	for _, create := range []struct {
		name string
		new  func(*NestedContainer) (*NestedContainer, error)
	}{
		{
			name: "Nested",
			new: func(parent *NestedContainer) (*NestedContainer, error) {
				return NewNestedContainer(parent), nil
			},
		},
		{name: "RegistrationsOnly", new: NewRegistrationsOnly},
	} {
		t.Run(create.name, func(t *testing.T) {
			t.Parallel()
			for _, parent := range []*NestedContainer{nil, {}} {
				c, err := create.new(parent)
				require.NoError(t, err)
				var locator ServiceLocator
				require.NoError(t, c.Resolve(&locator))
				require.Same(t, c, locator)
			}
		})
	}
}

func Test_Singleton_ResolvePathsUseRegistrationScope(t *testing.T) {
	t.Parallel()

	type consumer struct{ service *depService }
	tests := []struct {
		name        string
		bindingName string
		resolve     func(*NestedContainer) (*depService, error)
	}{
		{
			name: "Resolve",
			resolve: func(c *NestedContainer) (*depService, error) {
				var result *depService
				err := c.Resolve(&result)
				return result, err
			},
		},
		{
			name:        "ResolveNamed",
			bindingName: "Service",
			resolve: func(c *NestedContainer) (*depService, error) {
				var result *depService
				err := c.ResolveNamed("Service", &result)
				return result, err
			},
		},
		{
			name: "ConstructorInjection",
			resolve: func(c *NestedContainer) (*depService, error) {
				c.MustRegisterTransient(func(service *depService) *consumer {
					return &consumer{service: service}
				})
				var result *consumer
				if err := c.Resolve(&result); err != nil {
					return nil, err
				}
				return result.service, nil
			},
		},
		{
			name: "Invoke",
			resolve: func(c *NestedContainer) (*depService, error) {
				var result *depService
				err := c.Invoke(func(service *depService) { result = service })
				return result, err
			},
		},
		{
			name: "Fill",
			resolve: func(c *NestedContainer) (*depService, error) {
				var result struct {
					Service *depService `container:"type"`
				}
				err := c.Fill(&result)
				return result.Service, err
			},
		},
		{
			name:        "FillNamed",
			bindingName: "Service",
			resolve: func(c *NestedContainer) (*depService, error) {
				var result struct {
					Service *depService `container:"name"`
				}
				err := c.Fill(&result)
				return result.Service, err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parent := NewNestedContainer(nil)
			parentCounter := &counterService{calls: 1}
			RegisterInstance(parent, parentCounter)
			calls := 0
			var owner ServiceLocator
			factory := func(counter *counterService, locator ServiceLocator) *depService {
				calls++
				owner = locator
				return newDepService(counter)
			}
			if tt.bindingName == "" {
				parent.MustRegisterSingleton(factory)
			} else {
				parent.MustRegisterNamedSingleton(tt.bindingName, factory)
			}

			child, err := parent.NewScope()
			require.NoError(t, err)
			childCounter := &counterService{calls: 2}
			RegisterInstance(child, childCounter)
			grandchild, err := child.NewScope()
			require.NoError(t, err)
			RegisterInstance(grandchild, &counterService{calls: 3})
			sibling, err := parent.NewScope()
			require.NoError(t, err)
			require.Zero(t, calls)

			resolved, err := tt.resolve(grandchild)
			require.NoError(t, err)
			require.NotNil(t, resolved)
			require.Same(t, parentCounter, resolved.counter)
			require.Same(t, parent, owner)
			for _, scope := range []*NestedContainer{parent, child, grandchild, sibling} {
				var shared *depService
				require.NoError(t, scope.ResolveNamed(tt.bindingName, &shared))
				require.Same(t, resolved, shared)
			}
			require.Equal(t, 1, calls)

			var directChildCounter *counterService
			require.NoError(t, child.Resolve(&directChildCounter))
			require.Same(t, childCounter, directChildCounter)
		})
	}
}

func Test_RegistrationsOnly_RebindsOriginalConstructors(t *testing.T) {
	t.Parallel()

	type ownedService struct {
		counter *counterService
		owner   ServiceLocator
	}
	tests := []struct {
		name  string
		clone func(*NestedContainer) (*NestedContainer, error)
	}{
		{name: "NewRegistrationsOnly", clone: NewRegistrationsOnly},
		{name: "NewScopeRegistrationsOnly", clone: (*NestedContainer).NewScopeRegistrationsOnly},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parent := NewNestedContainer(nil)
			parentCounter := &counterService{calls: 1}
			RegisterInstance(parent, parentCounter)
			calls := 0
			factory := func(counter *counterService, owner ServiceLocator) *ownedService {
				calls++
				return &ownedService{counter: counter, owner: owner}
			}
			parent.MustRegisterSingleton(factory)
			parent.MustRegisterNamedSingleton("named", factory)
			previous := map[string]*ownedService{}
			for _, name := range []string{"", "named"} {
				var resolved *ownedService
				require.NoError(t, parent.ResolveNamed(name, &resolved))
				previous[name] = resolved
			}
			require.Equal(t, 2, calls)
			parentInstance := previous[""]

			source, err := parent.NewScope()
			require.NoError(t, err)
			RegisterInstance(source, &counterService{calls: 2})
			source = NewNestedContainer(source)

			for i := range 2 {
				clone, err := tt.clone(source)
				require.NoError(t, err)
				require.Equal(t, 2+i*2, calls, "replaying registrations must stay lazy")
				cloneCounter := &counterService{calls: 3 + i}
				RegisterInstance(clone, cloneCounter)
				child, err := clone.NewScope()
				require.NoError(t, err)
				RegisterInstance(child, &counterService{calls: 99})

				for _, name := range []string{"", "named"} {
					var resolved *ownedService
					require.NoError(t, child.ResolveNamed(name, &resolved))
					require.NotNil(t, resolved)
					require.Same(t, cloneCounter, resolved.counter)
					require.Same(t, clone, resolved.owner)
					require.NotSame(t, previous[name], resolved)
					var shared *ownedService
					require.NoError(t, clone.ResolveNamed(name, &shared))
					require.Same(t, resolved, shared)
					previous[name] = resolved
				}
				require.Equal(t, 4+i*2, calls)
				source = child
			}

			var unchanged *ownedService
			require.NoError(t, parent.Resolve(&unchanged))
			require.Same(t, parentInstance, unchanged)
			require.Same(t, parentCounter, unchanged.counter)
		})
	}
}

func Test_ScopedAndTransient_UseScopeDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		bindingName string
		register    func(*NestedContainer, any) error
		transient   bool
	}{
		{name: "Scoped", register: (*NestedContainer).RegisterScoped},
		{name: "Transient", register: (*NestedContainer).RegisterTransient, transient: true},
		{
			name:        "NamedScoped",
			bindingName: "named",
			register: func(c *NestedContainer, factory any) error {
				return c.RegisterNamedScoped("named", factory)
			},
		},
		{
			name:        "NamedTransient",
			bindingName: "named",
			register: func(c *NestedContainer, factory any) error {
				return c.RegisterNamedTransient("named", factory)
			},
			transient: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parent := NewNestedContainer(nil)
			require.NoError(t, tt.register(parent, newDepService))
			child, err := parent.NewScope()
			require.NoError(t, err)
			grandchild, err := child.NewScope()
			require.NoError(t, err)
			clone, err := NewRegistrationsOnly(child)
			require.NoError(t, err)
			clonedChild, err := clone.NewScope()
			require.NoError(t, err)

			var previous *depService
			for i, scope := range []*NestedContainer{parent, child, grandchild, clone, clonedChild} {
				counter := &counterService{calls: i}
				RegisterInstance(scope, counter)
				var first, second *depService
				require.NoError(t, scope.ResolveNamed(tt.bindingName, &first))
				require.NoError(t, scope.ResolveNamed(tt.bindingName, &second))
				require.NotNil(t, first)
				require.NotNil(t, second)
				require.Same(t, counter, first.counter)
				require.Same(t, counter, second.counter)
				if tt.transient {
					require.NotSame(t, first, second)
				} else {
					require.Same(t, first, second)
				}
				require.NotSame(t, previous, first)
				previous = first
			}
		})
	}
}

func Test_RegistrationReplacement_ReplaysLatestLifetime(t *testing.T) {
	t.Parallel()

	registrations := []struct {
		name      string
		register  func(*NestedContainer, string, any) error
		scoped    bool
		transient bool
	}{
		{name: "Singleton", register: (*NestedContainer).RegisterNamedSingleton},
		{name: "Scoped", register: (*NestedContainer).RegisterNamedScoped, scoped: true},
		{name: "Transient", register: (*NestedContainer).RegisterNamedTransient, transient: true},
	}
	for _, initial := range registrations {
		for _, replacement := range registrations {
			for _, name := range []string{"", "named"} {
				t.Run(initial.name+"To"+replacement.name+"/"+name, func(t *testing.T) {
					t.Parallel()

					parent := NewNestedContainer(nil)
					require.NoError(t, initial.register(parent, name, func() *counterService {
						return &counterService{calls: 1}
					}))
					require.NoError(t, replacement.register(parent, name, func() *counterService {
						return &counterService{calls: 2}
					}))
					unrelated := &counterService{calls: 3}
					RegisterNamedInstance(parent, "unrelated", unrelated)
					var rootInstance *counterService
					require.NoError(t, parent.ResolveNamed(name, &rootInstance))

					child, err := parent.NewScope()
					require.NoError(t, err)
					clone, err := parent.NewScopeRegistrationsOnly()
					require.NoError(t, err)
					for _, scope := range []*NestedContainer{child, clone} {
						var first, second, other *counterService
						require.NoError(t, scope.ResolveNamed(name, &first))
						require.NoError(t, scope.ResolveNamed(name, &second))
						require.NotNil(t, first)
						require.NotNil(t, second)
						require.Equal(t, 2, first.calls)
						require.Equal(t, 2, second.calls)
						if replacement.transient {
							require.NotSame(t, first, second)
						} else {
							require.Same(t, first, second)
						}
						if scope == child && !replacement.scoped && !replacement.transient {
							require.Same(t, rootInstance, first)
						} else {
							require.NotSame(t, rootInstance, first)
						}
						require.NoError(t, scope.ResolveNamed("unrelated", &other))
						require.Same(t, unrelated, other)
					}
				})
			}
		}
	}

	t.Run("InstanceReplacesScoped", func(t *testing.T) {
		t.Parallel()
		parent := NewNestedContainer(nil)
		parent.MustRegisterScoped(newCounterService)
		instance := &counterService{calls: 42}
		RegisterInstance(parent, instance)
		child, err := parent.NewScope()
		require.NoError(t, err)
		clone, err := NewRegistrationsOnly(parent)
		require.NoError(t, err)
		for _, scope := range []*NestedContainer{parent, child, clone} {
			var resolved *counterService
			require.NoError(t, scope.Resolve(&resolved))
			require.Same(t, instance, resolved)
		}
	})
}

func Test_Singleton_RebindingPreservesExistingChildren(t *testing.T) {
	t.Parallel()

	parent := NewNestedContainer(nil)
	parent.MustRegisterSingleton(func() *counterService {
		return &counterService{calls: 1}
	})
	child, err := parent.NewScope()
	require.NoError(t, err)
	parent.MustRegisterSingleton(func() *counterService {
		return &counterService{calls: 2}
	})

	var original, replacement *counterService
	require.NoError(t, child.Resolve(&original))
	require.NoError(t, parent.Resolve(&replacement))
	require.NotNil(t, original)
	require.NotNil(t, replacement)
	require.Equal(t, 1, original.calls)
	require.Equal(t, 2, replacement.calls)
	require.NotSame(t, original, replacement)

	clone, err := NewRegistrationsOnly(child)
	require.NoError(t, err)
	var cloned *counterService
	require.NoError(t, clone.Resolve(&cloned))
	require.NotNil(t, cloned)
	require.Equal(t, 1, cloned.calls)
	require.NotSame(t, original, cloned)
}

func Test_Singleton_ResolutionFailuresAreRetryable(t *testing.T) {
	t.Parallel()

	t.Run("MissingOwnerDependency", func(t *testing.T) {
		t.Parallel()
		parent := NewNestedContainer(nil)
		parent.MustRegisterSingleton(newDepService)
		child, err := parent.NewScope()
		require.NoError(t, err)
		RegisterInstance(child, &counterService{calls: 2})

		var resolved *depService
		for range 2 {
			require.ErrorIs(t, child.Resolve(&resolved), ErrResolveInstance)
			require.Nil(t, resolved)
		}
		counter := &counterService{calls: 1}
		RegisterInstance(parent, counter)
		require.NoError(t, child.Resolve(&resolved))
		require.NotNil(t, resolved)
		require.Same(t, counter, resolved.counter)
	})

	for _, returnValueOnError := range []bool{false, true} {
		name := "NilValueWithError"
		if returnValueOnError {
			name = "NonNilValueWithError"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			parent := NewNestedContainer(nil)
			sentinel := errors.New("construction failed")
			calls := 0
			parent.MustRegisterNamedSingleton("named", func() (*counterService, error) {
				calls++
				value := &counterService{calls: calls}
				if calls < 3 {
					if returnValueOnError {
						return value, sentinel
					}
					return nil, sentinel
				}
				return value, nil
			})
			child, err := parent.NewScope()
			require.NoError(t, err)
			var resolved *counterService
			for range 2 {
				require.ErrorIs(t, child.ResolveNamed("named", &resolved), sentinel)
				require.Nil(t, resolved)
			}
			require.NoError(t, child.ResolveNamed("named", &resolved))
			require.NotNil(t, resolved)
			require.Equal(t, 3, resolved.calls)
			var shared *counterService
			require.NoError(t, parent.ResolveNamed("named", &shared))
			require.Same(t, resolved, shared)
			require.Equal(t, 3, calls)
		})
	}
}

func Test_Singleton_CachesSuccessfulNilAndZeroValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		resolver  func(func()) any
		newTarget func() any
		want      any
	}{
		{
			name: "TypedNil",
			resolver: func(recordCall func()) any {
				return func() (*counterService, error) {
					recordCall()
					return nil, nil
				}
			},
			newTarget: func() any { return new(newCounterService()) },
			want:      new(*counterService),
		},
		{
			name: "ZeroScalar",
			resolver: func(recordCall func()) any {
				return func() (int, error) {
					recordCall()
					return 0, nil
				}
			},
			newTarget: func() any { return new(42) },
			want:      new(0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parent := NewNestedContainer(nil)
			calls := 0
			parent.MustRegisterSingleton(tt.resolver(func() { calls++ }))
			child, err := parent.NewScope()
			require.NoError(t, err)
			require.Zero(t, calls)

			for _, scope := range []*NestedContainer{child, parent, child, parent} {
				target := tt.newTarget()
				require.NoError(t, scope.Resolve(target))
				require.Equal(t, tt.want, target)
			}
			require.Equal(t, 1, calls)
		})
	}
}

func Test_Registration_RejectsInvalidOriginalConstructor(t *testing.T) {
	t.Parallel()

	var nilFactory func() *counterService
	tests := []struct {
		name    string
		factory any
	}{
		{name: "Nil"},
		{name: "TypedNil", factory: nilFactory},
		{name: "NotAFunction", factory: 42},
		{name: "NoResults", factory: func() {}},
		{name: "TooManyResults", factory: func() (int, int, int) { return 0, 0, 0 }},
		{
			name:    "SelfDependency",
			factory: func(counter *counterService) *counterService { return counter },
		},
	}
	registrations := []struct {
		name     string
		register func(*NestedContainer, any) error
	}{
		{name: "Singleton", register: (*NestedContainer).RegisterSingleton},
		{name: "EagerSingleton", register: (*NestedContainer).RegisterSingletonAndInvoke},
		{name: "Scoped", register: (*NestedContainer).RegisterScoped},
		{name: "Transient", register: (*NestedContainer).RegisterTransient},
	}
	for _, registration := range registrations {
		for _, tt := range tests {
			t.Run(registration.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				c := NewNestedContainer(nil)
				instance := newCounterService()
				RegisterInstance(c, instance)
				require.Error(t, registration.register(c, tt.factory))
				clone, err := NewRegistrationsOnly(c)
				require.NoError(t, err)
				for _, scope := range []*NestedContainer{c, clone} {
					var resolved *counterService
					require.NoError(t, scope.Resolve(&resolved))
					require.Same(t, instance, resolved)
				}
			})
		}
	}
}

func Test_EagerSingleton_ReplaysLazily(t *testing.T) {
	t.Parallel()

	parent := NewNestedContainer(nil)
	counter := newCounterService()
	RegisterInstance(parent, counter)
	calls := 0
	require.NoError(t, parent.RegisterSingletonAndInvoke(func(counter *counterService) *depService {
		calls++
		return newDepService(counter)
	}))
	require.Equal(t, 1, calls)
	child, err := parent.NewScope()
	require.NoError(t, err)
	RegisterInstance(child, newCounterService())
	var original *depService
	require.NoError(t, child.Resolve(&original))
	require.NotNil(t, original)
	require.Same(t, counter, original.counter)

	clone, err := NewRegistrationsOnly(child)
	require.NoError(t, err)
	cloneCounter := newCounterService()
	RegisterInstance(clone, cloneCounter)
	require.Equal(t, 1, calls)
	var cloned *depService
	require.NoError(t, clone.Resolve(&cloned))
	require.NotNil(t, cloned)
	require.Same(t, cloneCounter, cloned.counter)
	require.NotSame(t, original, cloned)
	require.Equal(t, 2, calls)
}

func Test_EagerSingleton_FailurePreservesRegistration(t *testing.T) {
	t.Parallel()

	parent := NewNestedContainer(nil)
	instance := newCounterService()
	RegisterInstance(parent, instance)
	sentinel := errors.New("construction failed")
	err := parent.RegisterSingletonAndInvoke(func() (*counterService, error) {
		return newCounterService(), sentinel
	})
	require.ErrorIs(t, err, sentinel)
	clone, err := NewRegistrationsOnly(parent)
	require.NoError(t, err)
	for _, scope := range []*NestedContainer{parent, clone} {
		var resolved *counterService
		require.NoError(t, scope.Resolve(&resolved))
		require.Same(t, instance, resolved)
	}
}

func Test_Singleton_ConcurrentResolution(t *testing.T) {
	t.Parallel()

	parent := NewNestedContainer(nil)
	counter := newCounterService()
	RegisterInstance(parent, counter)
	calls := 0
	parent.MustRegisterSingleton(func(counter *counterService) *depService {
		calls++
		return newDepService(counter)
	})
	child, err := parent.NewScope()
	require.NoError(t, err)
	RegisterInstance(child, newCounterService())
	sibling, err := parent.NewScope()
	require.NoError(t, err)
	scopes := []*NestedContainer{parent, child, sibling}

	results := make([]*depService, 16)
	errs := make([]error, len(results))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			<-start
			errs[i] = scopes[i%len(scopes)].Resolve(&results[i])
		})
	}
	close(start)
	wg.Wait()

	require.Equal(t, 1, calls)
	for i, result := range results {
		require.NoError(t, errs[i])
		require.NotNil(t, result)
		require.Same(t, results[0], result)
		require.Same(t, counter, result.counter)
	}
}
