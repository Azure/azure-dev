// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- lazyEnvironmentResolver.Getenv Tests ---

func Test_LazyEnvironmentResolver_Getenv_Success(t *testing.T) {
	t.Parallel()

	env := environment.NewWithValues("test", map[string]string{
		"MY_VAR":  "my_value",
		"ANOTHER": "another_value",
	})

	resolver := &lazyEnvironmentResolver{
		lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
			return env, nil
		}),
	}

	assert.Equal(t, "my_value", resolver.Getenv("MY_VAR"))
	assert.Equal(t, "another_value", resolver.Getenv("ANOTHER"))
	assert.Equal(t, "", resolver.Getenv("MISSING"))
}

func Test_LazyEnvironmentResolver_Getenv_Error(t *testing.T) {
	t.Parallel()

	resolver := &lazyEnvironmentResolver{
		lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
			return nil, assert.AnError
		}),
	}

	// When the lazy env fails, Getenv returns ""
	assert.Equal(t, "", resolver.Getenv("ANY_KEY"))
}

// --- resolveAction Tests ---

func Test_ResolveAction_NotRegistered(t *testing.T) {
	t.Parallel()

	// Create a real empty nested container
	c := ioc.NewNestedContainer(nil)

	// Attempt to resolve a non-existent action
	_, resolveErr := resolveAction[*buildAction](c, "nonexistent-action")
	// Should error because the action isn't registered
	require.Error(t, resolveErr)
}

// --- registerAction Tests ---

func Test_RegisterAction_DoesNotPanic(t *testing.T) {
	t.Parallel()

	// Create a real empty nested container
	c := ioc.NewNestedContainer(nil)

	// This should not panic - it just registers a resolver
	require.NotPanics(t, func() {
		registerAction[*buildAction](c, "test-action")
	})
}
