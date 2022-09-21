package azcli

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
)

func (cli *azCli) GetResource(ctx context.Context, subscriptionId string, resourceId string) (AzCliResourceExtended, error) {
	client, err := cli.createResourcesClient(ctx, subscriptionId)
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
	client, err := cli.createResourcesClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := armresources.ClientListByResourceGroupOptions{}
	if listOptions != nil && *listOptions.JmesPathQuery != "" {
		options.Filter = listOptions.JmesPathQuery
	}

	resources := []AzCliResource{}
	pager := client.NewListByResourceGroupPager(resourceGroupName, &options)
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

func (cli *azCli) ListResourceGroup(ctx context.Context, subscriptionId string, listOptions *ListResourceGroupOptions) ([]AzCliResource, error) {
	client, err := cli.createResourceGroupClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := armresources.ResourceGroupsClientListOptions{}
	if listOptions != nil {
		if listOptions.TagFilter != nil {
			tagFilter := fmt.Sprintf("$filter=%s = eq '%s'", listOptions.TagFilter.Key, listOptions.TagFilter.Value)
			options.Filter = &tagFilter
		} else if listOptions.JmesPathQuery != nil {
			queryFilter := fmt.Sprintf("$filter=%p", listOptions.JmesPathQuery)
			options.Filter = &queryFilter
		}
	}

	// TODO: Implement these filters.
	// if listOptions != nil {
	// 	if listOptions.TagFilter != nil {
	// 		args = append(args, "--tag", fmt.Sprintf("%s=%s", listOptions.TagFilter.Key, listOptions.TagFilter.Value))
	// 	}

	// 	if listOptions.JmesPathQuery != nil {
	// 		args = append(args, "--query", *listOptions.JmesPathQuery)
	// 	}
	// }

	groups := []AzCliResource{}
	pager := client.NewListPager(&options)

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

func (cli *azCli) DeleteResourceGroup(ctx context.Context, subscriptionId string, resourceGroupName string) error {
	client, err := cli.createResourceGroupClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	poller, err := client.BeginDelete(ctx, resourceGroupName, nil)
	if err != nil {
		return fmt.Errorf("beginning resource group deletion: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("deleting resource group: %w", err)
	}

	return nil
}

func (cli *azCli) createResourcesClient(ctx context.Context, subscriptionId string) (*armresources.Client, error) {
	cred, err := identity.GetCredentials(ctx)
	if err != nil {
		return nil, err
	}

	options := cli.createClientOptions(ctx)
	client, err := armresources.NewClient(subscriptionId, cred, options)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return client, nil
}

func (cli *azCli) createResourceGroupClient(ctx context.Context, subscriptionId string) (*armresources.ResourceGroupsClient, error) {
	cred, err := identity.GetCredentials(ctx)
	if err != nil {
		return nil, err
	}

	options := cli.createClientOptions(ctx)
	client, err := armresources.NewResourceGroupsClient(subscriptionId, cred, options)
	if err != nil {
		return nil, fmt.Errorf("creating ResourceGroup client: %w", err)
	}

	return client, nil
}

// Creates the module client options
// These options include the underlying transport to be used.
func (cli *azCli) createClientOptions(ctx context.Context) *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			// Supports mocking for unit tests
			Transport: httputil.GetHttpClient(ctx),
			// Per request policies to inject into HTTP pipeline
			PerCallPolicies: []policy.Policy{
				NewUserAgentPolicy(cli.userAgent),
			},
		},
	}
}

type userAgentPolicy struct {
	userAgent string
}

// Policy to ensure the AZD custom user agent is set on all HTTP requests.
func NewUserAgentPolicy(userAgent string) policy.Policy {
	return &userAgentPolicy{
		userAgent: userAgent,
	}
}

// Sets the custom user-agent string on the underlying request
func (p *userAgentPolicy) Do(req *policy.Request) (*http.Response, error) {
	if strings.TrimSpace(p.userAgent) != "" {
		req.Raw().Header.Set("User-Agent", p.userAgent)
	}
	return req.Next()
}
