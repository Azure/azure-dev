// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestProviderStreamRegistry_Register(t *testing.T) {
	t.Parallel()

	registry := newProviderStreamRegistry[string]("test provider")
	called := false
	require.NoError(t, registry.Register("resources", "first stream", func() error {
		called = true
		return nil
	}))
	require.True(t, called)
	require.Equal(t, "first stream", registry.owners["resources"])
}

func TestProviderStreamRegistry_RejectsDuplicate(t *testing.T) {
	t.Parallel()

	for _, owner := range []string{"first stream", "second stream"} {
		t.Run(owner, func(t *testing.T) {
			t.Parallel()

			registry := newProviderStreamRegistry[string]("test provider")
			require.NoError(t, registry.Register("resources", "first stream", func() error { return nil }))

			err := registry.Register("resources", owner, func() error {
				t.Error("duplicate registration must not reach the container")
				return nil
			})
			require.Equal(t, codes.AlreadyExists, status.Code(err))
			require.Equal(t, `test provider "resources" is already registered`, status.Convert(err).Message())
			require.Equal(t, "first stream", registry.owners["resources"])
		})
	}
}

func TestProviderStreamRegistry_RegistrationFailureAllowsRetry(t *testing.T) {
	t.Parallel()

	registry := newProviderStreamRegistry[string]("test provider")
	err := registry.Register("resources", "first stream", func() error {
		return errors.New("invalid resolver")
	})
	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, "registering test provider: invalid resolver", status.Convert(err).Message())
	require.Empty(t, registry.owners)

	require.NoError(t, registry.Register("resources", "second stream", func() error { return nil }))
	require.Equal(t, "second stream", registry.owners["resources"])
}

func TestProviderStreamRegistry_ReleaseOnlyOwnedProviders(t *testing.T) {
	t.Parallel()

	registry := newProviderStreamRegistry[string]("test provider")
	register := func() error { return nil }
	require.NoError(t, registry.Register("first", "first stream", register))
	require.NoError(t, registry.Register("second", "first stream", register))
	require.NoError(t, registry.Register("other", "second stream", register))

	registry.Release("first stream")
	require.Equal(t, map[string]string{"other": "second stream"}, registry.owners)
}

func TestProviderStreamRegistry_StaleReleasePreservesNewOwner(t *testing.T) {
	t.Parallel()

	registry := newProviderStreamRegistry[string]("test provider")
	register := func() error { return nil }
	require.NoError(t, registry.Register("resources", "first stream", register))
	registry.Release("first stream")
	require.NoError(t, registry.Register("resources", "second stream", register))

	registry.Release("first stream")
	require.Equal(t, "second stream", registry.owners["resources"])
}

func TestProviderStreamRegistry_ConcurrentRegistration(t *testing.T) {
	t.Parallel()

	registry := newProviderStreamRegistry[int]("test provider")
	const attempts = 16
	results := make(chan error, attempts)
	var registrations atomic.Int32
	var wg sync.WaitGroup
	for owner := range attempts {
		wg.Go(func() {
			results <- registry.Register("resources", owner, func() error {
				registrations.Add(1)
				return nil
			})
		})
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.Equal(t, codes.AlreadyExists, status.Code(err))
		}
	}
	require.Equal(t, 1, successes)
	require.EqualValues(t, 1, registrations.Load())
	require.Len(t, registry.owners, 1)
}
