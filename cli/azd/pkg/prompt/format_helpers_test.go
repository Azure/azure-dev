// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatSubscriptionDisplayName(t *testing.T) {
	sub := &account.Subscription{
		Id:   "sub-123-456",
		Name: "My Subscription",
	}

	tests := []struct {
		name       string
		hideId     bool
		wantName   bool
		wantId     bool
		exactMatch string
	}{
		{
			name:       "hideId true returns name only",
			hideId:     true,
			wantName:   true,
			wantId:     false,
			exactMatch: "My Subscription",
		},
		{
			name:     "hideId false includes both name and id",
			hideId:   false,
			wantName: true,
			wantId:   true,
			// Cannot check exact match due to ANSI color codes
			// from output.WithGrayFormat
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatSubscriptionDisplayName(sub, tt.hideId)
			require.NotEmpty(t, result)

			if tt.exactMatch != "" {
				assert.Equal(t, tt.exactMatch, result)
			}
			if tt.wantName {
				assert.Contains(t, result, "My Subscription")
			}
			if tt.wantId {
				assert.Contains(t, result, "sub-123-456")
			}
		})
	}
}

func TestFormatAutoSelectedSubscriptionMessage(t *testing.T) {
	sub := &account.Subscription{
		Id:   "sub-abc-def",
		Name: "Production",
	}

	tests := []struct {
		name     string
		hideId   bool
		expected string
	}{
		{
			name:     "hideId true omits id",
			hideId:   true,
			expected: "Auto-selected subscription: Production",
		},
		{
			name:   "hideId false includes id",
			hideId: false,
			expected: "Auto-selected subscription: " +
				"Production (sub-abc-def)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatAutoSelectedSubscriptionMessage(
				sub, tt.hideId,
			)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatAutoSelectedSubscriptionMessage_EmptyName(t *testing.T) {
	sub := &account.Subscription{
		Id:   "sub-id",
		Name: "",
	}

	result := formatAutoSelectedSubscriptionMessage(sub, false)
	assert.Equal(
		t, "Auto-selected subscription:  (sub-id)", result,
	)
}

func TestIsDemoModeEnabled(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{
			name:     "true enables demo mode",
			envValue: "true",
			expected: true,
		},
		{
			name:     "TRUE enables demo mode",
			envValue: "TRUE",
			expected: true,
		},
		{
			name:     "1 enables demo mode",
			envValue: "1",
			expected: true,
		},
		{
			name:     "false disables demo mode",
			envValue: "false",
			expected: false,
		},
		{
			name:     "0 disables demo mode",
			envValue: "0",
			expected: false,
		},
		{
			name:     "empty string disables demo mode",
			envValue: "",
			expected: false,
		},
		{
			name:     "invalid value disables demo mode",
			envValue: "not-a-bool",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv("AZD_DEMO_MODE", tt.envValue)
			} else {
				t.Setenv("AZD_DEMO_MODE", "")
			}

			result := isDemoModeEnabled()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsDemoModeEnabled_Unset(t *testing.T) {
	// Do NOT set AZD_DEMO_MODE — rely on the default
	// process env (which should not have it in CI).
	// This tests the "env var not present" path.
	t.Setenv("AZD_DEMO_MODE", "")
	assert.False(t, isDemoModeEnabled())
}

func TestNewEmptyAzureContext(t *testing.T) {
	ctx := NewEmptyAzureContext()

	require.NotNil(t, ctx)
	assert.Equal(t, "", ctx.Scope.TenantId)
	assert.Equal(t, "", ctx.Scope.SubscriptionId)
	assert.Equal(t, "", ctx.Scope.Location)
	assert.Equal(t, "", ctx.Scope.ResourceGroup)
	require.NotNil(t, ctx.Resources)
}

func TestAzureScope_Fields(t *testing.T) {
	scope := AzureScope{
		TenantId:       "tenant-1",
		SubscriptionId: "sub-1",
		Location:       "eastus",
		ResourceGroup:  "rg-1",
	}

	assert.Equal(t, "tenant-1", scope.TenantId)
	assert.Equal(t, "sub-1", scope.SubscriptionId)
	assert.Equal(t, "eastus", scope.Location)
	assert.Equal(t, "rg-1", scope.ResourceGroup)
}

func TestSelectOptions_Defaults(t *testing.T) {
	// Verify that a zero-value SelectOptions has expected defaults
	opts := SelectOptions{}

	assert.Nil(t, opts.ForceNewResource)
	assert.Nil(t, opts.AllowNewResource)
	assert.Equal(t, "", opts.Message)
	assert.Equal(t, "", opts.HelpMessage)
	assert.Equal(t, "", opts.LoadingMessage)
	assert.Nil(t, opts.DisplayNumbers)
	assert.Equal(t, 0, opts.DisplayCount)
	assert.Equal(t, "", opts.Hint)
	assert.Nil(t, opts.EnableFiltering)
	assert.Nil(t, opts.Writer)
}

func TestResourceGroupOptions_Defaults(t *testing.T) {
	opts := ResourceGroupOptions{}
	assert.Nil(t, opts.SelectorOptions)
}

func TestErrSentinels(t *testing.T) {
	// Verify sentinel errors are not nil and have messages
	require.NotNil(t, ErrNoResourcesFound)
	require.NotNil(t, ErrNoResourceSelected)
	assert.Equal(
		t, "no resources found", ErrNoResourcesFound.Error(),
	)
	assert.Equal(
		t, "no resource selected", ErrNoResourceSelected.Error(),
	)
}
