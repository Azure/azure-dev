package devcentersdk

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// Projects
type ProjectListRequestBuilder struct {
	*EntityListRequestBuilder[ProjectListRequestBuilder]
}

func NewProjectListRequestBuilder(c *devCenterClient, devCenter *DevCenter) *ProjectListRequestBuilder {
	builder := &ProjectListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, c, devCenter)

	return builder
}

func (c *ProjectListRequestBuilder) Get(ctx context.Context) (*ProjectListResponse, error) {
	projects, err := c.client.projectListByDevCenter(ctx, c.devCenter)
	if err != nil {
		return nil, err
	}

	return &ProjectListResponse{
		Value: projects,
	}, nil
}

type ProjectItemRequestBuilder struct {
	*EntityItemRequestBuilder[ProjectItemRequestBuilder]
}

func NewProjectItemRequestBuilder(c *devCenterClient, devCenter *DevCenter, projectName string) *ProjectItemRequestBuilder {
	builder := &ProjectItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, c, devCenter, projectName)

	return builder
}

func (c *ProjectItemRequestBuilder) Catalogs() *CatalogListRequestBuilder {
	return NewCatalogListRequestBuilder(c.client, c.devCenter, c.id)
}

func (c *ProjectItemRequestBuilder) CatalogByName(catalogName string) *CatalogItemRequestBuilder {
	return NewCatalogItemRequestBuilder(c.client, c.devCenter, c.id, catalogName)
}

func (c *ProjectItemRequestBuilder) EnvironmentTypes() *EnvironmentTypeListRequestBuilder {
	return NewEnvironmentTypeListRequestBuilder(c.client, c.devCenter, c.id)
}

func (c *ProjectItemRequestBuilder) EnvironmentDefinitions() *EnvironmentDefinitionListRequestBuilder {
	return NewEnvironmentDefinitionListRequestBuilder(c.client, c.devCenter, c.id, "")
}

func (c *ProjectItemRequestBuilder) Environments() *EnvironmentListRequestBuilder {
	return NewEnvironmentListRequestBuilder(c.client, c.devCenter, c.id)
}

func (c *ProjectItemRequestBuilder) EnvironmentsByUser(userId string) *EnvironmentListRequestBuilder {
	builder := NewEnvironmentListRequestBuilder(c.client, c.devCenter, c.id)
	builder.userId = userId

	return builder
}

func (c *ProjectItemRequestBuilder) EnvironmentsByMe() *EnvironmentListRequestBuilder {
	builder := NewEnvironmentListRequestBuilder(c.client, c.devCenter, c.id)
	builder.userId = "me"

	return builder
}

func (c *ProjectItemRequestBuilder) EnvironmentByName(environmentName string) *EnvironmentItemRequestBuilder {
	return NewEnvironmentItemRequestBuilder(c.client, c.devCenter, c.id, environmentName)
}

func (c ProjectItemRequestBuilder) Permissions() *PermissionListRequestBuilder {
	return NewPermissionListRequestBuilder(c.client, c.devCenter, c.id)
}

func (c *ProjectItemRequestBuilder) Get(ctx context.Context) (*Project, error) {
	req, err := c.createRequest(ctx, http.MethodGet, fmt.Sprintf("projects/%s", c.id))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, err
	}

	if !runtime.HasStatusCode(res, http.StatusOK) {
		return nil, runtime.NewResponseError(res)
	}

	project, err := c.client.projectByDevCenter(ctx, c.devCenter, c.id)
	if err != nil {
		return nil, err
	}

	return project, nil
}

//
//nolint:lll
var resourceIdRegex = regexp.MustCompile(
	`\/subscriptions\/(?P<subscriptionId>.+?)\/resourceGroups\/(?P<resourceGroup>.+?)\/providers\/(?P<resourceProvider>.+?)\/(?P<resourcePath>.+?)\/(?P<resourceName>.+)`,
)

func resourceFromId(resourceId string) (*ResourceId, error) {
	// Find matches and extract named values
	match := resourceIdRegex.FindStringSubmatch(resourceId)

	if len(match) == 0 {
		return nil, errors.New("no match found")
	}

	namedValues := make(map[string]string)

	// The first element in the match slice is the entire matched string,
	// so we start the loop from 1 to skip it.
	for i, name := range resourceIdRegex.SubexpNames()[1:] {
		namedValues[name] = match[i+1]
	}

	return &ResourceId{
		Id:             resourceId,
		SubscriptionId: namedValues["subscriptionId"],
		ResourceGroup:  namedValues["resourceGroup"],
		Provider:       namedValues["resourceProvider"],
		ResourcePath:   namedValues["resourcePath"],
		ResourceName:   namedValues["resourceName"],
	}, nil
}
