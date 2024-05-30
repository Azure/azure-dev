package devcentersdk

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

type PermissionListRequestBuilder struct {
	*EntityListRequestBuilder[PermissionListRequestBuilder]
	projectName string
}

func NewPermissionListRequestBuilder(
	c *devCenterClient,
	devCenter *DevCenter,
	projectName string,
) *PermissionListRequestBuilder {
	builder := &PermissionListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, c, devCenter)
	builder.projectName = projectName

	return builder
}

func (c *PermissionListRequestBuilder) Get(ctx context.Context) ([]*armauthorization.Permission, error) {
	project, err := c.client.projectByDevCenter(ctx, c.devCenter, c.projectName)
	if err != nil {
		return nil, err
	}

	options := azsdk.NewClientOptionsBuilderFactory(c.client.options.Transport, "azd", c.client.cloud).
		NewClientOptionsBuilder().
		WithPerCallPolicy(azsdk.NewMsCorrelationPolicy()).
		BuildArmClientOptions()
	permissionsClient, err := armauthorization.NewPermissionsClient(project.SubscriptionId, c.client.credential, options)
	if err != nil {
		return nil, err
	}

	pager := permissionsClient.NewListForResourcePager(
		project.ResourceGroup,
		"Microsoft.DevCenter",
		"projects",
		"",
		project.Name,
		nil,
	)

	permissions := []*armauthorization.Permission{}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed getting next page of subscriptions: %w", err)
		}

		permissions = append(permissions, page.Value...)
	}

	return permissions, nil
}

func (c *PermissionListRequestBuilder) HasWriteAccess(ctx context.Context) bool {
	return c.hasPermission(ctx, "Microsoft.DevCenter/projects/users/environments/userWrite/action")
}

func (c *PermissionListRequestBuilder) HasDeleteAccess(ctx context.Context) bool {
	return c.hasPermission(ctx, "Microsoft.DevCenter/projects/users/environments/userDelete/action")
}

func (c *PermissionListRequestBuilder) hasPermission(ctx context.Context, permission string) bool {
	permissions, err := c.Get(ctx)
	if err != nil {
		return false
	}

	return slices.ContainsFunc(permissions, func(p *armauthorization.Permission) bool {
		return slices.ContainsFunc(p.DataActions, func(action *string) bool {
			return strings.EqualFold(*action, permission)
		})
	})
}
