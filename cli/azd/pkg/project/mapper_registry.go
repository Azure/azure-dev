// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

func init() {
	registerProjectMappings()
}

// registerProjectMappings registers all project type conversions with the mapper.
// This allows other packages to convert project types to proto types via the mapper.
func registerProjectMappings() {
	// Artifact -> proto Artifact conversion
	mapper.MustRegister(func(ctx context.Context, src *Artifact) (*azdext.Artifact, error) {
		protoKind, err := artifactKindToProto(src.Kind)
		if err != nil {
			return nil, fmt.Errorf("converting artifact kind: %w", err)
		}

		protoLocationKind, err := locationKindToProto(src.LocationKind)
		if err != nil {
			return nil, fmt.Errorf("converting location kind: %w", err)
		}

		return &azdext.Artifact{
			Kind:         protoKind,
			Location:     src.Location,
			LocationKind: protoLocationKind,
			Metadata:     src.Metadata,
		}, nil
	})

	// proto Artifact -> Artifact conversion
	mapper.MustRegister(func(ctx context.Context, src *azdext.Artifact) (*Artifact, error) {
		if src == nil {
			return nil, nil
		}

		goKind, err := protoToArtifactKind(src.Kind)
		if err != nil {
			return nil, fmt.Errorf("converting proto artifact kind: %w", err)
		}

		goLocationKind, err := protoToLocationKind(src.LocationKind)
		if err != nil {
			return nil, fmt.Errorf("converting proto location kind: %w", err)
		}

		artifact := Artifact{
			Kind:         goKind,
			Location:     src.Location,
			LocationKind: goLocationKind,
			Metadata:     src.Metadata,
		}
		if artifact.Metadata == nil {
			artifact.Metadata = make(map[string]string)
		}

		return &artifact, nil
	})

	// ArtifactCollection -> []proto Artifact conversion
	mapper.MustRegister(func(ctx context.Context, src ArtifactCollection) ([]*azdext.Artifact, error) {
		artifacts := make([]*azdext.Artifact, len(src))
		for i, artifact := range src {
			var proto *azdext.Artifact
			if err := mapper.Convert(artifact, &proto); err != nil {
				return nil, err
			}
			artifacts[i] = proto
		}
		return artifacts, nil
	})

	// []proto Artifact -> ArtifactCollection conversion
	mapper.MustRegister(func(ctx context.Context, src []*azdext.Artifact) (ArtifactCollection, error) {
		artifacts := make(ArtifactCollection, len(src))
		for i, protoArtifact := range src {
			var artifact *Artifact
			if err := mapper.Convert(protoArtifact, &artifact); err != nil {
				return nil, err
			}
			artifacts[i] = artifact
		}
		return artifacts, nil
	})

	// ServiceConfig -> proto ServiceConfig conversion
	mapper.MustRegister(func(ctx context.Context, src *ServiceConfig) (*azdext.ServiceConfig, error) {
		resolver := mapper.GetResolver(ctx)
		envResolver := getEnvResolver(resolver)

		resourceGroupName, err := src.ResourceGroupName.Envsubst(envResolver)
		if err != nil {
			return nil, fmt.Errorf("envsubst service resource group name: %w", err)
		}

		resourceName, err := src.ResourceName.Envsubst(envResolver)
		if err != nil {
			return nil, fmt.Errorf("envsubst service resource name: %w", err)
		}

		image, err := src.Image.Envsubst(envResolver)
		if err != nil {
			return nil, fmt.Errorf("envsubst image: %w", err)
		}

		// Convert Docker options
		var docker *azdext.DockerProjectOptions
		err = mapper.WithResolver(resolver).Convert(src.Docker, &docker)
		if err != nil {
			return nil, fmt.Errorf("convert docker options: %w", err)
		}

		return &azdext.ServiceConfig{
			Name:              src.Name,
			ResourceGroupName: resourceGroupName,
			ResourceName:      resourceName,
			ApiVersion:        src.ApiVersion,
			RelativePath:      src.RelativePath,
			Host:              string(src.Host),
			Language:          string(src.Language),
			OutputPath:        src.OutputPath,
			Image:             image,
			Docker:            docker,
		}, nil
	})

	// DockerProjectOptions -> proto DockerProjectOptions conversion
	mapper.MustRegister(func(ctx context.Context, src DockerProjectOptions) (*azdext.DockerProjectOptions, error) {
		resolver := mapper.GetResolver(ctx)
		envResolver := getEnvResolver(resolver)

		registry, err := src.Registry.Envsubst(envResolver)
		if err != nil {
			return nil, fmt.Errorf("envsubst docker registry: %w", err)
		}

		image, err := src.Image.Envsubst(envResolver)
		if err != nil {
			return nil, fmt.Errorf("envsubst docker image: %w", err)
		}

		tag, err := src.Tag.Envsubst(envResolver)
		if err != nil {
			return nil, fmt.Errorf("envsubst docker tag: %w", err)
		}

		buildArgs := []string{}
		for _, arg := range src.BuildArgs {
			resolvedArg, err := arg.Envsubst(envResolver)
			if err != nil {
				return nil, fmt.Errorf("envsubst docker build arg '%s': %w", arg, err)
			}
			buildArgs = append(buildArgs, resolvedArg)
		}

		return &azdext.DockerProjectOptions{
			Path:        src.Path,
			Context:     src.Context,
			Platform:    src.Platform,
			Target:      src.Target,
			Registry:    registry,
			Image:       image,
			Tag:         tag,
			RemoteBuild: src.RemoteBuild,
			BuildArgs:   buildArgs,
		}, nil
	})

	// ServiceBuildResult -> proto ServiceBuildResult conversion
	mapper.MustRegister(func(ctx context.Context, src *ServiceBuildResult) (*azdext.ServiceBuildResult, error) {
		if src == nil {
			return nil, nil
		}

		var artifacts []*azdext.Artifact
		if err := mapper.Convert(src.Artifacts, &artifacts); err != nil {
			return nil, err
		}
		return &azdext.ServiceBuildResult{
			Artifacts: artifacts,
		}, nil
	})

	// ServicePackageResult -> proto ServicePackageResult conversion
	mapper.MustRegister(func(ctx context.Context, src *ServicePackageResult) (*azdext.ServicePackageResult, error) {
		if src == nil {
			return nil, nil
		}

		var artifacts []*azdext.Artifact
		if err := mapper.Convert(src.Artifacts, &artifacts); err != nil {
			return nil, err
		}
		return &azdext.ServicePackageResult{
			Artifacts: artifacts,
		}, nil
	})

	// ServicePublishResult -> proto ServicePublishResult conversion
	mapper.MustRegister(func(ctx context.Context, src *ServicePublishResult) (*azdext.ServicePublishResult, error) {
		if src == nil {
			return nil, nil
		}

		var artifacts []*azdext.Artifact
		if err := mapper.Convert(src.Artifacts, &artifacts); err != nil {
			return nil, err
		}
		return &azdext.ServicePublishResult{
			Artifacts: artifacts,
		}, nil
	})

	// ServiceDeployResult -> proto ServiceDeployResult conversion
	mapper.MustRegister(func(ctx context.Context, src *ServiceDeployResult) (*azdext.ServiceDeployResult, error) {
		if src == nil {
			return nil, nil
		}

		var artifacts []*azdext.Artifact
		if err := mapper.Convert(src.Artifacts, &artifacts); err != nil {
			return nil, err
		}
		return &azdext.ServiceDeployResult{
			Artifacts: artifacts,
		}, nil
	})

	// PublishOptions -> proto PublishOptions conversion
	mapper.MustRegister(func(ctx context.Context, src *PublishOptions) (*azdext.PublishOptions, error) {
		if src == nil {
			return nil, nil
		}

		return &azdext.PublishOptions{
			Image: src.Image,
		}, nil
	})

	// proto PublishOptions -> PublishOptions conversion
	mapper.MustRegister(func(ctx context.Context, src *azdext.PublishOptions) (*PublishOptions, error) {
		if src == nil {
			return nil, nil
		}

		return &PublishOptions{
			Image: src.Image,
		}, nil
	})

	// ResourceConfig -> proto ComposedResource conversion
	mapper.MustRegister(func(ctx context.Context, src *ResourceConfig) (*azdext.ComposedResource, error) {
		if src == nil {
			return nil, nil
		}

		resourceConfigBytes, err := json.Marshal(src.Props)
		if err != nil {
			return nil, fmt.Errorf("marshaling resource config: %w", err)
		}

		return &azdext.ComposedResource{
			Name:       src.Name,
			Type:       string(src.Type),
			Config:     resourceConfigBytes,
			Uses:       src.Uses,
			ResourceId: src.ResourceId,
		}, nil
	})

	// ResourceType -> proto ComposedResourceType conversion
	mapper.MustRegister(func(ctx context.Context, src ResourceType) (*azdext.ComposedResourceType, error) {
		return &azdext.ComposedResourceType{
			Name:        string(src),
			DisplayName: src.String(),
			Type:        src.AzureResourceType(),
			Kinds:       getResourceTypeKinds(src),
		}, nil
	})

	// Register reverse conversions (FromProto* functions)

	// proto ComposedResource -> ResourceConfig conversion
	mapper.MustRegister(func(ctx context.Context, src *azdext.ComposedResource) (*ResourceConfig, error) {
		if src == nil {
			return nil, nil
		}

		// Create properly typed resource props based on resource type
		props, err := createTypedResourceProps(ResourceType(src.Type), src.Config)
		if err != nil {
			return nil, fmt.Errorf("creating typed resource props: %w", err)
		}

		return &ResourceConfig{
			Name:       src.Name,
			Type:       ResourceType(src.Type),
			Props:      props,
			Uses:       src.Uses,
			ResourceId: src.ResourceId,
		}, nil
	})

	// proto ServiceConfig -> ServiceConfig conversion
	mapper.MustRegister(func(ctx context.Context, src *azdext.ServiceConfig) (*ServiceConfig, error) {
		if src == nil {
			return nil, nil
		}

		result := &ServiceConfig{
			Name:              src.Name,
			ResourceGroupName: osutil.NewExpandableString(src.ResourceGroupName),
			ResourceName:      osutil.NewExpandableString(src.ResourceName),
			ApiVersion:        src.ApiVersion,
			RelativePath:      src.RelativePath,
			OutputPath:        src.OutputPath,
			Image:             osutil.NewExpandableString(src.Image),
		}

		if src.Host != "" {
			result.Host = ServiceTargetKind(src.Host)
		}

		if src.Language != "" {
			result.Language = ServiceLanguageKind(src.Language)
		}

		// Convert Docker options if present
		if src.Docker != nil {
			var dockerOptions DockerProjectOptions
			err := mapper.Convert(src.Docker, &dockerOptions)
			if err != nil {
				return nil, fmt.Errorf("convert docker options: %w", err)
			}
			result.Docker = dockerOptions
		}

		return result, nil
	})

	// proto DockerProjectOptions -> DockerProjectOptions conversion (value)
	mapper.MustRegister(func(ctx context.Context, src *azdext.DockerProjectOptions) (DockerProjectOptions, error) {
		if src == nil {
			return DockerProjectOptions{}, nil
		}

		result := DockerProjectOptions{
			Path:        src.Path,
			Context:     src.Context,
			Platform:    src.Platform,
			Target:      src.Target,
			Registry:    osutil.NewExpandableString(src.Registry),
			Image:       osutil.NewExpandableString(src.Image),
			Tag:         osutil.NewExpandableString(src.Tag),
			RemoteBuild: src.RemoteBuild,
		}

		if len(src.BuildArgs) > 0 {
			result.BuildArgs = make([]osutil.ExpandableString, len(src.BuildArgs))
			for i, arg := range src.BuildArgs {
				result.BuildArgs[i] = osutil.NewExpandableString(arg)
			}
		}

		return result, nil
	})

	// proto DockerProjectOptions -> *DockerProjectOptions conversion (pointer)
	mapper.MustRegister(func(ctx context.Context, src *azdext.DockerProjectOptions) (*DockerProjectOptions, error) {
		if src == nil {
			return nil, nil
		}

		result := &DockerProjectOptions{
			Path:        src.Path,
			Context:     src.Context,
			Platform:    src.Platform,
			Target:      src.Target,
			Registry:    osutil.NewExpandableString(src.Registry),
			Image:       osutil.NewExpandableString(src.Image),
			Tag:         osutil.NewExpandableString(src.Tag),
			RemoteBuild: src.RemoteBuild,
		}

		if len(src.BuildArgs) > 0 {
			result.BuildArgs = make([]osutil.ExpandableString, len(src.BuildArgs))
			for i, arg := range src.BuildArgs {
				result.BuildArgs[i] = osutil.NewExpandableString(arg)
			}
		}

		return result, nil
	})

	// proto ServiceBuildResult -> ServiceBuildResult conversion
	mapper.MustRegister(func(ctx context.Context, src *azdext.ServiceBuildResult) (*ServiceBuildResult, error) {
		if src == nil {
			return nil, nil
		}

		result := &ServiceBuildResult{}

		// Convert artifacts
		if err := mapper.Convert(src.Artifacts, &result.Artifacts); err != nil {
			return nil, fmt.Errorf("failed to convert artifacts: %w", err)
		}

		return result, nil
	})

	// proto ServicePackageResult -> ServicePackageResult conversion
	mapper.MustRegister(func(ctx context.Context, src *azdext.ServicePackageResult) (*ServicePackageResult, error) {
		if src == nil {
			return nil, nil
		}

		result := &ServicePackageResult{}

		// Convert artifacts
		if err := mapper.Convert(src.Artifacts, &result.Artifacts); err != nil {
			return nil, fmt.Errorf("failed to convert artifacts: %w", err)
		}

		return result, nil
	})

	// proto ServicePublishResult -> ServicePublishResult conversion
	mapper.MustRegister(func(ctx context.Context, src *azdext.ServicePublishResult) (*ServicePublishResult, error) {
		if src == nil {
			return nil, nil
		}

		result := &ServicePublishResult{}

		// Convert artifacts
		if err := mapper.Convert(src.Artifacts, &result.Artifacts); err != nil {
			return nil, fmt.Errorf("failed to convert artifacts: %w", err)
		}

		return result, nil
	})

	// proto ServiceDeployResult -> ServiceDeployResult conversion
	mapper.MustRegister(func(ctx context.Context, src *azdext.ServiceDeployResult) (*ServiceDeployResult, error) {
		if src == nil {
			return nil, nil
		}

		result := &ServiceDeployResult{}

		// Convert artifacts
		if err := mapper.Convert(src.Artifacts, &result.Artifacts); err != nil {
			return nil, fmt.Errorf("failed to convert artifacts: %w", err)
		}

		return result, nil
	})

	mapper.MustRegister(func(ctx context.Context, src *environment.TargetResource) (*Artifact, error) {
		if src == nil {
			return nil, nil
		}

		// Build the Azure resource ID manually since arm.ResourceID.String() might not work correctly
		// Azure resource IDs follow the pattern: /subscriptions/{id}/resourceGroups/{rg}/providers/{provider}/{type}/{name}
		resourceIdString := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/%s/%s",
			src.SubscriptionId(),
			src.ResourceGroupName(),
			src.ResourceType(),
			src.ResourceName(),
		)

		metadata := src.Metadata()
		if metadata == nil {
			metadata = map[string]string{}
		}

		metadata["subscriptionId"] = src.SubscriptionId()
		metadata["resourceGroup"] = src.ResourceGroupName()
		metadata["name"] = src.ResourceName()
		metadata["type"] = src.ResourceType()

		artifact := Artifact{
			Kind:         ArtifactKindResource,
			Location:     resourceIdString,
			LocationKind: LocationKindRemote,
			Metadata:     metadata,
		}

		return &artifact, nil
	})

	// ServiceContext bi-directional mappings
	mapper.MustRegister(func(ctx context.Context, src *ServiceContext) (*azdext.ServiceContext, error) {
		if src == nil {
			return nil, nil
		}

		result := &azdext.ServiceContext{}

		// Convert ArtifactCollection fields to []*Artifact using existing mappers

		if len(src.Restore) > 0 {
			if err := mapper.Convert(src.Restore, &result.Restore); err != nil {
				return nil, fmt.Errorf("failed to convert Restore artifacts: %w", err)
			}
		}

		if len(src.Build) > 0 {
			if err := mapper.Convert(src.Build, &result.Build); err != nil {
				return nil, fmt.Errorf("failed to convert Build artifacts: %w", err)
			}
		}

		if len(src.Package) > 0 {
			if err := mapper.Convert(src.Package, &result.Package); err != nil {
				return nil, fmt.Errorf("failed to convert Package artifacts: %w", err)
			}
		}

		if len(src.Publish) > 0 {
			if err := mapper.Convert(src.Publish, &result.Publish); err != nil {
				return nil, fmt.Errorf("failed to convert Publish artifacts: %w", err)
			}
		}

		if len(src.Deploy) > 0 {
			if err := mapper.Convert(src.Deploy, &result.Deploy); err != nil {
				return nil, fmt.Errorf("failed to convert Deploy artifacts: %w", err)
			}
		}

		return result, nil
	})

	mapper.MustRegister(func(ctx context.Context, src *azdext.ServiceContext) (*ServiceContext, error) {
		if src == nil {
			return nil, nil
		}

		result := &ServiceContext{
			Restore: make(ArtifactCollection, 0),
			Build:   make(ArtifactCollection, 0),
			Package: make(ArtifactCollection, 0),
			Publish: make(ArtifactCollection, 0),
			Deploy:  make(ArtifactCollection, 0),
		}

		// Convert []*Artifact fields to ArtifactCollection using existing mappers

		if len(src.Restore) > 0 {
			if err := mapper.Convert(src.Restore, &result.Restore); err != nil {
				return nil, fmt.Errorf("failed to convert Restore artifacts: %w", err)
			}
		}

		if len(src.Build) > 0 {
			if err := mapper.Convert(src.Build, &result.Build); err != nil {
				return nil, fmt.Errorf("failed to convert Build artifacts: %w", err)
			}
		}

		if len(src.Package) > 0 {
			if err := mapper.Convert(src.Package, &result.Package); err != nil {
				return nil, fmt.Errorf("failed to convert Package artifacts: %w", err)
			}
		}

		if len(src.Publish) > 0 {
			if err := mapper.Convert(src.Publish, &result.Publish); err != nil {
				return nil, fmt.Errorf("failed to convert Publish artifacts: %w", err)
			}
		}

		if len(src.Deploy) > 0 {
			if err := mapper.Convert(src.Deploy, &result.Deploy); err != nil {
				return nil, fmt.Errorf("failed to convert Deploy artifacts: %w", err)
			}
		}

		return result, nil
	})

	// ArtifactList bi-directional mappings for completeness
	mapper.MustRegister(func(ctx context.Context, src ArtifactCollection) (*azdext.ArtifactList, error) {
		result := &azdext.ArtifactList{}

		if len(src) > 0 {
			if err := mapper.Convert(src, &result.Artifacts); err != nil {
				return nil, fmt.Errorf("failed to convert ArtifactCollection: %w", err)
			}
		}

		return result, nil
	})

	mapper.MustRegister(func(ctx context.Context, src *azdext.ArtifactList) (ArtifactCollection, error) {
		if src == nil || len(src.Artifacts) == 0 {
			return make(ArtifactCollection, 0), nil
		}

		var result ArtifactCollection
		if err := mapper.Convert(src.Artifacts, &result); err != nil {
			return ArtifactCollection{}, fmt.Errorf("failed to convert azdext artifacts: %w", err)
		}

		return result, nil
	})

	mapper.MustRegister(func(ctx context.Context, src *ProjectConfig) (*azdext.ProjectConfig, error) {
		resolver := mapper.GetResolver(ctx)
		envResolver := getEnvResolver(resolver)

		resourceGroupName, err := src.ResourceGroupName.Envsubst(envResolver)
		if err != nil {
			return nil, fmt.Errorf("failed resolving ResourceGroupName, %w", err)
		}

		services := make(map[string]*azdext.ServiceConfig, len(src.Services))
		for i, svc := range src.Services {
			var serviceConfig *azdext.ServiceConfig
			if err := mapper.Convert(svc, &serviceConfig); err != nil {
				return nil, err
			}

			services[i] = serviceConfig
		}

		projectConfig := &azdext.ProjectConfig{
			Name:              src.Name,
			ResourceGroupName: resourceGroupName,
			Path:              src.Path,
			Metadata: func() *azdext.ProjectMetadata {
				if src.Metadata != nil {
					return &azdext.ProjectMetadata{Template: src.Metadata.Template}
				}
				return nil
			}(),
			Infra: &azdext.InfraOptions{
				Provider: string(src.Infra.Provider),
				Path:     src.Infra.Path,
				Module:   src.Infra.Module,
			},
			Services: services,
		}

		return projectConfig, nil
	})

	// proto ProjectConfig -> ProjectConfig conversion
	mapper.MustRegister(func(ctx context.Context, src *azdext.ProjectConfig) (*ProjectConfig, error) {
		if src == nil {
			return &ProjectConfig{}, nil
		}

		services := make(map[string]*ServiceConfig, len(src.Services))
		for name, protoSvc := range src.Services {
			var serviceConfig *ServiceConfig
			if err := mapper.Convert(protoSvc, &serviceConfig); err != nil {
				return nil, fmt.Errorf("converting service %s: %w", name, err)
			}
			services[name] = serviceConfig
		}

		result := &ProjectConfig{
			Name:              src.Name,
			ResourceGroupName: osutil.NewExpandableString(src.ResourceGroupName),
			Path:              src.Path,
			Services:          services,
		}

		// Convert metadata if present
		if src.Metadata != nil {
			result.Metadata = &ProjectMetadata{
				Template: src.Metadata.Template,
			}
		}

		// Convert infra options if present
		if src.Infra != nil {
			result.Infra = provisioning.Options{
				Provider: provisioning.ProviderKind(src.Infra.Provider),
				Path:     src.Infra.Path,
				Module:   src.Infra.Module,
			}
		}

		return result, nil
	})
}

