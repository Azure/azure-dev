// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ComposeService struct {
	azdext.UnimplementedComposeServiceServer

	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext]
}

func NewComposeService(
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
) azdext.ComposeServiceServer {
	return &ComposeService{
		lazyAzdContext: lazyAzdContext,
	}
}

// AddResource implements azdext.ComposeServiceServer.
func (c *ComposeService) AddResource(
	ctx context.Context,
	req *azdext.AddResourceRequest,
) (*azdext.AddResourceResponse, error) {
	azdContext, err := c.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := project.Load(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

	if projectConfig.Resources == nil {
		projectConfig.Resources = make(map[string]*project.ResourceConfig)
	}

	resourceProps, err := createResourceProps(req.Resource.Type, req.Resource.Config)
	if err != nil {
		return nil, fmt.Errorf("creating resource props: %w", err)
	}

	projectConfig.Resources[req.Resource.Name] = &project.ResourceConfig{
		Name:  req.Resource.Name,
		Type:  project.ResourceType(req.Resource.Type),
		Props: resourceProps,
		Uses:  req.Resource.Uses,
	}

	if err := project.Save(ctx, projectConfig, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.AddResourceResponse{
		Resource: req.Resource,
	}, nil
}

func createResourceProps(resourceType string, config []byte) (any, error) {
	switch project.ResourceType(resourceType) {
	case project.ResourceTypeHostContainerApp:
		props := project.ContainerAppProps{}
		if len(config) == 0 {
			return props, nil
		}
		if err := json.Unmarshal(config, &props); err != nil {
			return nil, err
		}
		return props, nil
	case project.ResourceTypeDbCosmos:
		props := project.CosmosDBProps{}
		if len(config) == 0 {
			return props, nil
		}
		if err := json.Unmarshal(config, &props); err != nil {
			return nil, err
		}
		return props, nil
	case project.ResourceTypeStorage:
		props := project.StorageProps{}
		if len(config) == 0 {
			return props, nil
		}
		if err := json.Unmarshal(config, &props); err != nil {
			return nil, err
		}
		return props, nil
	case project.ResourceTypeAiProject:
		props := project.AiFoundryModelProps{}
		if len(config) == 0 {
			return props, nil
		}
		if err := json.Unmarshal(config, &props); err != nil {
			return nil, err
		}
		return props, nil
	case project.ResourceTypeDbMongo:
		props := project.CosmosDBProps{}
		if len(config) == 0 {
			return props, nil
		}
		if err := json.Unmarshal(config, &props); err != nil {
			return nil, err
		}
		return props, nil
	case project.ResourceTypeMessagingEventHubs:
		props := project.EventHubsProps{}
		if len(config) == 0 {
			return props, nil
		}
		if err := json.Unmarshal(config, &props); err != nil {
			return nil, err
		}
		return props, nil
	case project.ResourceTypeMessagingServiceBus:
		props := project.ServiceBusProps{}
		if len(config) == 0 {
			return props, nil
		}
		if err := json.Unmarshal(config, &props); err != nil {
			return nil, err
		}
		return props, nil
	default:
		return nil, nil
	}
}

// GetResource implements azdext.ComposeServiceServer.
func (c *ComposeService) GetResource(
	ctx context.Context,
	req *azdext.GetResourceRequest,
) (*azdext.GetResourceResponse, error) {
	azdContext, err := c.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := project.Load(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

	existingResource, has := projectConfig.Resources[req.Name]
	if !has {
		return nil, status.Errorf(codes.NotFound, "resource %s not found", req.Name)
	}

	resourceConfigBytes, err := json.Marshal(existingResource.Props)
	if err != nil {
		return nil, fmt.Errorf("marshaling resource config: %w", err)
	}

	composedResource := &azdext.ComposedResource{
		Name:   existingResource.Name,
		Type:   string(existingResource.Type),
		Config: resourceConfigBytes,
		Uses:   existingResource.Uses,
	}

	return &azdext.GetResourceResponse{
		Resource: composedResource,
	}, nil
}

// GetResourceType implements azdext.ComposeServiceServer.
func (c *ComposeService) GetResourceType(
	context.Context,
	*azdext.GetResourceTypeRequest,
) (*azdext.GetResourceTypeResponse, error) {
	panic("unimplemented")
}

// ListResourceTypes implements azdext.ComposeServiceServer.
func (c *ComposeService) ListResourceTypes(
	context.Context,
	*azdext.EmptyRequest,
) (*azdext.ListResourceTypesResponse, error) {
	panic("unimplemented")
}

// ListResources implements azdext.ComposeServiceServer.
func (c *ComposeService) ListResources(context.Context, *azdext.EmptyRequest) (*azdext.ListResourcesResponse, error) {
	panic("unimplemented")
}
