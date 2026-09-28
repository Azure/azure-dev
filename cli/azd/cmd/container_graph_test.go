// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/azd"
	"github.com/azure/azure-dev/cli/azd/pkg/devcenter"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestRegistrationGraph_Lifetimes(t *testing.T) {
	t.Parallel()

	for _, profile := range []struct {
		name      string
		configure func(*ioc.NestedContainer) error
	}{
		{name: "common"},
		{name: "default", configure: azd.NewDefaultPlatform().ConfigureContainer},
		{name: "devcenter", configure: func(container *ioc.NestedContainer) error {
			if err := azd.NewDefaultPlatform().ConfigureContainer(container); err != nil {
				return err
			}
			provider := devcenter.NewPlatform(&platform.Config{Type: devcenter.PlatformKindDevCenter})
			return provider.ConfigureContainer(container)
		}},
	} {
		t.Run(profile.name, func(t *testing.T) {
			t.Parallel()
			container := ioc.NewNestedContainer(nil)
			registerCommonDependencies(container)
			if profile.configure != nil {
				require.NoError(t, profile.configure(container))
			}

			registrations := container.Registrations()
			paths := captiveDependencyPaths(registrations, commandScopeInputs())
			if len(paths) > 0 {
				t.Errorf("%d singleton paths capture command-scoped inputs:\n%s", len(paths), strings.Join(paths, "\n"))
			}
			reviewPaths := captiveDependencyPaths(registrations, scopedDependencyInputs(registrations))
			reviewPaths = slices.DeleteFunc(reviewPaths, func(path string) bool { return slices.Contains(paths, path) })
			if len(reviewPaths) > 0 {
				t.Logf("%d additional singleton-to-scoped paths need lifetime review:\n%s",
					len(reviewPaths), strings.Join(reviewPaths, "\n"))
			}
		})
	}
}

func scopedDependencyInputs(registrations []ioc.Registration) map[registrationKey]bool {
	inputs := map[registrationKey]bool{}
	for _, registration := range registrations {
		if registration.Lifetime == ioc.ScopedLifetime {
			inputs[registrationKey{registration.ServiceType, registration.Name}] = true
		}
	}
	return inputs
}

// commandScopeInputs identifies command-specific values, not every scoped registration.
// The deliberately root-owned process context is excluded.
func commandScopeInputs() map[registrationKey]bool {
	return map[registrationKey]bool{
		{serviceType: reflect.TypeFor[*cobra.Command]()}:                       true,
		{serviceType: reflect.TypeFor[CmdAnnotations]()}:                       true,
		{serviceType: reflect.TypeFor[CmdCalledAs]()}:                          true,
		{serviceType: reflect.TypeFor[internal.EnvFlag]()}:                     true,
		{serviceType: reflect.TypeFor[input.Console]()}:                        true,
		{serviceType: reflect.TypeFor[io.Writer]()}:                            true,
		{serviceType: reflect.TypeFor[*environment.Environment]()}:             true,
		{serviceType: reflect.TypeFor[*lazy.Lazy[*environment.Environment]]()}: true,
	}
}

func TestRegistrationGraph_Inspection(t *testing.T) {
	container := ioc.NewNestedContainer(nil)
	container.MustRegisterScoped(func() *graphRequest {
		panic("inspection must not construct services")
	})
	container.MustRegisterNamedSingleton("named", func(*graphRequest) *graphSingleton {
		panic("inspection must not construct services")
	})

	requestType := reflect.TypeFor[*graphRequest]()
	singletonType := reflect.TypeFor[*graphSingleton]()
	found := false
	for _, registration := range container.Registrations() {
		if registration.ServiceType != singletonType {
			continue
		}
		found = true
		require.Equal(t, "named", registration.Name)
		require.Equal(t, ioc.SingletonLifetime, registration.Lifetime)
		require.Equal(t, []reflect.Type{requestType}, registration.Dependencies)
		registration.Dependencies[0] = singletonType
	}
	require.True(t, found)
	for _, registration := range container.Registrations() {
		if registration.ServiceType == singletonType {
			require.Equal(t, []reflect.Type{requestType}, registration.Dependencies)
		}
	}
}

