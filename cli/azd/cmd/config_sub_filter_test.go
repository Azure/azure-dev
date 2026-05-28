// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestSubFilterSetAction_SingleTenant_SavesFilter(t *testing.T) {
	mockConsole := mockinput.NewMockConsole()
	ucm := newTestUserConfigManager(t)

	mockAccount := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{Id: "sub-1", Name: "Alpha", TenantId: "t1", UserAccessTenantId: "t1"},
			{Id: "sub-2", Name: "Bravo", TenantId: "t1", UserAccessTenantId: "t1"},
		},
	}

	action := &subFilterSetAction{
		accountManager:    mockAccount,
		userConfigManager: ucm,
		console:           mockConsole,
	}

	// Register MultiSelect response — pick the first option by label
	mockConsole.WhenMultiSelect(func(opts input.ConsoleOptions) bool {
		return strings.Contains(opts.Message, "subscription")
	}).RespondFn(func(opts input.ConsoleOptions) (any, error) {
		if len(opts.Options) > 0 {
			return []string{opts.Options[0]}, nil
		}
		return []string{}, nil
	})

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, result.Message.Header, "Saved subscription filter")

	// Verify filter was persisted
	cfg, err := ucm.Load()
	require.NoError(t, err)
	ids, exists := prompt.LoadSubscriptionFilter(cfg, "t1")
	require.True(t, exists)
	require.Len(t, ids, 1)
}

func TestSubFilterSetAction_EmptySelection_NoOp(t *testing.T) {
	mockConsole := mockinput.NewMockConsole()
	ucm := newTestUserConfigManager(t)

	mockAccount := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{Id: "sub-1", Name: "Alpha", TenantId: "t1", UserAccessTenantId: "t1"},
		},
	}

	action := &subFilterSetAction{
		accountManager:    mockAccount,
		userConfigManager: ucm,
		console:           mockConsole,
	}

	// Register MultiSelect response — empty selection
	mockConsole.WhenMultiSelect(func(opts input.ConsoleOptions) bool {
		return true
	}).Respond([]string{})

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)

	// Verify no filter was saved
	cfg, err := ucm.Load()
	require.NoError(t, err)
	_, exists := prompt.LoadSubscriptionFilter(cfg, "t1")
	require.False(t, exists)
}

func TestSubFilterRemoveAction_NoExistingFilter_NoOp(t *testing.T) {
	mockConsole := mockinput.NewMockConsole()
	ucm := newTestUserConfigManager(t)

	mockAccount := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{Id: "sub-1", Name: "Alpha", TenantId: "t1", UserAccessTenantId: "t1"},
		},
	}

	action := &subFilterRemoveAction{
		accountManager:    mockAccount,
		userConfigManager: ucm,
		console:           mockConsole,
	}

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestSubFilterRemoveAction_ConfirmedRemoval(t *testing.T) {
	mockConsole := mockinput.NewMockConsole()
	ucm := newTestUserConfigManager(t)

	// Pre-save a filter
	cfg, err := ucm.Load()
	require.NoError(t, err)
	err = prompt.SaveSubscriptionFilter(cfg, "t1", []string{"sub-1"})
	require.NoError(t, err)
	err = ucm.Save(cfg)
	require.NoError(t, err)

	mockAccount := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{Id: "sub-1", Name: "Alpha", TenantId: "t1", UserAccessTenantId: "t1"},
		},
	}

	action := &subFilterRemoveAction{
		accountManager:    mockAccount,
		userConfigManager: ucm,
		console:           mockConsole,
	}

	// Confirm removal
	mockConsole.WhenConfirm(func(opts input.ConsoleOptions) bool {
		return strings.Contains(opts.Message, "Remove subscription filter")
	}).Respond(true)

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, result.Message.Header, "Removed subscription filter")

	// Verify filter was removed
	cfg, err = ucm.Load()
	require.NoError(t, err)
	_, exists := prompt.LoadSubscriptionFilter(cfg, "t1")
	require.False(t, exists)
}

func TestSubFilterRemoveAction_CancelledRemoval(t *testing.T) {
	mockConsole := mockinput.NewMockConsole()
	ucm := newTestUserConfigManager(t)

	// Pre-save a filter
	cfg, err := ucm.Load()
	require.NoError(t, err)
	err = prompt.SaveSubscriptionFilter(cfg, "t1", []string{"sub-1"})
	require.NoError(t, err)
	err = ucm.Save(cfg)
	require.NoError(t, err)

	mockAccount := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{Id: "sub-1", Name: "Alpha", TenantId: "t1", UserAccessTenantId: "t1"},
		},
	}

	action := &subFilterRemoveAction{
		accountManager:    mockAccount,
		userConfigManager: ucm,
		console:           mockConsole,
	}

	// Decline removal
	mockConsole.WhenConfirm(func(opts input.ConsoleOptions) bool {
		return true
	}).Respond(false)

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)

	// Verify filter is still there
	cfg, err = ucm.Load()
	require.NoError(t, err)
	ids, exists := prompt.LoadSubscriptionFilter(cfg, "t1")
	require.True(t, exists)
	require.Equal(t, []string{"sub-1"}, ids)
}
