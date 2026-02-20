// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package account

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionsCache(t *testing.T) {
	dir := t.TempDir()
	s := &subscriptionsCache{
		cacheDir:     dir,
		inMemoryCopy: map[string][]Subscription{},
	}
	ctx := context.Background()

	// Empty state
	// Load items returns "not exist"
	_, err := s.Load(ctx, "key1")
	require.ErrorIs(t, err, os.ErrNotExist)

	_, err = s.Load(ctx, "key2")
	require.ErrorIs(t, err, os.ErrNotExist)

	// Clear items does not fail
	err = s.Clear(ctx)
	require.NoError(t, err)

	// Save items
	err = s.Save(ctx, "key1", []Subscription{{Id: "1", Name: "sub1"}})
	require.NoError(t, err)

	err = s.Save(ctx, "key2", []Subscription{{Id: "2", Name: "sub2"}})
	require.NoError(t, err)

	// Load items
	load, err := s.Load(ctx, "key1")
	require.NoError(t, err)
	require.Equal(t, "1", load[0].Id)

	load, err = s.Load(ctx, "key2")
	require.NoError(t, err)
	require.Equal(t, "2", load[0].Id)

	// Update items with Save and Load
	err = s.Save(ctx, "key1", []Subscription{{Id: "1", Name: "sub1-updated"}})
	require.NoError(t, err)

	load, err = s.Load(ctx, "key1")
	require.NoError(t, err)
	require.Equal(t, "1", load[0].Id)
	require.Equal(t, "sub1-updated", load[0].Name)

	// Clear items
	err = s.Clear(ctx)
	require.NoError(t, err)

	_, err = s.Load(ctx, "key1")
	require.ErrorIs(t, err, os.ErrNotExist)

	_, err = s.Load(ctx, "key2")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestSubscriptionsCache_Merge(t *testing.T) {
	t.Run("MergeIntoEmptyCache", func(t *testing.T) {
		dir := t.TempDir()
		s := &subscriptionsCache{
			cacheDir:     dir,
			inMemoryCopy: map[string][]Subscription{},
		}
		ctx := context.Background()

		// Merge into empty cache should add all subscriptions
		err := s.Merge(ctx, "key1", []Subscription{
			{Id: "sub1", Name: "Subscription 1", TenantId: "tenant1"},
			{Id: "sub2", Name: "Subscription 2", TenantId: "tenant2"},
		})
		require.NoError(t, err)

		load, err := s.Load(ctx, "key1")
		require.NoError(t, err)
		require.Len(t, load, 2)

		// Sort by ID for consistent comparison
		sort.Slice(load, func(i, j int) bool { return load[i].Id < load[j].Id })
		require.Equal(t, "sub1", load[0].Id)
		require.Equal(t, "sub2", load[1].Id)
	})

	t.Run("MergeUpdatesExistingSubscriptions", func(t *testing.T) {
		dir := t.TempDir()
		s := &subscriptionsCache{
			cacheDir:     dir,
			inMemoryCopy: map[string][]Subscription{},
		}
		ctx := context.Background()

		// Initial cache state
		err := s.Save(ctx, "key1", []Subscription{
			{Id: "sub1", Name: "Subscription 1 Old", TenantId: "tenant1"},
			{Id: "sub2", Name: "Subscription 2 Old", TenantId: "tenant2"},
		})
		require.NoError(t, err)

		// Merge with updated subscription
		err = s.Merge(ctx, "key1", []Subscription{
			{Id: "sub1", Name: "Subscription 1 New", TenantId: "tenant1"},
		})
		require.NoError(t, err)

		load, err := s.Load(ctx, "key1")
		require.NoError(t, err)
		require.Len(t, load, 2)

		// Sort by ID for consistent comparison
		sort.Slice(load, func(i, j int) bool { return load[i].Id < load[j].Id })
		require.Equal(t, "sub1", load[0].Id)
		require.Equal(t, "Subscription 1 New", load[0].Name)
		require.Equal(t, "sub2", load[1].Id)
		require.Equal(t, "Subscription 2 Old", load[1].Name)
	})

	t.Run("MergePreservesUnchangedSubscriptions", func(t *testing.T) {
		dir := t.TempDir()
		s := &subscriptionsCache{
			cacheDir:     dir,
			inMemoryCopy: map[string][]Subscription{},
		}
		ctx := context.Background()

		// Initial cache with subscriptions from two tenants
		err := s.Save(ctx, "key1", []Subscription{
			{Id: "subA", Name: "Subscription A", TenantId: "tenant1", UserAccessTenantId: "tenant1"},
			{Id: "subB", Name: "Subscription B", TenantId: "tenant2", UserAccessTenantId: "tenant2"},
		})
		require.NoError(t, err)

		// Merge with only tenant1's subscription (simulating tenant2 being temporarily inaccessible)
		err = s.Merge(ctx, "key1", []Subscription{
			{Id: "subA", Name: "Subscription A Updated", TenantId: "tenant1", UserAccessTenantId: "tenant1"},
		})
		require.NoError(t, err)

		load, err := s.Load(ctx, "key1")
		require.NoError(t, err)
		require.Len(t, load, 2, "Both subscriptions should be preserved")

		// Sort by ID for consistent comparison
		sort.Slice(load, func(i, j int) bool { return load[i].Id < load[j].Id })

		// subA should be updated
		require.Equal(t, "subA", load[0].Id)
		require.Equal(t, "Subscription A Updated", load[0].Name)

		// subB should be preserved with original values
		require.Equal(t, "subB", load[1].Id)
		require.Equal(t, "Subscription B", load[1].Name)
		require.Equal(t, "tenant2", load[1].TenantId)
		require.Equal(t, "tenant2", load[1].UserAccessTenantId)
	})

	t.Run("MergeAddsNewSubscriptions", func(t *testing.T) {
		dir := t.TempDir()
		s := &subscriptionsCache{
			cacheDir:     dir,
			inMemoryCopy: map[string][]Subscription{},
		}
		ctx := context.Background()

		// Initial cache with one subscription
		err := s.Save(ctx, "key1", []Subscription{
			{Id: "sub1", Name: "Subscription 1", TenantId: "tenant1"},
		})
		require.NoError(t, err)

		// Merge with two subscriptions (one existing, one new)
		err = s.Merge(ctx, "key1", []Subscription{
			{Id: "sub1", Name: "Subscription 1", TenantId: "tenant1"},
			{Id: "sub3", Name: "Subscription 3", TenantId: "tenant3"},
		})
		require.NoError(t, err)

		load, err := s.Load(ctx, "key1")
		require.NoError(t, err)
		require.Len(t, load, 2)

		// Sort by ID for consistent comparison
		sort.Slice(load, func(i, j int) bool { return load[i].Id < load[j].Id })
		require.Equal(t, "sub1", load[0].Id)
		require.Equal(t, "sub3", load[1].Id)
	})

	t.Run("MergeWithEmptyList", func(t *testing.T) {
		dir := t.TempDir()
		s := &subscriptionsCache{
			cacheDir:     dir,
			inMemoryCopy: map[string][]Subscription{},
		}
		ctx := context.Background()

		// Initial cache with subscriptions
		err := s.Save(ctx, "key1", []Subscription{
			{Id: "sub1", Name: "Subscription 1", TenantId: "tenant1"},
			{Id: "sub2", Name: "Subscription 2", TenantId: "tenant2"},
		})
		require.NoError(t, err)

		// Merge with empty list (simulating all tenants being temporarily inaccessible)
		err = s.Merge(ctx, "key1", []Subscription{})
		require.NoError(t, err)

		// Existing subscriptions should be preserved
		load, err := s.Load(ctx, "key1")
		require.NoError(t, err)
		require.Len(t, load, 2, "Existing subscriptions should be preserved when merging empty list")
	})

	t.Run("MergeMultipleKeys", func(t *testing.T) {
		dir := t.TempDir()
		s := &subscriptionsCache{
			cacheDir:     dir,
			inMemoryCopy: map[string][]Subscription{},
		}
		ctx := context.Background()

		// Save subscriptions for key1
		err := s.Save(ctx, "key1", []Subscription{
			{Id: "sub1", Name: "Subscription 1", TenantId: "tenant1"},
		})
		require.NoError(t, err)

		// Save subscriptions for key2
		err = s.Save(ctx, "key2", []Subscription{
			{Id: "sub2", Name: "Subscription 2", TenantId: "tenant2"},
		})
		require.NoError(t, err)

		// Merge into key1 shouldn't affect key2
		err = s.Merge(ctx, "key1", []Subscription{
			{Id: "sub3", Name: "Subscription 3", TenantId: "tenant3"},
		})
		require.NoError(t, err)

		// key1 should have both subscriptions
		load, err := s.Load(ctx, "key1")
		require.NoError(t, err)
		require.Len(t, load, 2)

		// key2 should remain unchanged
		load, err = s.Load(ctx, "key2")
		require.NoError(t, err)
		require.Len(t, load, 1)
		require.Equal(t, "sub2", load[0].Id)
	})
}