func TestRegistrationGraph_Paths(t *testing.T) {
	t.Parallel()
	requestType := reflect.TypeFor[*graphRequest]()
	singletonType := reflect.TypeFor[*graphSingleton]()
	bridgeType := reflect.TypeFor[*graphBridge]()
	request := ioc.Registration{ServiceType: requestType, Lifetime: ioc.ScopedLifetime}
	singleton := ioc.Registration{
		ServiceType: singletonType, Lifetime: ioc.SingletonLifetime, Dependencies: []reflect.Type{requestType},
	}
	bridge := ioc.Registration{
		ServiceType: bridgeType, Lifetime: ioc.TransientLifetime, Dependencies: []reflect.Type{requestType},
	}
	indirect := singleton
	indirect.Dependencies = []reflect.Type{bridgeType}
	namedSingleton := singleton
	namedSingleton.Name = "named"
	namedRequest := request
	namedRequest.Name = "named"
	scopedBridge := bridge
	scopedBridge.Lifetime = ioc.ScopedLifetime
	cycle := bridge
	cycle.Dependencies = []reflect.Type{singletonType, requestType}
	instance := request
	instance.Lifetime = ioc.SingletonLifetime
	requestPolicy := map[registrationKey]bool{{serviceType: requestType}: true}
	transientPath := "singleton *cmd.graphSingleton -> transient *cmd.graphBridge -> scoped *cmd.graphRequest"

	for _, scenario := range []struct {
		name          string
		registrations []ioc.Registration
		requiresScope map[registrationKey]bool
		want          []string
	}{
		{
			name: "direct", registrations: []ioc.Registration{request, singleton}, requiresScope: requestPolicy,
			want: []string{"singleton *cmd.graphSingleton -> scoped *cmd.graphRequest"},
		},
		{
			name: "transient bridge", registrations: []ioc.Registration{request, bridge, indirect},
			requiresScope: requestPolicy,
			want:          []string{transientPath},
		},
		{
			name: "scoped bridge", registrations: []ioc.Registration{request, scopedBridge, indirect},
			requiresScope: requestPolicy,
			want:          []string{"singleton *cmd.graphSingleton -> scoped *cmd.graphBridge -> scoped *cmd.graphRequest"},
		},
		{
			name: "intentional root scoped value", registrations: []ioc.Registration{request, singleton},
		},
		{
			name: "named and unnamed singletons", registrations: []ioc.Registration{request, singleton, namedSingleton},
			requiresScope: requestPolicy,
			want: []string{
				"singleton *cmd.graphSingleton -> scoped *cmd.graphRequest",
				"singleton *cmd.graphSingleton[name=\"named\"] -> scoped *cmd.graphRequest",
			},
		},
		{
			name:          "constructor parameters do not resolve named bindings",
			registrations: []ioc.Registration{namedRequest, singleton},
			requiresScope: map[registrationKey]bool{{serviceType: requestType, name: "named"}: true},
		},
		{
			name: "command input registered later", registrations: []ioc.Registration{singleton},
			requiresScope: requestPolicy,
			want:          []string{"singleton *cmd.graphSingleton -> command input *cmd.graphRequest"},
		},
		{
			name: "cycles terminate", registrations: []ioc.Registration{request, cycle, indirect},
			requiresScope: requestPolicy,
			want:          []string{transientPath},
		},
		{
			name:          "policy is independent of registration lifetime",
			registrations: []ioc.Registration{instance, singleton}, requiresScope: requestPolicy,
			want: []string{"singleton *cmd.graphSingleton -> singleton *cmd.graphRequest"},
		},
		{
			name: "shared singleton without command inputs", registrations: []ioc.Registration{instance, singleton},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			require.Equal(t, scenario.want, captiveDependencyPaths(scenario.registrations, scenario.requiresScope))
			slices.Reverse(scenario.registrations)
			require.Equal(t, scenario.want, captiveDependencyPaths(scenario.registrations, scenario.requiresScope))
		})
	}
}

func TestRegistrationGraph_RegisteredLifetimes(t *testing.T) {
	t.Parallel()
	requestResolver := func() *graphRequest { panic("inspection must not construct services") }
	bridgeResolver := func(*graphRequest) *graphBridge { panic("inspection must not construct services") }
	singletonResolver := func(*graphBridge) *graphSingleton { panic("inspection must not construct services") }

	for _, scenario := range []struct {
		name      string
		configure func(*ioc.NestedContainer)
		want      []string
	}{
		{
			name: "singleton to scoped",
			configure: func(container *ioc.NestedContainer) {
				container.MustRegisterSingleton(bridgeResolver)
			},
			want: []string{"singleton *cmd.graphBridge -> scoped *cmd.graphRequest"},
		},
		{
			name: "singleton through transient to scoped",
			configure: func(container *ioc.NestedContainer) {
				container.MustRegisterTransient(bridgeResolver)
				container.MustRegisterSingleton(singletonResolver)
			},
			want: []string{"singleton *cmd.graphSingleton -> transient *cmd.graphBridge -> scoped *cmd.graphRequest"},
		},
		{
			name: "named singleton through transient to scoped",
			configure: func(container *ioc.NestedContainer) {
				container.MustRegisterTransient(bridgeResolver)
				container.MustRegisterNamedSingleton("named", singletonResolver)
			},
			want: []string{
				"singleton *cmd.graphSingleton[name=\"named\"] -> transient *cmd.graphBridge -> scoped *cmd.graphRequest",
			},
		},
		{
			name: "transient without scoped dependencies is allowed",
			configure: func(container *ioc.NestedContainer) {
				container.MustRegisterTransient(func() *graphBridge { panic("inspection must not construct services") })
				container.MustRegisterSingleton(singletonResolver)
			},
		},
		{
			name: "scoped consumer of scoped dependency is allowed",
			configure: func(container *ioc.NestedContainer) {
				container.MustRegisterTransient(bridgeResolver)
				container.MustRegisterScoped(singletonResolver)
			},
		},
		{
			name: "replacement lifetime is respected",
			configure: func(container *ioc.NestedContainer) {
				container.MustRegisterTransient(bridgeResolver)
				container.MustRegisterSingleton(singletonResolver)
				container.MustRegisterTransient(requestResolver)
			},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			container := ioc.NewNestedContainer(nil)
			container.MustRegisterScoped(requestResolver)
			scenario.configure(container)
			registrations := container.Registrations()
			require.Equal(t, scenario.want, captiveDependencyPaths(registrations, scopedDependencyInputs(registrations)))
		})
	}
}

