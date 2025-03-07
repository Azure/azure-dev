// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"context"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
)

// Azure Scope contains the high level metadata about an Azure deployment.
type AzureScope struct {
	TenantId       string
	SubscriptionId string
	Location       string
	ResourceGroup  string
}

// AzureResourceList contains a list of Azure resources.
type AzureResourceList struct {
	resourceService ResourceService
	resources       []*arm.ResourceID
}

// NewAzureResourceList creates a new Azure resource list.
func NewAzureResourceList(resourceService ResourceService, resources []*arm.ResourceID) *AzureResourceList {
	if resources == nil {
		resources = []*arm.ResourceID{}
	}

	return &AzureResourceList{
		resourceService: resourceService,
		resources:       resources,
	}
}

// Add adds an Azure resource to the list.
func (arl *AzureResourceList) Add(resourceId string) error {
	if resourceId == "new" {
		return nil
	}

	if _, has := arl.FindById(resourceId); has {
		return nil
	}

	parsedResource, err := arm.ParseResourceID(resourceId)
	if err != nil {
		return err
	}

	arl.resources = append(arl.resources, parsedResource)

	return nil
}

// Find finds the first Azure resource matched by the predicate function.
func (arl *AzureResourceList) Find(predicate func(resourceId *arm.ResourceID) bool) (*arm.ResourceID, bool) {
	for _, resource := range arl.resources {
		if predicate(resource) {
			return resource, true
		}
	}

	return nil, false
}

// FindAll finds all Azure resources matched by the predicate function.
func (arl *AzureResourceList) FindAll(predicate func(resourceId *arm.ResourceID) bool) ([]*arm.ResourceID, bool) {
	matches := []*arm.ResourceID{}

	for _, resource := range arl.resources {
		if predicate(resource) {
			matches = append(matches, resource)
		}
	}

	return matches, len(matches) > 0
}

// FindByType finds the first Azure resource by the specified type.
func (arl *AzureResourceList) FindByType(resourceType azapi.AzureResourceType) (*arm.ResourceID, bool) {
	return arl.Find(func(resourceId *arm.ResourceID) bool {
		return strings.EqualFold(resourceId.ResourceType.String(), string(resourceType))
	})
}

// FindAllByType finds all Azure resources by the specified type.
func (arl *AzureResourceList) FindAllByType(resourceType azapi.AzureResourceType) ([]*arm.ResourceID, bool) {
	return arl.FindAll(func(resourceId *arm.ResourceID) bool {
		return strings.EqualFold(resourceId.ResourceType.String(), string(resourceType))
	})
}

// FindByTypeAndKind finds the first Azure resource by the specified type and kind.
func (arl *AzureResourceList) FindByTypeAndKind(
	ctx context.Context,
	resourceType azapi.AzureResourceType,
	kinds []string,
) (*arm.ResourceID, bool) {
	typeMatches, has := arl.FindAllByType(resourceType)
	if !has {
		return nil, false
	}

	// When no kinds are specified, return the first resource found
	if len(kinds) == 0 {
		return typeMatches[0], true
	}

	// When kinds are specified, check if the resource kind matches
	for _, typeMatch := range typeMatches {
		resource, err := arl.resourceService.GetResource(ctx, typeMatch.SubscriptionID, typeMatch.String(), "")
		if err != nil {
			return nil, false
		}

		for _, kind := range kinds {
			if strings.EqualFold(kind, resource.Kind) {
				return typeMatch, true
			}
		}
	}

	return nil, false
}

// FindById finds the first Azure resource by the specified ID.
func (arl *AzureResourceList) FindById(resourceId string) (*arm.ResourceID, bool) {
	return arl.Find(func(resource *arm.ResourceID) bool {
		return strings.EqualFold(resource.String(), resourceId)
	})
}

// FindByTypeAndName finds the first Azure resource by the specified type and name.
func (arl *AzureResourceList) FindByTypeAndName(
	resourceType azapi.AzureResourceType,
	resourceName string,
) (*arm.ResourceID, bool) {
	return arl.Find(func(resource *arm.ResourceID) bool {
		return strings.EqualFold(resource.ResourceType.String(), string(resourceType)) &&
			strings.EqualFold(resource.Name, resourceName)
	})
}

// AzureContext contains the Scope and list of Resources from an Azure deployment.
type AzureContext struct {
	Scope         AzureScope
	Resources     *AzureResourceList
	promptService PromptService
}

// NewEmptyAzureContext creates a new empty Azure context.
func NewEmptyAzureContext() *AzureContext {
	return &AzureContext{
		Scope:     AzureScope{},
		Resources: &AzureResourceList{},
	}
}

// NewAzureContext creates a new Azure context.
func NewAzureContext(
	promptService PromptService,
	scope AzureScope,
	resourceList *AzureResourceList,
) *AzureContext {
	return &AzureContext{
		Scope:         scope,
		Resources:     resourceList,
		promptService: promptService,
	}
}

// EnsureSubscription ensures that the Azure context has a subscription.
// If the subscription is not set, the user is prompted to select a subscription.
func (pc *AzureContext) EnsureSubscription(ctx context.Context) error {
	if pc.Scope.SubscriptionId == "" {
		subscription, err := pc.promptService.PromptSubscription(ctx, nil)
		if err != nil {
			return err
		}

		pc.Scope.TenantId = subscription.TenantId
		pc.Scope.SubscriptionId = subscription.Id
	}

	return nil
}

// EnsureResourceGroup ensures that the Azure context has a resource group.
// If the resource group is not set, the user is prompted to select a resource group.
func (pc *AzureContext) EnsureResourceGroup(ctx context.Context) error {
	if pc.Scope.ResourceGroup == "" {
		resourceGroup, err := pc.promptService.PromptResourceGroup(ctx, pc, nil)
		if err != nil {
			return err
		}

		pc.Scope.ResourceGroup = resourceGroup.Name
	}

	return nil
}

// EnsureLocation ensures that the Azure context has a location.
// If the location is not set, the user is prompted to select a location.
func (pc *AzureContext) EnsureLocation(ctx context.Context) error {
	if pc.Scope.Location == "" {
		location, err := pc.promptService.PromptLocation(ctx, pc, nil)
		if err != nil {
			return err
		}

		pc.Scope.Location = location.Name
	}

	return nil
}
