// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestLoadSubscriptionFilter_NoFilter(t *testing.T) {
	cfg := config.NewConfig(nil)

	ids, exists := LoadSubscriptionFilter(cfg, "tid-1")
	require.False(t, exists)
	require.Nil(t, ids)
}

func TestLoadSubscriptionFilter_EmptyTenantId(t *testing.T) {
	cfg := config.NewConfig(nil)

	ids, exists := LoadSubscriptionFilter(cfg, "")
	require.False(t, exists)
	require.Nil(t, ids)
}

func TestSaveAndLoadSubscriptionFilter(t *testing.T) {
	cfg := config.NewConfig(nil)

	err := SaveSubscriptionFilter(cfg, "tid-1", []string{"sub-a", "sub-b"})
	require.NoError(t, err)

	ids, exists := LoadSubscriptionFilter(cfg, "tid-1")
	require.True(t, exists)
	require.Equal(t, []string{"sub-a", "sub-b"}, ids)
}

func TestRemoveSubscriptionFilter(t *testing.T) {
	cfg := config.NewConfig(nil)

	err := SaveSubscriptionFilter(cfg, "tid-1", []string{"sub-a"})
	require.NoError(t, err)

	err = RemoveSubscriptionFilter(cfg, "tid-1")
	require.NoError(t, err)

	ids, exists := LoadSubscriptionFilter(cfg, "tid-1")
	require.False(t, exists)
	require.Nil(t, ids)
}

func TestApplySubscriptionFilter_NoFilter(t *testing.T) {
	subs := []account.Subscription{
		{Id: "sub-1", Name: "Alpha"},
		{Id: "sub-2", Name: "Bravo"},
	}

	result, applied := ApplySubscriptionFilter(subs, nil)
	require.False(t, applied)
	require.Len(t, result, 2)
}

func TestApplySubscriptionFilter_WithFilter(t *testing.T) {
	subs := []account.Subscription{
		{Id: "sub-1", Name: "Alpha"},
		{Id: "sub-2", Name: "Bravo"},
		{Id: "sub-3", Name: "Charlie"},
	}

	result, applied := ApplySubscriptionFilter(subs, []string{"sub-1", "sub-3"})
	require.True(t, applied)
	require.Len(t, result, 2)
	require.Equal(t, "sub-1", result[0].Id)
	require.Equal(t, "sub-3", result[1].Id)
}

func TestApplySubscriptionFilter_CaseInsensitive(t *testing.T) {
	subs := []account.Subscription{
		{Id: "SUB-1", Name: "Alpha"},
		{Id: "sub-2", Name: "Bravo"},
	}

	result, applied := ApplySubscriptionFilter(subs, []string{"sub-1"})
	require.True(t, applied)
	require.Len(t, result, 1)
	require.Equal(t, "SUB-1", result[0].Id)
}

func TestApplySubscriptionFilter_StaleFilter(t *testing.T) {
	subs := []account.Subscription{
		{Id: "sub-1", Name: "Alpha"},
	}

	// Filter references IDs that no longer exist
	result, applied := ApplySubscriptionFilter(subs, []string{"sub-999"})
	require.False(t, applied)
	require.Len(t, result, 1) // returns all
}

func TestGetAllSubscriptionFilters(t *testing.T) {
	cfg := config.NewConfig(nil)

	_ = SaveSubscriptionFilter(cfg, "tid-1", []string{"sub-a"})
	_ = SaveSubscriptionFilter(cfg, "tid-2", []string{"sub-b", "sub-c"})

	filters := GetAllSubscriptionFilters(cfg)
	require.Len(t, filters, 2)
	require.Equal(t, []string{"sub-a"}, filters["tid-1"])
	require.Equal(t, []string{"sub-b", "sub-c"}, filters["tid-2"])
}

func TestGetAllSubscriptionFilters_Empty(t *testing.T) {
	cfg := config.NewConfig(nil)

	filters := GetAllSubscriptionFilters(cfg)
	require.Nil(t, filters)
}

func TestSubscriptionsMatchingFilter(t *testing.T) {
	subs := []account.Subscription{
		{Id: "sub-1", Name: "Alpha"},
		{Id: "sub-2", Name: "Bravo"},
		{Id: "sub-3", Name: "Charlie"},
	}

	displayFn := func(s *account.Subscription) string {
		return s.Name
	}

	result := SubscriptionsMatchingFilter(
		subs, []string{"sub-1", "sub-3"}, displayFn,
	)
	require.Equal(t, []string{"Alpha", "Charlie"}, result)
}

func TestSubscriptionsMatchingFilter_EmptyFilter(t *testing.T) {
	subs := []account.Subscription{
		{Id: "sub-1", Name: "Alpha"},
	}

	displayFn := func(s *account.Subscription) string {
		return s.Name
	}

	result := SubscriptionsMatchingFilter(subs, nil, displayFn)
	require.Nil(t, result)
}

func TestMultipleTenantsIndependentFilters(t *testing.T) {
	cfg := config.NewConfig(nil)

	_ = SaveSubscriptionFilter(cfg, "tid-1", []string{"sub-a"})
	_ = SaveSubscriptionFilter(cfg, "tid-2", []string{"sub-b"})

	ids1, exists1 := LoadSubscriptionFilter(cfg, "tid-1")
	require.True(t, exists1)
	require.Equal(t, []string{"sub-a"}, ids1)

	ids2, exists2 := LoadSubscriptionFilter(cfg, "tid-2")
	require.True(t, exists2)
	require.Equal(t, []string{"sub-b"}, ids2)

	// Removing one doesn't affect the other
	_ = RemoveSubscriptionFilter(cfg, "tid-1")
	_, exists1 = LoadSubscriptionFilter(cfg, "tid-1")
	require.False(t, exists1)

	ids2, exists2 = LoadSubscriptionFilter(cfg, "tid-2")
	require.True(t, exists2)
	require.Equal(t, []string{"sub-b"}, ids2)
}