func TestRegistrationGraph_CaptiveTransient(t *testing.T) {
	t.Parallel()
	root := ioc.NewNestedContainer(nil)
	rootRequest := &graphRequest{name: "root"}
	root.MustRegisterScoped(func() *graphRequest { return rootRequest })
	bridgeCalls := 0
	root.MustRegisterTransient(func(request *graphRequest) *graphBridge {
		bridgeCalls++
		return &graphBridge{request: request}
	})
	root.MustRegisterSingleton(func(bridge *graphBridge) *graphSingleton {
		return &graphSingleton{bridge: bridge}
	})

	registrations := root.Registrations()
	require.Equal(t, []string{
		"singleton *cmd.graphSingleton -> transient *cmd.graphBridge -> scoped *cmd.graphRequest",
	}, captiveDependencyPaths(registrations, scopedDependencyInputs(registrations)))
	require.Zero(t, bridgeCalls)

	var firstSingleton *graphSingleton
	for _, name := range []string{"first", "second"} {
		child, err := root.NewScope()
		require.NoError(t, err)
		childRequest := &graphRequest{name: name}
		ioc.RegisterInstance(child, childRequest)

		var singleton *graphSingleton
		require.NoError(t, child.Resolve(&singleton))
		require.NotNil(t, singleton)
		require.NotNil(t, singleton.bridge)
		require.Same(t, rootRequest, singleton.bridge.request)
		if firstSingleton == nil {
			firstSingleton = singleton
		} else {
			require.Same(t, firstSingleton, singleton)
		}

		var freshBridge *graphBridge
		require.NoError(t, child.Resolve(&freshBridge))
		require.NotNil(t, freshBridge)
		require.Same(t, childRequest, freshBridge.request)
		require.NotSame(t, singleton.bridge, freshBridge)
	}
	require.Equal(t, 3, bridgeCalls, "one retained transient and one fresh transient per child")
}

type registrationKey struct {
	serviceType reflect.Type
	name        string
}

// captiveDependencyPaths follows declared constructor inputs only, returning shortest paths to required-scope inputs.
// ServiceLocator and lazy callback lookups need separate behavioral tests.
func captiveDependencyPaths(registrations []ioc.Registration, requiresScope map[registrationKey]bool) []string {
	graph := make(map[registrationKey]ioc.Registration, len(registrations))
	for _, registration := range registrations {
		graph[registrationKey{registration.ServiceType, registration.Name}] = registration
	}

	var paths []string
	for root, registration := range graph {
		if registration.Lifetime != ioc.SingletonLifetime {
			continue
		}
		paths = append(paths, captivePathsFrom(root, graph, requiresScope)...)
	}
	slices.Sort(paths)
	return paths
}

func captivePathsFrom(
	root registrationKey,
	graph map[registrationKey]ioc.Registration,
	requiresScope map[registrationKey]bool,
) []string {
	var paths []string
	visited := map[registrationKey]bool{root: true}
	queue := [][]registrationKey{{root}}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		current := graph[path[len(path)-1]]
		for _, dependency := range current.Dependencies {
			next := registrationKey{serviceType: dependency}
			if visited[next] {
				continue
			}
			visited[next] = true
			nextPath := append(slices.Clone(path), next)
			if requiresScope[next] {
				paths = append(paths, formatRegistrationPath(nextPath, graph))
			} else if _, registered := graph[next]; registered {
				queue = append(queue, nextPath)
			}
		}
	}
	return paths
}

func formatRegistrationPath(path []registrationKey, graph map[registrationKey]ioc.Registration) string {
	parts := make([]string, len(path))
	lifetimes := map[ioc.Lifetime]string{
		ioc.SingletonLifetime: "singleton", ioc.TransientLifetime: "transient", ioc.ScopedLifetime: "scoped",
	}
	for index, key := range path {
		lifetime := "command input"
		if registration, found := graph[key]; found {
			lifetime = lifetimes[registration.Lifetime]
		}
		parts[index] = fmt.Sprintf("%s %s", lifetime, key.serviceType)
		if key.name != "" {
			parts[index] += fmt.Sprintf("[name=%q]", key.name)
		}
	}
	return strings.Join(parts, " -> ")
}

type graphRequest struct {
	name string
}

type graphSingleton struct {
	bridge *graphBridge
}

type graphBridge struct {
	request *graphRequest
}
