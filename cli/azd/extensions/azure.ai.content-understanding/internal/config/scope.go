// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"strings"
	"unicode"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

type ResourceScope struct {
	SubscriptionID string
	ResourceGroup  string
	ResourceID     string
	ResourceName   string
}

// ResolveResourceScope gives full account IDs authority over ambient scope, not conflicting explicit options.
func ResolveResourceScope(resource, subscription, group string, fallback ResourceScope) (ResourceScope, error) {
	result := ResourceScope{
		SubscriptionID: first(subscription, fallback.SubscriptionID),
		ResourceGroup:  first(group, fallback.ResourceGroup),
		ResourceName:   resource,
	}
	if !strings.Contains(resource, "/") {
		return result, nil
	}
	value := strings.TrimSuffix(resource, "/")
	id, err := arm.ParseResourceID(value)
	if err != nil || len(strings.Split(value, "/")) != 9 || strings.ContainsAny(value, "?#%\\") ||
		strings.ContainsFunc(value, unicode.IsControl) ||
		!strings.EqualFold(id.ResourceType.String(), "Microsoft.CognitiveServices/accounts") ||
		id.SubscriptionID == "" || id.ResourceGroupName == "" {
		return ResourceScope{}, invalid("invalid_resource_id",
			"Specify a full Cognitive Services account ARM resource ID.")
	}
	for _, part := range strings.Split(value, "/")[1:] {
		if part == "" || part == "." || part == ".." || strings.TrimSpace(part) != part {
			return ResourceScope{}, invalid("invalid_resource_id",
				"Specify a full Cognitive Services account ARM resource ID.")
		}
	}
	if subscription != "" && !strings.EqualFold(subscription, id.SubscriptionID) {
		return ResourceScope{}, invalid("conflicting_subscription",
			"The explicit subscription conflicts with the subscription embedded in the resource ID.")
	}
	if group != "" && !strings.EqualFold(group, id.ResourceGroupName) {
		return ResourceScope{}, invalid("conflicting_resource_group",
			"The explicit resource group conflicts with the resource group embedded in the resource ID.")
	}
	return ResourceScope{
		SubscriptionID: id.SubscriptionID, ResourceGroup: id.ResourceGroupName,
		ResourceID: value, ResourceName: id.Name,
	}, nil
}
