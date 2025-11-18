// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// composeService exposes features of the AZD composability model to the Extensions Framework layer.
type composeService struct {
	azdext.UnimplementedComposeServiceServer
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext]
	lazyEnv        *lazy.Lazy[*environment.Environment]
	lazyEnvManger  *lazy.Lazy[environment.Manager]
}

func NewComposeService(
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
	lazyEnv *lazy.Lazy[*environment.Environment],
	lazyEnvManger *lazy.Lazy[environment.Manager],
) azdext.ComposeServiceServer {
	return &composeService{
		lazyAzdContext: lazyAzdContext,
		lazyEnv:        lazyEnv,
		lazyEnvManger:  lazyEnvManger,
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

	env, err := c.lazyEnv.GetValue()
	if err != nil {
		return nil, err
	}

	envManager, err := c.lazyEnvManger.GetValue()
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

	// Convert proto resource to ResourceConfig using mapper
	// The mapper now handles creating properly typed Props based on resource type
	var resourceConfig *project.ResourceConfig
	if err := mapper.Convert(req.Resource, &resourceConfig); err != nil {
		return nil, err
	}

	resourceId := req.Resource.ResourceId
	projectConfig.Resources[req.Resource.Name] = resourceConfig

	if resourceId != "" {
		// add existing:true to azure.yaml
		if resource, exists := projectConfig.Resources[req.Resource.Name]; exists {
			resource.Existing = true
		}
		// save resource id to env
		env.DotenvSet(infra.ResourceIdName(req.Resource.Name), resourceId)

		err = envManager.Save(ctx, env)
		if err != nil {
			return nil, fmt.Errorf("saving environment: %w", err)
		}
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

	var composedResource *azdext.ComposedResource
	if err := mapper.Convert(existingResource, &composedResource); err != nil {
		return nil, err
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
	resourceType := project.AllResourceTypes()
	var composedResourceTypes []*azdext.ComposedResourceType
	for _, resource := range resourceType {
		var composedResourceType *azdext.ComposedResourceType
		if err := mapper.Convert(resource, &composedResourceType); err != nil {
			return nil, err
		}
		composedResourceTypes = append(composedResourceTypes, composedResourceType)
	}
	return &azdext.ListResourceTypesResponse{
		ResourceTypes: composedResourceTypes,
	}, nil
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
		var composedResource *azdext.ComposedResource
		if err := mapper.Convert(resource, &composedResource); err != nil {
			return nil, err
		}
		composedResources = append(composedResources, composedResource)
	}

	return &azdext.ListResourcesResponse{
		Resources: composedResources,
	}, nil
}
