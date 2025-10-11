// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestServiceConfigMapping(t *testing.T) {
	// ServiceConfig should be automatically registered via init()
	serviceConfig := &ServiceConfig{
		Name:         "test-service",
		Host:         ContainerAppTarget,
		Language:     ServiceLanguageDotNet,
		RelativePath: "./src/api",
	}

	var protoConfig *azdext.ServiceConfig
	err := mapper.Convert(serviceConfig, &protoConfig)
	require.NoError(t, err)
	require.NotNil(t, protoConfig)
	require.Equal(t, "test-service", protoConfig.Name)
	require.Equal(t, string(ContainerAppTarget), protoConfig.Host)
	require.Equal(t, string(ServiceLanguageDotNet), protoConfig.Language)
	require.Equal(t, "./src/api", protoConfig.RelativePath)
}

func TestServiceConfigMappingWithResolver(t *testing.T) {
	// Test with environment resolver
	testResolver := func(key string) string {
		switch key {
		case "SERVICE_NAME":
			return "resolved-service"
		case "REGISTRY":
			return "myregistry.azurecr.io"
		default:
			return ""
		}
	}

	serviceConfig := &ServiceConfig{
		Name:         "test-service",
		Host:         ContainerAppTarget,
		Language:     ServiceLanguageDotNet,
		RelativePath: "./src/api",
	}

	var protoConfig *azdext.ServiceConfig
	err := mapper.WithResolver(testResolver).Convert(serviceConfig, &protoConfig)
	require.NoError(t, err)
	require.NotNil(t, protoConfig)
	require.Equal(t, "test-service", protoConfig.Name)
	require.Equal(t, string(ContainerAppTarget), protoConfig.Host)
}

func TestDockerProjectOptionsMapping(t *testing.T) {
	dockerOptions := DockerProjectOptions{
		Path:        "./Dockerfile",
		Context:     ".",
		Platform:    "linux/amd64",
		Target:      "production",
		RemoteBuild: true,
	}

	var protoOptions *azdext.DockerProjectOptions
	err := mapper.Convert(dockerOptions, &protoOptions)
	require.NoError(t, err)
	require.NotNil(t, protoOptions)
	require.Equal(t, "./Dockerfile", protoOptions.Path)
	require.Equal(t, ".", protoOptions.Context)
	require.Equal(t, "linux/amd64", protoOptions.Platform)
	require.Equal(t, "production", protoOptions.Target)
	require.True(t, protoOptions.RemoteBuild)
}

func TestServicePackageResultMapping(t *testing.T) {
	packageResult := &ServicePackageResult{
		Artifacts: []Artifact{
			{
				Kind:         ArtifactKindArchive,
				Location:     "./dist",
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"imageHash":   "sha256:abc123",
					"sourceImage": "local:latest",
					"targetImage": "registry.io/app:v1.0.0",
				},
			},
		},
	}

	var protoResult *azdext.ServicePackageResult
	err := mapper.Convert(*packageResult, &protoResult)
	require.NoError(t, err)
	require.NotNil(t, protoResult)
	require.Len(t, protoResult.Artifacts, 1)
	require.Equal(t, "./dist", protoResult.Artifacts[0].Location)
}

func TestFromProtoServicePublishResultMapping(t *testing.T) {
	// ServicePublishResult should be automatically registered via init()
	protoResult := &azdext.ServicePublishResult{
		Artifacts: []*azdext.Artifact{
			{
				Kind:         string(ArtifactKindEndpoint),
				Location:     "example.azurecr.io/app:latest",
				LocationKind: string(LocationKindRemote),
				Metadata: map[string]string{
					"imageHash": "sha256:abc123",
				},
			},
		},
	}

	var result *ServicePublishResult
	err := mapper.Convert(protoResult, &result)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify the conversion worked correctly
	require.Len(t, result.Artifacts, 1)
	require.Equal(t, "example.azurecr.io/app:latest", result.Artifacts[0].Location)
}

func TestFromProtoServicePackageResultMapping(t *testing.T) {
	// Create test input
	protoResult := &azdext.ServicePackageResult{
		Artifacts: []*azdext.Artifact{
			{
				Kind:         string(ArtifactKindArchive),
				Location:     "/app/output.tar",
				LocationKind: string(LocationKindLocal),
				Metadata: map[string]string{
					"imageHash":   "sha256:abc123",
					"sourceImage": "app:local",
					"targetImage": "example.azurecr.io/app:latest",
				},
			},
		},
	}

	var result *ServicePackageResult
	err := mapper.Convert(protoResult, &result)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify the conversion worked correctly
	require.Len(t, result.Artifacts, 1)
	require.Equal(t, "/app/output.tar", result.Artifacts[0].Location)
}

