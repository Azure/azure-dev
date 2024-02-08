package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func (cli *azCli) GetResource(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
	apiVersion string,
) (AzCliResourceExtended, error) {
	res, err := cli.resourcesClient.GetByID(ctx, resourceId, apiVersion, nil)
	if err != nil {
		return AzCliResourceExtended{}, fmt.Errorf("getting resource by id: %w", err)
	}

	return AzCliResourceExtended{
		AzCliResource: AzCliResource{
			Id:       *res.ID,
			Name:     *res.Name,
			Type:     *res.Type,
			Location: *res.Location,
		},
		Kind: *res.Kind,
	}, nil
}

func (cli *azCli) ListResourceGroupResources(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	listOptions *ListResourceGroupResourcesOptions,
) ([]AzCliResource, error) {
	// Filter expression on the underlying REST API are different from --query param in az cli.
	// https://learn.microsoft.com/en-us/rest/api/resources/resources/list-by-resource-group#uri-parameters
	options := armresources.ClientListByResourceGroupOptions{}
	if listOptions != nil && *listOptions.Filter != "" {
		options.Filter = listOptions.Filter
	}

	resources := []AzCliResource{}
	pager := cli.resourcesClient.NewListByResourceGroupPager(resourceGroupName, &options)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, resource := range page.ResourceListResult.Value {
			resources = append(resources, AzCliResource{
				Id:       *resource.ID,
				Name:     *resource.Name,
				Type:     *resource.Type,
				Location: *resource.Location,
			})
		}
	}

	return resources, nil
}

func (cli *azCli) ListResourceGroup(
	ctx context.Context,
	subscriptionId string,
	listOptions *ListResourceGroupOptions,
) ([]AzCliResource, error) {
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

	groups := []AzCliResource{}
	pager := cli.resourceGroupsClient.NewListPager(&options)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, group := range page.ResourceGroupListResult.Value {
			groups = append(groups, AzCliResource{
				Id:       *group.ID,
				Name:     *group.Name,
				Type:     *group.Type,
				Location: *group.Location,
			})
		}
	}

	return groups, nil
}

func (cli *azCli) CreateOrUpdateResourceGroup(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	location string,
	tags map[string]*string,
) error {
	_, err := cli.resourceGroupsClient.CreateOrUpdate(ctx, resourceGroupName, armresources.ResourceGroup{
		Location: &location,
		Tags:     tags,
	}, nil)

	return err
}

func (cli *azCli) DeleteResourceGroup(ctx context.Context, subscriptionId string, resourceGroupName string) error {
	poller, err := cli.resourceGroupsClient.BeginDelete(ctx, resourceGroupName, nil)
	if err != nil {
		return fmt.Errorf("beginning resource group deletion: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("deleting resource group: %w", err)
	}

	return nil
}