// getEnvResolver returns a resolver function that either uses the provided resolver or returns empty strings.
// This centralizes the common pattern of handling optional environment variable resolution.
func getEnvResolver(resolver mapper.Resolver) func(string) string {
	if resolver != nil {
		return func(key string) string { return resolver(key) }
	}
	return func(string) string { return "" }
}

// getResourceTypeKinds returns the kinds for a given resource type.
// This corresponds to the addListResourcesKind function in grpcserver/compose_service.go.
func getResourceTypeKinds(resourceType ResourceType) []string {
	switch resourceType {
	case ResourceTypeDbCosmos:
		return []string{"GlobalDocumentDB"}
	case ResourceTypeDbMongo:
		return []string{"MongoDB"}
	case ResourceTypeHostAppService:
		return []string{"app", "app,linux"}
	default:
		return []string{}
	}
}

// createTypedResourceProps unmarshals the resource configuration bytes into the appropriate struct based on resource type.
// This matches the logic from internal/grpcserver/compose_service.go createResourceProps function.
func createTypedResourceProps(resourceType ResourceType, config []byte) (any, error) {
	switch resourceType {
	case ResourceTypeHostAppService:
		props := AppServiceProps{}
		if len(config) == 0 {
			return props, nil
		}
		if err := json.Unmarshal(config, &props); err != nil {
			return nil, err
		}
		return props, nil
	case ResourceTypeHostContainerApp:
		props := ContainerAppProps{}
		if len(config) == 0 {
			return props, nil
		}
		if err := json.Unmarshal(config, &props); err != nil {
			return nil, err
		}
		return props, nil
	case ResourceTypeDbCosmos:
		props := CosmosDBProps{}
		if len(config) == 0 {
			return props, nil
		}
		if err := json.Unmarshal(config, &props); err != nil {
			return nil, err
		}
		return props, nil
	case ResourceTypeStorage:
		props := StorageProps{}
		if len(config) == 0 {
			return props, nil
		}
		if err := json.Unmarshal(config, &props); err != nil {
			return nil, err
		}
		return props, nil
	case ResourceTypeAiProject:
		props := AiFoundryModelProps{}
		if len(config) == 0 {
			return props, nil
		}
		if err := json.Unmarshal(config, &props); err != nil {
			return nil, err
		}
		return props, nil
	case ResourceTypeDbMongo:
		props := CosmosDBProps{}
		if len(config) == 0 {
			return props, nil
		}
		if err := json.Unmarshal(config, &props); err != nil {
			return nil, err
		}
		return props, nil
	case ResourceTypeMessagingEventHubs:
		props := EventHubsProps{}
		if len(config) == 0 {
			return props, nil
		}
		if err := json.Unmarshal(config, &props); err != nil {
			return nil, err
		}
		return props, nil
	case ResourceTypeMessagingServiceBus:
		props := ServiceBusProps{}
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

// artifactKindToProto converts Go ArtifactKind to protobuf ArtifactKind
func artifactKindToProto(kind ArtifactKind) (azdext.ArtifactKind, error) {
	switch kind {
	case ArtifactKindDirectory:
		return azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY, nil
	case ArtifactKindConfig:
		return azdext.ArtifactKind_ARTIFACT_KIND_CONFIG, nil
	case ArtifactKindArchive:
		return azdext.ArtifactKind_ARTIFACT_KIND_ARCHIVE, nil
	case ArtifactKindContainer:
		return azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER, nil
	case ArtifactKindEndpoint:
		return azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT, nil
	case ArtifactKindDeployment:
		return azdext.ArtifactKind_ARTIFACT_KIND_DEPLOYMENT, nil
	case ArtifactKindResource:
		return azdext.ArtifactKind_ARTIFACT_KIND_RESOURCE, nil
	default:
		return azdext.ArtifactKind_ARTIFACT_KIND_UNSPECIFIED, fmt.Errorf("unknown artifact kind: %s", kind)
	}
}

// protoToArtifactKind converts protobuf ArtifactKind to Go ArtifactKind
func protoToArtifactKind(kind azdext.ArtifactKind) (ArtifactKind, error) {
	switch kind {
	case azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY:
		return ArtifactKindDirectory, nil
	case azdext.ArtifactKind_ARTIFACT_KIND_CONFIG:
		return ArtifactKindConfig, nil
	case azdext.ArtifactKind_ARTIFACT_KIND_ARCHIVE:
		return ArtifactKindArchive, nil
	case azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER:
		return ArtifactKindContainer, nil
	case azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT:
		return ArtifactKindEndpoint, nil
	case azdext.ArtifactKind_ARTIFACT_KIND_DEPLOYMENT:
		return ArtifactKindDeployment, nil
	case azdext.ArtifactKind_ARTIFACT_KIND_RESOURCE:
		return ArtifactKindResource, nil
	case azdext.ArtifactKind_ARTIFACT_KIND_UNSPECIFIED:
		return "", fmt.Errorf("unspecified artifact kind")
	default:
		return "", fmt.Errorf("unknown proto artifact kind: %v", kind)
	}
}

// locationKindToProto converts Go LocationKind to protobuf LocationKind
func locationKindToProto(kind LocationKind) (azdext.LocationKind, error) {
	switch kind {
	case LocationKindLocal:
		return azdext.LocationKind_LOCATION_KIND_LOCAL, nil
	case LocationKindRemote:
		return azdext.LocationKind_LOCATION_KIND_REMOTE, nil
	default:
		return azdext.LocationKind_LOCATION_KIND_UNSPECIFIED, fmt.Errorf("unknown location kind: %s", kind)
	}
}

// protoToLocationKind converts protobuf LocationKind to Go LocationKind
func protoToLocationKind(kind azdext.LocationKind) (LocationKind, error) {
	switch kind {
	case azdext.LocationKind_LOCATION_KIND_LOCAL:
		return LocationKindLocal, nil
	case azdext.LocationKind_LOCATION_KIND_REMOTE:
		return LocationKindRemote, nil
	case azdext.LocationKind_LOCATION_KIND_UNSPECIFIED:
		return "", fmt.Errorf("unspecified location kind")
	default:
		return "", fmt.Errorf("unknown proto location kind: %v", kind)
	}
}