func TestFromProtoServicePackageResultMappingNilProto(t *testing.T) {
	// Test with nil proto result - should return empty result
	var result *ServicePackageResult
	err := mapper.Convert((*azdext.ServicePackageResult)(nil), &result)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Artifacts, 0)
}

func TestResourceConfigMapping(t *testing.T) {
	// Create test resource config
	resourceConfig := &ResourceConfig{
		Name: "test-storage",
		Type: ResourceTypeStorage,
		Props: map[string]interface{}{
			"sku":  "Standard_LRS",
			"kind": "StorageV2",
		},
		Uses:       []string{"db-cosmos"},
		ResourceId: "test-resource-id",
	}

	var protoResource *azdext.ComposedResource
	err := mapper.Convert(resourceConfig, &protoResource)
	require.NoError(t, err)
	require.NotNil(t, protoResource)
	require.Equal(t, "test-storage", protoResource.Name)
	require.Equal(t, "storage", protoResource.Type)
	require.Equal(t, []string{"db-cosmos"}, protoResource.Uses)
	require.Equal(t, "test-resource-id", protoResource.ResourceId)

	// Verify the config JSON marshaling
	var configData map[string]interface{}
	err = json.Unmarshal(protoResource.Config, &configData)
	require.NoError(t, err)
	require.Equal(t, "Standard_LRS", configData["sku"])
	require.Equal(t, "StorageV2", configData["kind"])
}

func TestResourceTypeMapping(t *testing.T) {
	var protoResourceType *azdext.ComposedResourceType
	err := mapper.Convert(ResourceTypeDbCosmos, &protoResourceType)
	require.NoError(t, err)
	require.NotNil(t, protoResourceType)
	require.Equal(t, "db.cosmos", protoResourceType.Name)
	require.Equal(t, "CosmosDB", protoResourceType.DisplayName)
	require.Equal(t, "Microsoft.DocumentDB/databaseAccounts/sqlDatabases", protoResourceType.Type)
	require.Equal(t, []string{"GlobalDocumentDB"}, protoResourceType.Kinds)
}

func TestFromProtoServiceConfigMapping(t *testing.T) {
	// Create test proto service config
	protoConfig := &azdext.ServiceConfig{
		Name:              "test-service",
		ResourceGroupName: "test-rg",
		ResourceName:      "test-app",
		ApiVersion:        "2022-03-01",
		RelativePath:      "./src/api",
		Host:              "containerapp",
		Language:          "csharp",
		OutputPath:        "./dist",
		Image:             "nginx:latest",
		Docker: &azdext.DockerProjectOptions{
			Path:        "./Dockerfile",
			Context:     ".",
			Platform:    "linux/amd64",
			Target:      "production",
			Registry:    "myregistry.azurecr.io",
			Image:       "myapp",
			Tag:         "v1.0.0",
			RemoteBuild: true,
			BuildArgs:   []string{"ARG1=value1", "ARG2=value2"},
		},
	}

	var serviceConfig *ServiceConfig
	err := mapper.Convert(protoConfig, &serviceConfig)
	require.NoError(t, err)
	require.NotNil(t, serviceConfig)
	require.Equal(t, "test-service", serviceConfig.Name)
	require.Equal(t, "test-rg", serviceConfig.ResourceGroupName.MustEnvsubst(func(string) string { return "" }))
	require.Equal(t, "test-app", serviceConfig.ResourceName.MustEnvsubst(func(string) string { return "" }))
	require.Equal(t, "2022-03-01", serviceConfig.ApiVersion)
	require.Equal(t, "./src/api", serviceConfig.RelativePath)
	require.Equal(t, ContainerAppTarget, serviceConfig.Host)
	require.Equal(t, ServiceLanguageCsharp, serviceConfig.Language)
	require.Equal(t, "./dist", serviceConfig.OutputPath)
	require.Equal(t, "nginx:latest", serviceConfig.Image.MustEnvsubst(func(string) string { return "" }))

	// Verify docker options conversion
	require.Equal(t, "./Dockerfile", serviceConfig.Docker.Path)
	require.Equal(t, ".", serviceConfig.Docker.Context)
	require.Equal(t, "linux/amd64", serviceConfig.Docker.Platform)
	require.Equal(t, "production", serviceConfig.Docker.Target)
	require.Equal(t, "myregistry.azurecr.io", serviceConfig.Docker.Registry.MustEnvsubst(func(string) string { return "" }))
	require.Equal(t, "myapp", serviceConfig.Docker.Image.MustEnvsubst(func(string) string { return "" }))
	require.Equal(t, "v1.0.0", serviceConfig.Docker.Tag.MustEnvsubst(func(string) string { return "" }))
	require.True(t, serviceConfig.Docker.RemoteBuild)
	require.Len(t, serviceConfig.Docker.BuildArgs, 2)
	require.Equal(t, "ARG1=value1", serviceConfig.Docker.BuildArgs[0].MustEnvsubst(func(string) string { return "" }))
	require.Equal(t, "ARG2=value2", serviceConfig.Docker.BuildArgs[1].MustEnvsubst(func(string) string { return "" }))
}

