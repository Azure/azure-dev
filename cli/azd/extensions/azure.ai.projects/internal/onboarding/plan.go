// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// ParseID validates the exact ARM type and complete subscription/RG hierarchy.
func ParseID(value, resourceType string) (*arm.ResourceID, error) {
	if !validResourceIDSyntax(value) {
		return nil, fmt.Errorf(
			"expected a complete %s resource ID without URL credentials or extra components", resourceType)
	}
	id, err := arm.ParseResourceID(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s resource ID: %w", resourceType, err)
	}
	segments := 9
	if strings.EqualFold(resourceType, "Microsoft.Network/virtualNetworks/subnets") {
		segments = 11
	}
	if id.SubscriptionID == "" || id.ResourceGroupName == "" || id.Name == "" ||
		!strings.EqualFold(id.ResourceType.String(), resourceType) || len(strings.Split(value, "/")) != segments {
		return nil, fmt.Errorf("expected a complete %s resource ID", resourceType)
	}
	return id, nil
}

func validResourceIDSyntax(value string) bool {
	return strings.HasPrefix(strings.ToLower(value), "/subscriptions/") &&
		!strings.ContainsAny(value, "?#%\\@:\x00 \t\r\n") && !strings.Contains(value, "//")
}
