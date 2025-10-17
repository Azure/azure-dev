// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

func init() {
	registerProjectMappings()
}

// registerProjectMappings registers all project type conversions with the mapper.
// This allows other packages to convert project types to proto types via the mapper.
func registerProjectMappings() {
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

	// ServicePackageResult -> proto ServicePackageResult conversion
	mapper.MustRegister(func(ctx context.Context, src *ServicePackageResult) (*azdext.ServicePackageResult, error) {
		if src == nil {
			return nil, nil
		}

		protoResult := &azdext.ServicePackageResult{PackagePath: src.PackagePath}

		if dockerDetails, ok := src.Details.(*DockerPackageResult); ok {
			protoResult.DockerPackageResult = &azdext.DockerPackageResult{
				ImageHash:   dockerDetails.ImageHash,
				SourceImage: dockerDetails.SourceImage,
				TargetImage: dockerDetails.TargetImage,
			}
			return protoResult, nil
		}

		details := detailsInterfaceToStringMap(src.Details)
		if len(details) > 0 {
			protoResult.Details = details
		}

		return protoResult, nil
	})

	// ServiceRestoreResult -> proto ServiceRestoreResult conversion
	mapper.MustRegister(func(ctx context.Context, src *ServiceRestoreResult) (*azdext.ServiceRestoreResult, error) {
		if src == nil {
			return nil, nil
		}

		protoResult := &azdext.ServiceRestoreResult{}

		details := detailsInterfaceToStringMap(src.Details)
		if len(details) > 0 {
			protoResult.Details = details
		}

		return protoResult, nil
	})

	// ServiceBuildResult -> proto ServiceBuildResult conversion
	mapper.MustRegister(func(ctx context.Context, src *ServiceBuildResult) (*azdext.ServiceBuildResult, error) {
		if src == nil {
			return nil, nil
		}

		protoResult := &azdext.ServiceBuildResult{}

		// Convert nested Restore result if present
		if src.Restore != nil {
			var restoreResult *azdext.ServiceRestoreResult
			err := mapper.Convert(src.Restore, &restoreResult)
			if err != nil {
				return nil, fmt.Errorf("converting restore result: %w", err)
			}
			protoResult.Restore = restoreResult
		}

		details := detailsInterfaceToStringMap(src.Details)
		if len(details) > 0 {
			protoResult.Details = details
		}

		return protoResult, nil
	})

	// ServicePublishResult -> proto ServicePublishResult conversion
	mapper.MustRegister(func(ctx context.Context, src *ServicePublishResult) (*azdext.ServicePublishResult, error) {
		if src == nil {
			return nil, nil
		}

		protoResult := &azdext.ServicePublishResult{}

		if containerDetails, ok := src.Details.(*ContainerPublishDetails); ok {
			if containerDetails.RemoteImage != "" {
				protoResult.ContainerDetails = &azdext.ContainerPublishDetails{
					RemoteImage: containerDetails.RemoteImage,
				}
			}
			return protoResult, nil
		}

		details := detailsInterfaceToStringMap(src.Details)
		if len(details) > 0 {
			protoResult.Details = details
		}

		if protoResult.ContainerDetails == nil && len(protoResult.Details) == 0 {
			return nil, nil
		}

		return protoResult, nil
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
			return &DockerProjectOptions{}, nil
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

	// proto ServicePackageResult -> ServicePackageResult conversion
	mapper.MustRegister(func(ctx context.Context, src *azdext.ServicePackageResult) (*ServicePackageResult, error) {
		if src == nil {
			return &ServicePackageResult{}, nil
		}

		result := &ServicePackageResult{
			PackagePath: src.PackagePath,
		}

		if src.DockerPackageResult != nil {
			result.Details = &DockerPackageResult{
				ImageHash:   src.DockerPackageResult.ImageHash,
				SourceImage: src.DockerPackageResult.SourceImage,
				TargetImage: src.DockerPackageResult.TargetImage,
			}
			return result, nil
		}

		if len(src.Details) > 0 {
			// Convert string map to interface for Details field
			result.Details = src.Details
		}

		return result, nil
	})

	// proto ServicePublishResult -> ServicePublishResult conversion
	mapper.MustRegister(func(ctx context.Context, src *azdext.ServicePublishResult) (*ServicePublishResult, error) {
		if src == nil {
			return &ServicePublishResult{}, nil
		}

		result := &ServicePublishResult{}

		if src.ContainerDetails != nil && src.ContainerDetails.RemoteImage != "" {
			result.Details = &ContainerPublishDetails{
				RemoteImage: src.ContainerDetails.RemoteImage,
			}
			return result, nil
		}

		if len(src.Details) > 0 {
			// Convert string map to interface for Details field
			result.Details = src.Details
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

func detailsInterfaceToStringMap(details interface{}) map[string]string {
	if details == nil {
		return nil
	}

	// Fast path for already-converted maps
	if m, ok := details.(map[string]string); ok {
		return m
	}

	// Use JSON as the serialization format for all types
	data, err := json.Marshal(details)
	if err != nil {
		// Fallback: convert to string representation
		value := fmt.Sprint(details)
		if value == "" || value == "<nil>" {
			return nil
		}
		return map[string]string{"value": value}
	}

	var result map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		// Fallback
		return map[string]string{"json": string(data)}
	}

	if len(result) == 0 {
		return nil
	}

	return result
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
