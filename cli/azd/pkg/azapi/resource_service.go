// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

type Resource struct {
	Id        string  `json:"id"`
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	Location  string  `json:"location"`
	ManagedBy *string `json:"managedBy,omitempty"`
}

type ResourceGroup struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
}

type ResourceExtended struct {
	Resource
	Kind string `json:"kind"`
}

// Optional parameters for resource group listing.
type ListResourceGroupOptions struct {
	// An optional tag filter
	TagFilter *Filter
	// An optional filter expression to filter the resource group results
	// https://learn.microsoft.com/en-us/rest/api/resources/resource-groups/list
	Filter *string
}

type Filter struct {
	Key   string
	Value string
}

// Optional parameters for resource group resources listing.
type ListResourceGroupResourcesOptions struct {
	// An optional filter expression to filter the resource list result
	// https://learn.microsoft.com/en-us/rest/api/resources/resources/list-by-resource-group#uri-parameters
	Filter *string
}

type ResourceService struct {
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
}

func NewResourceService(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
) *ResourceService {
	return &ResourceService{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
	}
}

func (rs *ResourceService) GetResource(
	ctx context.Context, subscriptionId string, resourceId string, apiVersion string) (ResourceExtended, error) {
	client, err := rs.createResourcesClient(ctx, subscriptionId)
	if err != nil {
		return ResourceExtended{}, err
	}

	res, err := client.GetByID(ctx, resourceId, apiVersion, nil)
	if err != nil {
		return ResourceExtended{}, fmt.Errorf("getting resource by id: %w", err)
	}

	return ResourceExtended{
		Resource: Resource{
			Id:       *res.ID,
			Name:     *res.Name,
			Type:     *res.Type,
			Location: *res.Location,
		},
		Kind: *res.Kind,
	}, nil
}

func (rs *ResourceService) CheckExistenceByID(
	ctx context.Context, resourceId arm.ResourceID, apiVersion string) (bool, error) {
	client, err := rs.createResourcesClient(ctx, resourceId.SubscriptionID)
	if err != nil {
		return false, err
	}

	response, err := client.CheckExistenceByID(ctx, resourceId.String(), apiVersion, nil)
	if err != nil {
		return false, fmt.Errorf("checking resource existence by id: %w", err)
	}

	return response.Success, nil
}

func (rs *ResourceService) GetRawResource(
	ctx context.Context, resourceId arm.ResourceID, apiVersion string) (string, error) {
	client, err := rs.createResourcesClient(ctx, resourceId.SubscriptionID)
	if err != nil {
		return "", err
	}

	var revisionResponse *http.Response
	ctx = policy.WithCaptureResponse(ctx, &revisionResponse)

	_, err = client.GetByID(ctx, resourceId.String(), apiVersion, nil)
	if err != nil {
		return "", fmt.Errorf("getting resource by id: %w", err)
	}

	body, err := runtime.Payload(revisionResponse)
	if err != nil {
		return "", fmt.Errorf("reading body: %w", err)
	}

	return string(body), nil
}

func (rs *ResourceService) ListResourceGroupResources(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	listOptions *ListResourceGroupResourcesOptions,
) ([]*ResourceExtended, error) {
	client, err := rs.createResourcesClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	// Filter expression on the underlying REST API are different from --query param in az cli.
	// https://learn.microsoft.com/en-us/rest/api/resources/resources/list-by-resource-group#uri-parameters
	options := armresources.ClientListByResourceGroupOptions{}
	if listOptions != nil && *listOptions.Filter != "" {
		options.Filter = listOptions.Filter
	}

	resources := []*ResourceExtended{}
	pager := client.NewListByResourceGroupPager(resourceGroupName, &options)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, resource := range page.ResourceListResult.Value {
			resources = append(resources, &ResourceExtended{
				Resource: Resource{
					Id:       *resource.ID,
					Name:     *resource.Name,
					Type:     *resource.Type,
					Location: *resource.Location,
				},
				Kind: convert.ToValueWithDefault(resource.Kind, ""),
			})
		}
	}

	return resources, nil
}

