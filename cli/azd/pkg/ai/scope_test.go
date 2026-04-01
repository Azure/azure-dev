// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewScope(t *testing.T) {
	tests := []struct {
		name           string
		subscriptionId string
		resourceGroup  string
		workspace      string
	}{
		{
			name:           "all fields populated",
			subscriptionId: "sub-123",
			resourceGroup:  "rg-test",
			workspace:      "ws-prod",
		},
		{
			name:           "empty strings",
			subscriptionId: "",
			resourceGroup:  "",
			workspace:      "",
		},
		{
			name:           "only subscription",
			subscriptionId: "sub-only",
			resourceGroup:  "",
			workspace:      "",
		},
		{
			name:           "realistic azure ids",
			subscriptionId: "00000000-0000-0000-0000-000000000001",
			resourceGroup:  "my-resource-group",
			workspace:      "my-ai-workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope := NewScope(
				tt.subscriptionId,
				tt.resourceGroup,
				tt.workspace,
			)

			require.NotNil(t, scope)
			assert.Equal(t, tt.subscriptionId, scope.SubscriptionId())
			assert.Equal(t, tt.resourceGroup, scope.ResourceGroup())
			assert.Equal(t, tt.workspace, scope.Workspace())
		})
	}
}
