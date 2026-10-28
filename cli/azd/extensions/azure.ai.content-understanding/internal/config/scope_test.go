// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"errors"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const accountID = "/subscriptions/embedded-sub/resourceGroups/embedded-rg/providers/Microsoft.CognitiveServices/accounts/cu"

func TestResourceScope(t *testing.T) {
	fallback := ResourceScope{SubscriptionID: "ambient-sub", ResourceGroup: "ambient-rg"}
	for _, tt := range []struct {
		name, resource, sub, group string
		want                       ResourceScope
	}{
		{"ambient", "", "", "", fallback},
		{"account name", "cu", "", "", ResourceScope{
			SubscriptionID: "ambient-sub", ResourceGroup: "ambient-rg", ResourceName: "cu",
		}},
		{"explicit", "cu", "explicit-sub", "explicit-rg", ResourceScope{
			SubscriptionID: "explicit-sub", ResourceGroup: "explicit-rg", ResourceName: "cu",
		}},
		{"embedded overrides ambient", accountID, "", "", ResourceScope{
			SubscriptionID: "embedded-sub", ResourceGroup: "embedded-rg", ResourceName: "cu", ResourceID: accountID,
		}},
		{"case insensitive explicit", accountID + "/", "EMBEDDED-SUB", "EMBEDDED-RG", ResourceScope{
			SubscriptionID: "embedded-sub", ResourceGroup: "embedded-rg", ResourceName: "cu", ResourceID: accountID,
		}},
		{"case insensitive structure",
			"/SUBSCRIPTIONS/sub/RESOURCEGROUPS/rg/PROVIDERS/microsoft.cognitiveservices/ACCOUNTS/cu", "", "", ResourceScope{
				SubscriptionID: "sub", ResourceGroup: "rg", ResourceName: "cu",
				ResourceID: "/SUBSCRIPTIONS/sub/RESOURCEGROUPS/rg/PROVIDERS/microsoft.cognitiveservices/ACCOUNTS/cu",
			}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolveResourceScope(tt.resource, tt.sub, tt.group, fallback)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestInvalidResourceScope(t *testing.T) {
	for _, tt := range []struct{ resource, sub, group, code string }{
		{accountID, "other", "", "conflicting_subscription"},
		{accountID, "", "other", "conflicting_resource_group"},
		{"/subscriptions/sub", "", "", "invalid_resource_id"},
		{"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/cu",
			"", "", "invalid_resource_id"},
		{accountID + "/deployments/child", "", "", "invalid_resource_id"},
		{accountID + "?secret=private-value", "", "", "invalid_resource_id"},
		{accountID + "#private-value", "", "", "invalid_resource_id"},
		{accountID + "%2Fprivate-value", "", "", "invalid_resource_id"},
		{accountID + "/..", "", "", "invalid_resource_id"},
		{accountID + "//", "", "", "invalid_resource_id"},
		{accountID + "\n", "", "", "invalid_resource_id"},
		{"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/..",
			"", "", "invalid_resource_id"},
		{"/subscriptions//resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/cu",
			"", "", "invalid_resource_id"},
		{"/subscriptions/sub/resourceGroups/ rg/providers/Microsoft.CognitiveServices/accounts/cu",
			"", "", "invalid_resource_id"},
		{"https://private-value.example", "", "", "invalid_resource_id"},
	} {
		t.Run(tt.resource+tt.sub+tt.group, func(t *testing.T) {
			_, err := ResolveResourceScope(tt.resource, tt.sub, tt.group, ResourceScope{})
			domain, ok := errors.AsType[*cuclient.Error](err)
			require.True(t, ok)
			assert.Equal(t, tt.code, domain.Code)
			assert.NotContains(t, err.Error(), "private-value")
		})
	}
}
