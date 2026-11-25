// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProviderRegistry_RegisterAndLookup(t *testing.T) {
	t.Parallel()

	registry := newProviderRegistry[*int]("test")
	provider := new(42)
	ctx := t.Context()
	err := registry.Register(ctx, "resources", provider, func(gotCtx context.Context, name string) error {
		require.Equal(t, ctx, gotCtx)
		require.Equal(t, "resources", name)
		registered, exists := registry.Lookup(name)
		require.True(t, exists, "provider must be available before acknowledgment")
		require.Same(t, provider, registered)
		return nil
	})
	require.NoError(t, err)

	registered, exists := registry.Lookup("resources")
	require.True(t, exists)
	require.Same(t, provider, registered)
}

func TestProviderRegistry_LookupMissing(t *testing.T) {
	t.Parallel()

	registry := newProviderRegistry[*int]("test")
	provider, exists := registry.Lookup("missing")
	require.False(t, exists)
	require.Nil(t, provider)
}

func TestProviderRegistry_RejectsDuplicate(t *testing.T) {
	t.Parallel()

	registry := newProviderRegistry[int]("test")
	advertise := func(context.Context, string) error { return nil }
	require.NoError(t, registry.Register(t.Context(), "resources", 1, advertise))

	err := registry.Register(t.Context(), "resources", 2, func(context.Context, string) error {
		t.Error("duplicate registration must not be advertised")
		return nil
	})
	require.EqualError(t, err, `test provider "resources" already registered`)
	provider, exists := registry.Lookup("resources")
	require.True(t, exists)
	require.Equal(t, 1, provider)
}

func TestProviderRegistry_RollsBackAndAllowsRetry(t *testing.T) {
	t.Parallel()

	registry := newProviderRegistry[int]("test")
	registrationErr := errors.New("registration rejected")
	err := registry.Register(t.Context(), "resources", 1, func(context.Context, string) error {
		return registrationErr
	})
	require.ErrorIs(t, err, registrationErr)
	_, exists := registry.Lookup("resources")
	require.False(t, exists)

	require.NoError(t, registry.Register(t.Context(), "resources", 2, func(context.Context, string) error {
		return nil
	}))
	provider, exists := registry.Lookup("resources")
	require.True(t, exists)
	require.Equal(t, 2, provider)
}

func TestProviderRegistry_ConcurrentRegistration(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	registry := newProviderRegistry[int]("test")
	advertising := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	stopped := make(chan struct{})
	registrationErr := errors.New("registration rejected")
	go func() {
		defer close(stopped)
		finished <- registry.Register(ctx, "resources", 1, func(ctx context.Context, _ string) error {
			close(advertising)
			select {
			case <-release:
				return registrationErr
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("registration did not stop")
		}
	})
	select {
	case <-advertising:
	case <-ctx.Done():
		t.Fatal("registration did not begin advertising")
	}

	advertise := func(context.Context, string) error { return nil }
	err := registry.Register(ctx, "resources", 2, advertise)
	require.EqualError(t, err, `test provider "resources" already registered`)
	require.NoError(t, registry.Register(ctx, "other", 3, advertise),
		"advertising must not hold the registry lock")
	provider, exists := registry.Lookup("resources")
	require.True(t, exists)
	require.Equal(t, 1, provider)

	close(release)
	select {
	case err := <-finished:
		require.ErrorIs(t, err, registrationErr)
	case <-ctx.Done():
		t.Fatal("registration did not finish")
	}
	_, exists = registry.Lookup("resources")
	require.False(t, exists)
	provider, exists = registry.Lookup("other")
	require.True(t, exists)
	require.Equal(t, 3, provider)
}