func (rs *ResourceService) ListResourceGroup(
	ctx context.Context,
	subscriptionId string,
	listOptions *ListResourceGroupOptions,
) ([]*Resource, error) {
	client, err := rs.createResourceGroupClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	// Filter values differ from those support in the --query param of az cli.
	// https://learn.microsoft.com/en-us/rest/api/resources/resource-groups/list
	options := armresources.ResourceGroupsClientListOptions{}
	if listOptions != nil {
		if listOptions.TagFilter != nil {
			tagFilter := fmt.Sprintf(
				"tagName eq '%s' and tagValue eq '%s'",
				listOptions.TagFilter.Key,
				listOptions.TagFilter.Value,
			)
			options.Filter = &tagFilter
		} else if listOptions.Filter != nil {
			options.Filter = listOptions.Filter
		}
	}

	groups := []*Resource{}
	pager := client.NewListPager(&options)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, group := range page.ResourceGroupListResult.Value {
			groups = append(groups, &Resource{
				Id:        *group.ID,
				Name:      *group.Name,
				Type:      *group.Type,
				Location:  *group.Location,
				ManagedBy: group.ManagedBy,
			})
		}
	}

	return groups, nil
}

func (rs *ResourceService) ListSubscriptionResources(
	ctx context.Context,
	subscriptionId string,
	listOptions *armresources.ClientListOptions,
) ([]*ResourceExtended, error) {
	client, err := rs.createResourcesClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	// Filter expression on the underlying REST API are different from --query param in az cli.
	// https://learn.microsoft.com/en-us/rest/api/resources/resources/list-by-resource-group#uri-parameters
	options := armresources.ClientListOptions{}
	if listOptions != nil && *listOptions.Filter != "" {
		options.Filter = listOptions.Filter
	}

	resources := []*ResourceExtended{}
	pager := client.NewListPager(&options)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, resource := range page.ResourceListResult.Value {
			resources = append(resources, &ResourceExtended{
				Resource: Resource{
					Id:       *resource.ID,
					Name:     *resource.Name,
					Type:     *resource.Type,
					Location: *resource.Location,
				},
				Kind: convert.ToValueWithDefault(resource.Kind, ""),
			})
		}
	}

	return resources, nil
}

func (rs *ResourceService) CreateOrUpdateResourceGroup(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	location string,
	tags map[string]*string,
) (*ResourceGroup, error) {
	client, err := rs.createResourceGroupClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	response, err := client.CreateOrUpdate(ctx, resourceGroupName, armresources.ResourceGroup{
		Location: &location,
		Tags:     tags,
	}, nil)

	if err != nil {
		return nil, fmt.Errorf("creating or updating resource group: %w", err)
	}

	return &ResourceGroup{
		Id:       *response.ID,
		Name:     *response.Name,
		Location: *response.Location,
	}, nil
}

func (rs *ResourceService) DeleteResourceGroup(ctx context.Context, subscriptionId string, resourceGroupName string) error {
	client, err := rs.createResourceGroupClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	poller, err := client.BeginDelete(ctx, resourceGroupName, nil)
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode == 404 { // Resource group is already deleted
		return nil
	}

	if err != nil {
		return fmt.Errorf("beginning resource group deletion: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("deleting resource group: %w", err)
	}

	return nil
}

func (rs *ResourceService) createResourcesClient(ctx context.Context, subscriptionId string) (*armresources.Client, error) {
	credential, err := rs.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armresources.NewClient(subscriptionId, credential, rs.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return client, nil
}

func (rs *ResourceService) createResourceGroupClient(
	ctx context.Context,
	subscriptionId string,
) (*armresources.ResourceGroupsClient, error) {
	credential, err := rs.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armresources.NewResourceGroupsClient(subscriptionId, credential, rs.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating ResourceGroup client: %w", err)
	}

	return client, nil
}

// GroupByResourceGroup creates a map of resources group by their resource group name.
// The key is the resource group name and the value is a list of resources in that group.
func GroupByResourceGroup(resources []*armresources.ResourceReference) (map[string][]*Resource, error) {
	resourceMap := map[string][]*Resource{}

	for _, resource := range resources {
		resourceId, err := arm.ParseResourceID(*resource.ID)
		if err != nil {
			return nil, err
		}

		if resourceId.ResourceGroupName == "" {
			continue
		}

		groupResources, exists := resourceMap[resourceId.ResourceGroupName]
		if !exists {
			groupResources = []*Resource{}
		}

		resourceType := resourceId.ResourceType.String()
		if resourceType != string(AzureResourceTypeResourceGroup) {
			groupResources = append(groupResources, &Resource{
				Id:       *resource.ID,
				Name:     resourceId.Name,
				Type:     resourceType,
				Location: resourceId.Location,
			})
		}

		resourceMap[resourceId.ResourceGroupName] = groupResources
	}

	return resourceMap, nil
}
