package devcentersdk

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"golang.org/x/exp/slices"
)

// DevCenters
type DevCenterListRequestBuilder struct {
	*EntityListRequestBuilder[DevCenterListRequestBuilder]
}

func (c *DevCenterListRequestBuilder) Projects() *ProjectListRequestBuilder {
	return NewProjectListRequestBuilder(c.client)
}

func NewDevCenterListRequestBuilder(c *devCenterClient) *DevCenterListRequestBuilder {
	builder := &DevCenterListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, c)

	return builder
}

// Gets a list of applications that the current logged in user has access to.
func (c *DevCenterListRequestBuilder) Get(ctx context.Context) (*DevCenterListResponse, error) {
	query := `
	Resources
	| where type in~ ('microsoft.devcenter/projects')
	| where properties['provisioningState'] =~ 'Succeeded'
	| project id, location, tenantId, name, properties, type
	`
	options := azsdk.DefaultClientOptionsBuilder(ctx, http.DefaultClient, "resourcegraph").BuildArmClientOptions()
	resourceGraphClient, err := armresourcegraph.NewClient(c.client.credential, options)
	if err != nil {
		return nil, err
	}

	queryRequest := armresourcegraph.QueryRequest{
		Query: &query,
		Options: &armresourcegraph.QueryRequestOptions{
			AllowPartialScopes: convert.RefOf(true),
		},
	}
	res, err := resourceGraphClient.Resources(ctx, queryRequest, nil)
	if err != nil {
		return nil, err
	}

	devCenters := []*DevCenter{}
	list, ok := res.QueryResponse.Data.([]interface{})
	if !ok {
		return nil, errors.New("error converting data to list")
	}

	for _, item := range list {
		value, ok := item.(map[string]interface{})
		if !ok {
			return nil, errors.New("error converting item to map")
		}

		props, ok := value["properties"].(map[string]interface{})
		if !ok {
			return nil, errors.New("error converting properties to map")
		}

		uri, ok := props["devCenterUri"].(string)
		if !ok {
			continue
		}

		uri = strings.TrimSuffix(uri, "/")

		exists := slices.ContainsFunc(devCenters, func(devCenter *DevCenter) bool {
			return devCenter.ServiceUri == uri
		})

		if !exists {
			id := props["devCenterId"].(string)

			devCenter := &DevCenter{
				Id:         id,
				Name:       filepath.Base(id),
				ServiceUri: uri,
			}
			devCenters = append(devCenters, devCenter)
		}
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
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, c, "")
	c.devCenter = devCenter

	return builder
}

func (c *DevCenterItemRequestBuilder) Projects() *ProjectListRequestBuilder {
	return NewProjectListRequestBuilder(c.client)
}

func (c *DevCenterItemRequestBuilder) ProjectByName(projectName string) *ProjectItemRequestBuilder {
	builder := NewProjectItemRequestBuilder(c.client, projectName)

	return builder
}
