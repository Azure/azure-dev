package devcentersdk

import (
	"context"
)

// DevCenters
type DevCenterListRequestBuilder struct {
	*EntityListRequestBuilder[DevCenterListRequestBuilder]
}

func NewDevCenterListRequestBuilder(c *devCenterClient) *DevCenterListRequestBuilder {
	builder := &DevCenterListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, c, nil)

	return builder
}

// Gets a list of applications that the current logged in user has access to.
func (c *DevCenterListRequestBuilder) Get(ctx context.Context) (*DevCenterListResponse, error) {
	devCenters, err := c.client.devCenterList(ctx)
	if err != nil {
		return nil, err
	}

	return &DevCenterListResponse{
		Value: devCenters,
	}, nil
}

type DevCenterItemRequestBuilder struct {
	*EntityItemRequestBuilder[DevCenterItemRequestBuilder]
}

func NewDevCenterItemRequestBuilder(c *devCenterClient, devCenter *DevCenter) *DevCenterItemRequestBuilder {
	builder := &DevCenterItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, c, devCenter, "")
	builder.devCenter = devCenter

	return builder
}

func (c *DevCenterItemRequestBuilder) Projects() *ProjectListRequestBuilder {
	return NewProjectListRequestBuilder(c.client, c.devCenter)
}

func (c *DevCenterItemRequestBuilder) ProjectByName(projectName string) *ProjectItemRequestBuilder {
	builder := NewProjectItemRequestBuilder(c.client, c.devCenter, projectName)

	return builder
}
