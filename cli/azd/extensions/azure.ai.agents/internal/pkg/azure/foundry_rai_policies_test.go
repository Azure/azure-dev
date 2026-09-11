// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRaiPolicyResourceID verifies the ID is assembled in the form the agent
// API accepts.
func TestRaiPolicyResourceID(t *testing.T) {
	t.Parallel()

	got := RaiPolicyResourceID("sub-1", "my-rg", "my-account", "my-policy")
	require.Equal(t,
		"/subscriptions/sub-1/resourceGroups/my-rg/providers/"+
			"Microsoft.CognitiveServices/accounts/my-account/raiPolicies/my-policy",
		got,
	)
}

// TestParseRaiPolicyResourceID covers the values a developer can end up with in
// agent.yaml: a real ID, a bare policy name, an unexpanded ${VAR} reference,
// and IDs that point at something other than a RAI policy.
func TestParseRaiPolicyResourceID(t *testing.T) {
	t.Parallel()

	valid := "/subscriptions/sub-1/resourceGroups/my-rg/providers/" +
		"Microsoft.CognitiveServices/accounts/my-account/raiPolicies/my-policy"

	tests := []struct {
		name string
		id   string
		ok   bool
		want RaiPolicyRef
	}{
		{
			name: "full resource id",
			id:   valid,
			ok:   true,
			want: RaiPolicyRef{
				SubscriptionID: "sub-1", ResourceGroup: "my-rg",
				AccountName: "my-account", PolicyName: "my-policy",
			},
		},
		{
			// ARM path segments are not case sensitive and the portal, the CLI
			// and the SDK each spell them differently.
			name: "mixed case segments",
			id: "/SUBSCRIPTIONS/sub-1/RESOURCEGROUPS/my-rg/PROVIDERS/" +
				"microsoft.cognitiveservices/ACCOUNTS/my-account/RAIPOLICIES/my-policy",
			ok: true,
			want: RaiPolicyRef{
				SubscriptionID: "sub-1", ResourceGroup: "my-rg",
				AccountName: "my-account", PolicyName: "my-policy",
			},
		},
		{name: "bare policy name", id: "Microsoft.DefaultV2"},
		{name: "unexpanded reference", id: "${RAI_POLICY_ID}"},
		{name: "empty", id: ""},
		{
			name: "account id without policy",
			id: "/subscriptions/sub-1/resourceGroups/my-rg/providers/" +
				"Microsoft.CognitiveServices/accounts/my-account",
		},
		{
			name: "wrong resource type",
			id: "/subscriptions/sub-1/resourceGroups/my-rg/providers/" +
				"Microsoft.CognitiveServices/accounts/my-account/deployments/my-deployment",
		},
		{
			name: "wrong provider",
			id: "/subscriptions/sub-1/resourceGroups/my-rg/providers/" +
				"Microsoft.Storage/accounts/my-account/raiPolicies/my-policy",
		},
		{
			name: "empty segment",
			id: "/subscriptions//resourceGroups/my-rg/providers/" +
				"Microsoft.CognitiveServices/accounts/my-account/raiPolicies/my-policy",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, ok := ParseRaiPolicyResourceID(test.id)
			require.Equal(t, test.ok, ok)
			require.Equal(t, test.want, got)
		})
	}
}

// TestParseRaiPolicyResourceIDRoundTrip verifies the two helpers agree, so an
// ID built by init is always recognized by the deploy-time verification.
func TestParseRaiPolicyResourceIDRoundTrip(t *testing.T) {
	t.Parallel()

	ref := RaiPolicyRef{
		SubscriptionID: "sub-1", ResourceGroup: "my-rg",
		AccountName: "my-account", PolicyName: "my-policy",
	}

	parsed, ok := ParseRaiPolicyResourceID(
		RaiPolicyResourceID(ref.SubscriptionID, ref.ResourceGroup, ref.AccountName, ref.PolicyName),
	)
	require.True(t, ok)
	require.Equal(t, ref, parsed)
}
