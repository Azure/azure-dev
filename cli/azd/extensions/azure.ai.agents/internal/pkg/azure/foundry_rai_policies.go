// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	armcognitiveservices "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
)

// raiPolicyIDTemplate is the ARM resource ID shape the agent API requires for
// `rai_config.rai_policy_name`. The service rejects a bare policy name, so azd
// always writes and compares the full ID.
const raiPolicyIDTemplate = "/subscriptions/%s/resourceGroups/%s/providers/" +
	"Microsoft.CognitiveServices/accounts/%s/raiPolicies/%s"

// RaiPolicyInfo describes one Responsible AI policy on a Foundry (Cognitive
// Services) account.
type RaiPolicyInfo struct {
	Name string
	// ResourceID is the full ARM ID, which is the form the agent API accepts.
	ResourceID string
	// BasePolicyName is the policy this one derives from, e.g.
	// "Microsoft.DefaultV2". Empty when the service does not report one.
	BasePolicyName string
	// SystemManaged is true for the service-supplied defaults every account
	// carries. They are attachable but cannot be edited, so init presents them
	// separately from the policies a user authored.
	SystemManaged bool
}

// RaiPolicyRef is a RAI policy's ARM resource ID decomposed into the parts the
// control-plane client needs.
type RaiPolicyRef struct {
	SubscriptionID string
	ResourceGroup  string
	AccountName    string
	PolicyName     string
}

// RaiPolicyResourceID builds the full ARM resource ID for a policy.
func RaiPolicyResourceID(subscriptionID, resourceGroup, accountName, policyName string) string {
	return fmt.Sprintf(
		raiPolicyIDTemplate,
		strings.TrimSpace(subscriptionID),
		strings.TrimSpace(resourceGroup),
		strings.TrimSpace(accountName),
		strings.TrimSpace(policyName),
	)
}

// ParseRaiPolicyResourceID decomposes a RAI policy ARM resource ID. It reports
// false for any value that is not a well-formed policy ID, including a bare
// policy name and an ID that still contains an unexpanded ${VAR} reference.
//
// Segment names are matched case-insensitively because ARM echoes resource IDs
// back with the casing the caller used, and portal-copied IDs vary.
func ParseRaiPolicyResourceID(id string) (RaiPolicyRef, bool) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(id), "/"), "/")
	if len(parts) != 10 {
		return RaiPolicyRef{}, false
	}
	expected := map[int]string{
		0: "subscriptions",
		2: "resourcegroups",
		4: "providers",
		5: "microsoft.cognitiveservices",
		6: "accounts",
		8: "raipolicies",
	}
	for i, want := range expected {
		if !strings.EqualFold(parts[i], want) {
			return RaiPolicyRef{}, false
		}
	}
	ref := RaiPolicyRef{
		SubscriptionID: parts[1],
		ResourceGroup:  parts[3],
		AccountName:    parts[7],
		PolicyName:     parts[9],
	}
	if ref.SubscriptionID == "" || ref.ResourceGroup == "" || ref.AccountName == "" || ref.PolicyName == "" {
		return RaiPolicyRef{}, false
	}
	return ref, true
}

// ListRaiPolicies returns every RAI policy on a Foundry account, including the
// service-supplied defaults.
func ListRaiPolicies(
	ctx context.Context,
	credential azcore.TokenCredential,
	subscriptionID, resourceGroup, accountName string,
) ([]RaiPolicyInfo, error) {
	client, err := armcognitiveservices.NewRaiPoliciesClient(subscriptionID, credential, NewArmClientOptions())
	if err != nil {
		return nil, fmt.Errorf("creating RAI policies client: %w", err)
	}

	pager := client.NewListPager(resourceGroup, accountName, nil)
	var results []RaiPolicyInfo
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing RAI policies on account %q: %w", accountName, err)
		}
		for _, policy := range page.Value {
			if policy == nil || policy.Name == nil || *policy.Name == "" {
				continue
			}
			info := RaiPolicyInfo{
				Name:       *policy.Name,
				ResourceID: RaiPolicyResourceID(subscriptionID, resourceGroup, accountName, *policy.Name),
			}
			// Prefer the ID the service reports; it is authoritative for casing.
			if policy.ID != nil && *policy.ID != "" {
				info.ResourceID = *policy.ID
			}
			if policy.Properties != nil {
				if policy.Properties.BasePolicyName != nil {
					info.BasePolicyName = *policy.Properties.BasePolicyName
				}
				if policy.Properties.Type != nil {
					info.SystemManaged = *policy.Properties.Type == armcognitiveservices.RaiPolicyTypeSystemManaged
				}
			}
			results = append(results, info)
		}
	}
	return results, nil
}
