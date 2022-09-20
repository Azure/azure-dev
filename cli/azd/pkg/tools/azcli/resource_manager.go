package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func createResourceManagerClient(ctx context.Context, subscriptionId string) (*armresources.Client, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("getting default azure creds: %w", err)
	}

	client, err := armresources.NewClient(subscriptionId, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("creating ARM client: %w", err)
	}

	return client, nil
}

func (cli *azCli) GetResource(ctx context.Context, subscriptionId string, resourceId string) (AzCliResourceExtended, error) {
	client, err := createResourceManagerClient(ctx, subscriptionId)
	if err != nil {
		return AzCliResourceExtended{}, err
	}

	res, err := client.GetByID(ctx, resourceId, "", nil)
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

func (cli *azCli) ListResourceGroupResources(ctx context.Context, subscriptionId string, resourceGroupName string, listOptions *ListResourceGroupResourcesOptions) ([]AzCliResource, error) {
	client, err := createResourceManagerClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := armresources.ClientListByResourceGroupOptions{}
	if listOptions != nil && *listOptions.JmesPathQuery != "" {
		options.Filter = listOptions.JmesPathQuery
	}

	resources := []*armresources.GenericResourceExpanded{}
	pager := client.NewListByResourceGroupPager(resourceGroupName, &options)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		resources = append(resources, page.ResourceListResult.Value...)
	}

	resultResources := []AzCliResource{}
	for _, resource := range resources {
		resultResources = append(resultResources, AzCliResource{
			Id:       *resource.ID,
			Name:     *resource.Name,
			Type:     *resource.Type,
			Location: *resource.Location,
		})
	}

	return resultResources, nil
}
