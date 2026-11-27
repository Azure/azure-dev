// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ioc

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// runConcurrently starts first and second together and waits for both to finish.
func runConcurrently(first func(), second func()) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		first()
	})
	wg.Go(func() {
		<-start
		second()
	})
	close(start)
	wg.Wait()
}

func TestConcurrentProviderRegistration(t *testing.T) {
	container := NewNestedContainer(nil)
	type provisioningProvider struct{}
	type generationProvider struct{}

	var provisioningErr, generationErr error
	runConcurrently(func() {
		provisioningErr = container.RegisterNamedTransient("resources", func() provisioningProvider {
			return provisioningProvider{}
		})
	}, func() {
		generationErr = container.RegisterNamedTransient("infra-generation:resources", func() generationProvider {
			return generationProvider{}
		})
	})

	require.NoError(t, provisioningErr)
	require.NoError(t, generationErr)
}

func TestRegisterNewTypeWhileResolvingUnrelatedType(t *testing.T) {
	container := NewNestedContainer(nil)
	type existingService struct{}
	type newService struct{}
	RegisterInstance(container, existingService{})

	var registerErr, resolveErr error
	runConcurrently(func() {
		registerErr = container.RegisterNamedTransient("extension", func() newService { return newService{} })
	}, func() {
		var service existingService
		resolveErr = container.Resolve(&service)
	})

	require.NoError(t, registerErr)
	require.NoError(t, resolveErr)
}

func TestRegisterWhileResolvingSameType(t *testing.T) {
	container := NewNestedContainer(nil)
	type provider struct{ name string }
	RegisterNamedInstance(container, "builtin", provider{name: "builtin"})

	var registerErr, resolveErr error
	var resolved provider
	runConcurrently(func() {
		registerErr = container.RegisterNamedTransient("extension", func() provider { return provider{name: "extension"} })
	}, func() {
		resolveErr = container.ResolveNamed("builtin", &resolved)
	})

	require.NoError(t, registerErr)
	require.NoError(t, resolveErr)
	require.Equal(t, "builtin", resolved.name)
}

func TestNewScopeWhileRegistering(t *testing.T) {
	container := NewNestedContainer(nil)
	type scopedService struct{}
	type newService struct{}
	container.MustRegisterScoped(func() scopedService { return scopedService{} })

	var registerErr, scopeErr error
	runConcurrently(func() {
		registerErr = container.RegisterScoped(func() newService { return newService{} })
	}, func() {
		_, scopeErr = container.NewScope()
	})

	require.NoError(t, registerErr)
	require.NoError(t, scopeErr)
}

func TestEagerRegistrationCanReenterContainer(t *testing.T) {
	c := NewNestedContainer(nil)
	err := c.RegisterSingletonAndInvoke(func() (string, error) {
		if err := c.RegisterTransient(func() int { return 42 }); err != nil {
			return "", err
		}

		var number int
		if err := c.Resolve(&number); err != nil {
			return "", err
		}
		return strconv.Itoa(number), nil
	})
	require.NoError(t, err)

	var value string
	require.NoError(t, c.Resolve(&value))
	require.Equal(t, "42", value)
}
