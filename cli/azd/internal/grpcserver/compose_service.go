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

// composeService exposes features of the AZD composability model to the Extensions Framework layer.
type composeService struct {
	azdext.UnimplementedComposeServiceServer

	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext]
}

func NewComposeService(
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
) azdext.ComposeServiceServer {
	return &composeService{
		lazyAzdContext: lazyAzdContext,
	}
}

// AddResource adds or updates a resource with the given name in the project configuration.
func (c *composeService) AddResource(
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

// GetResource retrieves a resource by its name from the project configuration.
// If the resource does not exist, it returns a NotFound error.
func (c *composeService) GetResource(
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

// GetResourceType gets the resource type configuration schema by the specified name.
func (c *composeService) GetResourceType(
	context.Context,
	*azdext.GetResourceTypeRequest,
) (*azdext.GetResourceTypeResponse, error) {
	panic("unimplemented")
}

// ListResourceTypes lists all available resource types.
func (c *composeService) ListResourceTypes(
	context.Context,
	*azdext.EmptyRequest,
) (*azdext.ListResourceTypesResponse, error) {
	panic("unimplemented")
}

// ListResources lists all resources in the project configuration.
func (c *composeService) ListResources(
	ctx context.Context,
	req *azdext.EmptyRequest,
) (*azdext.ListResourcesResponse, error) {
	azdContext, err := c.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := project.Load(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

	existingResources := projectConfig.Resources
	composedResources := make([]*azdext.ComposedResource, 0, len(existingResources))

	for _, resource := range existingResources {
		resourceConfigBytes, err := json.Marshal(resource.Props)
		if err != nil {
			return nil, fmt.Errorf("marshaling resource config: %w", err)
		}
		composedResource := &azdext.ComposedResource{
			Name:   resource.Name,
			Type:   string(resource.Type),
			Config: resourceConfigBytes,
			Uses:   resource.Uses,
		}
		composedResources = append(composedResources, composedResource)
	}

	return &azdext.ListResourcesResponse{
		Resources: composedResources,
	}, nil
}

// createResourceProps unmarshals the resource configuration bytes into the appropriate struct based on the resource type.
// For the short term this marshalling of resource properties needs to stay in sync with `pkg\project\resources.go`
// In the future we will converge this into a common component.
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