func TestFromProtoDockerProjectOptionsMapping(t *testing.T) {
	// Create test proto docker options
	protoOptions := &azdext.DockerProjectOptions{
		Path:        "./Dockerfile.test",
		Context:     "..",
		Platform:    "linux/arm64",
		Target:      "test",
		Registry:    "testregistry.azurecr.io",
		Image:       "testimage",
		Tag:         "v2.0.0",
		RemoteBuild: false,
		BuildArgs:   []string{"TEST_ARG=test_value"},
	}

	var dockerOptions *DockerProjectOptions
	err := mapper.Convert(protoOptions, &dockerOptions)
	require.NoError(t, err)
	require.NotNil(t, dockerOptions)
	require.Equal(t, "./Dockerfile.test", dockerOptions.Path)
	require.Equal(t, "..", dockerOptions.Context)
	require.Equal(t, "linux/arm64", dockerOptions.Platform)
	require.Equal(t, "test", dockerOptions.Target)
	require.Equal(t, "testregistry.azurecr.io", dockerOptions.Registry.MustEnvsubst(func(string) string { return "" }))
	require.Equal(t, "testimage", dockerOptions.Image.MustEnvsubst(func(string) string { return "" }))
	require.Equal(t, "v2.0.0", dockerOptions.Tag.MustEnvsubst(func(string) string { return "" }))
	require.False(t, dockerOptions.RemoteBuild)
	require.Len(t, dockerOptions.BuildArgs, 1)
	require.Equal(t, "TEST_ARG=test_value", dockerOptions.BuildArgs[0].MustEnvsubst(func(string) string { return "" }))
}

func TestFromProtoResourceConfigMapping(t *testing.T) {
	// Create test proto composed resource with storage config
	configData := map[string]interface{}{
		"containers": []string{"images", "documents"},
	}
	configBytes, err := json.Marshal(configData)
	require.NoError(t, err)

	protoResource := &azdext.ComposedResource{
		Name:       "test-storage",
		Type:       "storage",
		Config:     configBytes,
		Uses:       []string{"db-cosmos"},
		ResourceId: "test-resource-id",
	}

	var resourceConfig *ResourceConfig
	err = mapper.Convert(protoResource, &resourceConfig)
	require.NoError(t, err)
	require.NotNil(t, resourceConfig)
	require.Equal(t, "test-storage", resourceConfig.Name)
	require.Equal(t, ResourceType("storage"), resourceConfig.Type)
	require.Equal(t, []string{"db-cosmos"}, resourceConfig.Uses)
	require.Equal(t, "test-resource-id", resourceConfig.ResourceId)

	// Verify the props are properly typed as StorageProps
	storageProps, ok := resourceConfig.Props.(StorageProps)
	require.True(t, ok, "Expected StorageProps but got %T", resourceConfig.Props)
	require.Equal(t, []string{"images", "documents"}, storageProps.Containers)

	// Test with empty config
	protoResourceEmpty := &azdext.ComposedResource{
		Name:   "test-storage-empty",
		Type:   "storage",
		Config: nil,
	}

	var resourceConfigEmpty *ResourceConfig
	err = mapper.Convert(protoResourceEmpty, &resourceConfigEmpty)
	require.NoError(t, err)
	require.NotNil(t, resourceConfigEmpty)

	// Verify empty config gives us empty StorageProps
	emptyStorageProps, ok := resourceConfigEmpty.Props.(StorageProps)
	require.True(t, ok, "Expected StorageProps but got %T", resourceConfigEmpty.Props)
	require.Empty(t, emptyStorageProps.Containers)
}
