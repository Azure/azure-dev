// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// ResolveExistingParents narrows creation checks to an existing target group.
// P01 creation and subscription deployment actions retain their subscription scopes.
func ResolveExistingParents(
	ctx context.Context, plan Plan, credential azcore.TokenCredential, options *arm.ClientOptions,
) (Plan, error) {
	client, err := armresources.NewResourceGroupsClient(plan.SubscriptionID, credential, onboardingARMOptions(options))
	if err != nil {
		return plan, err
	}
	_, err = client.Get(ctx, plan.ResourceGroup, nil)
	if responseNotFound(err) {
		return plan, nil
	}
	if err != nil {
		return plan, fmt.Errorf("P08: discover existing resource-group parent: %w", err)
	}
	subscription := "/subscriptions/" + plan.SubscriptionID
	parent := subscription + "/resourceGroups/" + plan.ResourceGroup
	for _, resource := range []*Resource{&plan.Hosting, &plan.Workload, &plan.Storage, &plan.Registry} {
		if !resource.Supplied {
			resource.Parent = parent
		}
	}
	plan.Operations = slices.Clone(plan.Operations)
	for i := range plan.Operations {
		op := &plan.Operations[i]
		switch op.CheckID {
		case "P02", "P03", "P04", "P05":
			if strings.EqualFold(op.Scope, subscription) {
				op.Scope = parent
			}
		case "P09":
			if !strings.HasPrefix(strings.ToLower(op.Action), "microsoft.resources/deployments/") &&
				strings.EqualFold(op.Scope, subscription) {
				op.Scope = parent
			}
		case "P08":
			if strings.EqualFold(op.Scope, subscription) &&
				(op.Action == "Microsoft.Resources/subscriptions/resourceGroups/read" ||
					op.Action == "Microsoft.Storage/storageAccounts/read" ||
					op.Action == "Microsoft.ContainerRegistry/registries/read") {
				op.Scope = parent
			}
		}
	}
	return plan, nil
}
