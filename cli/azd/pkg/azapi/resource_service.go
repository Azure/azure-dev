package azapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
)

type Resource struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
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

func (rs *ResourceService) ListResourceGroupResources(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	listOptions *ListResourceGroupResourcesOptions,
) ([]*Resource, error) {
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

	resources := []*Resource{}
	pager := client.NewListByResourceGroupPager(resourceGroupName, &options)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, resource := range page.ResourceListResult.Value {
			resources = append(resources, &Resource{
				Id:       *resource.ID,
				Name:     *resource.Name,
				Type:     *resource.Type,
				Location: *resource.Location,
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
				Id:       *group.ID,
				Name:     *group.Name,
				Type:     *group.Type,
				Location: *group.Location,
			})
		}
	}

	return groups, nil
}

func (rs *ResourceService) CreateOrUpdateResourceGroup(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	location string,
	tags map[string]*string,
) error {
	client, err := rs.createResourceGroupClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	_, err = client.CreateOrUpdate(ctx, resourceGroupName, armresources.ResourceGroup{
		Location: &location,
		Tags:     tags,
	}, nil)

	return err
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
