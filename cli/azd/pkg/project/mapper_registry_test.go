// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
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
				Kind:         azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
				Location:     "example.azurecr.io/app:latest",
				LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
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
				Kind:         azdext.ArtifactKind_ARTIFACT_KIND_ARCHIVE,
				Location:     "/app/output.tar",
				LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
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

func TestServiceContextMapping(t *testing.T) {
	// Mappings are already registered via init() in mapper_registry.go

	// Create a sample project ServiceContext
	projectContext := ServiceContext{
		Restore: ArtifactCollection{
			{
				Kind:         ArtifactKindConfig,
				Location:     "/tmp/restored.txt",
				LocationKind: LocationKindLocal,
				Metadata:     map[string]string{"type": "dependencies"},
			},
		},
		Build: ArtifactCollection{
			{
				Kind:         ArtifactKindContainer,
				Location:     "my-app:latest",
				LocationKind: LocationKindLocal,
				Metadata:     map[string]string{"type": "docker"},
			},
		},
		Package: ArtifactCollection{
			{
				Kind:         ArtifactKindContainer,
				Location:     "registry.azurecr.io/my-app:v1.0.0",
				LocationKind: LocationKindRemote,
				Metadata:     map[string]string{"registry": "azurecr.io"},
			},
		},
		Publish: make(ArtifactCollection, 0),
		Deploy: ArtifactCollection{
			{
				Kind: ArtifactKindResource,
				//nolint:lll
				Location:     "/subscriptions/123/resourceGroups/rg/providers/Microsoft.ContainerInstance/containerGroups/my-app",
				LocationKind: LocationKindRemote,
				Metadata:     map[string]string{"resourceGroup": "rg"},
			},
		},
	}

	t.Run("project.ServiceContext -> azdext.ServiceContext", func(t *testing.T) {
		// Convert to azdext.ServiceContext
		var protoContext *azdext.ServiceContext
		err := mapper.Convert(&projectContext, &protoContext)
		require.NoError(t, err)
		require.NotNil(t, protoContext)

		// Verify the conversion
		assert.Len(t, protoContext.Restore, 1)
		assert.Equal(t, "dependencies", protoContext.Restore[0].Metadata["type"])
		assert.Equal(t, "/tmp/restored.txt", protoContext.Restore[0].Location)

		assert.Len(t, protoContext.Build, 1)
		assert.Equal(t, "docker", protoContext.Build[0].Metadata["type"])
		assert.Equal(t, "my-app:latest", protoContext.Build[0].Location)

		assert.Len(t, protoContext.Package, 1)
		assert.Equal(t, "azurecr.io", protoContext.Package[0].Metadata["registry"])
		assert.Equal(t, "registry.azurecr.io/my-app:v1.0.0", protoContext.Package[0].Location)

		assert.Len(t, protoContext.Publish, 0)

		assert.Len(t, protoContext.Deploy, 1)
		assert.Equal(t, "rg", protoContext.Deploy[0].Metadata["resourceGroup"])
		assert.Equal(
			t,
			"/subscriptions/123/resourceGroups/rg/providers/Microsoft.ContainerInstance/containerGroups/my-app",
			protoContext.Deploy[0].Location,
		)
	})

	t.Run("azdext.ServiceContext -> project.ServiceContext", func(t *testing.T) {
		// Create azdext.ServiceContext
		protoContext := &azdext.ServiceContext{
			Restore: []*azdext.Artifact{
				{
					Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONFIG,
					Location:     "/tmp/deps.txt",
					LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
					Metadata:     map[string]string{"restored": "true"},
				},
			},
			Build: []*azdext.Artifact{
				{
					Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
					Location:     "test-image:latest",
					LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
					Metadata:     map[string]string{"built": "true"},
				},
			},
			Package: []*azdext.Artifact{},
			Publish: []*azdext.Artifact{
				{
					Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
					Location:     "registry.azurecr.io/test-image:v2.0.0",
					LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
					Metadata:     map[string]string{"published": "true"},
				},
			},
			Deploy: []*azdext.Artifact{
				{
					Kind:         azdext.ArtifactKind_ARTIFACT_KIND_RESOURCE,
					Location:     "/subscriptions/456/resourceGroups/test-rg/providers/Microsoft.Web/sites/test-app",
					LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
					Metadata:     map[string]string{"deployed": "true"},
				},
			},
		}

		// Convert to project.ServiceContext
		var resultContext *ServiceContext
		err := mapper.Convert(protoContext, &resultContext)
		require.NoError(t, err)
		require.NotNil(t, resultContext)

		// Verify the conversion
		assert.Len(t, resultContext.Restore, 1)
		assert.Equal(t, "true", resultContext.Restore[0].Metadata["restored"])
		assert.Equal(t, "/tmp/deps.txt", resultContext.Restore[0].Location)

		assert.Len(t, resultContext.Build, 1)
		assert.Equal(t, "true", resultContext.Build[0].Metadata["built"])
		assert.Equal(t, "test-image:latest", resultContext.Build[0].Location)

		assert.Len(t, resultContext.Package, 0)

		assert.Len(t, resultContext.Publish, 1)
		assert.Equal(t, "true", resultContext.Publish[0].Metadata["published"])
		assert.Equal(t, "registry.azurecr.io/test-image:v2.0.0", resultContext.Publish[0].Location)

		assert.Len(t, resultContext.Deploy, 1)
		assert.Equal(t, "true", resultContext.Deploy[0].Metadata["deployed"])
		assert.Equal(
			t,
			"/subscriptions/456/resourceGroups/test-rg/providers/Microsoft.Web/sites/test-app",
			resultContext.Deploy[0].Location,
		)
	})

	t.Run("round-trip mapping", func(t *testing.T) {
		// Start with project context, convert to proto, then back to project

		// Project -> Proto
		var protoContext *azdext.ServiceContext
		err := mapper.Convert(&projectContext, &protoContext)
		require.NoError(t, err)

		// Proto -> Project
		var roundTripContext *ServiceContext
		err = mapper.Convert(protoContext, &roundTripContext)
		require.NoError(t, err)
		require.NotNil(t, roundTripContext)

		// Verify round-trip integrity
		assert.Len(t, roundTripContext.Restore, len(projectContext.Restore))
		assert.Len(t, roundTripContext.Build, len(projectContext.Build))
		assert.Len(t, roundTripContext.Package, len(projectContext.Package))
		assert.Len(t, roundTripContext.Publish, len(projectContext.Publish))
		assert.Len(t, roundTripContext.Deploy, len(projectContext.Deploy))

		// Check specific artifact integrity
		if len(roundTripContext.Build) > 0 && len(projectContext.Build) > 0 {
			assert.Equal(t, projectContext.Build[0].Kind, roundTripContext.Build[0].Kind)
			assert.Equal(t, projectContext.Build[0].Location, roundTripContext.Build[0].Location)
			assert.Equal(t, projectContext.Build[0].LocationKind, roundTripContext.Build[0].LocationKind)
			assert.Equal(t, projectContext.Build[0].Metadata["type"], roundTripContext.Build[0].Metadata["type"])
		}
	})
}

func TestArtifactListMapping(t *testing.T) {
	// Mappings are already registered via init() in mapper_registry.go

	t.Run("ArtifactCollection -> ArtifactList", func(t *testing.T) {
		collection := ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     "/tmp/test.txt",
				LocationKind: LocationKindLocal,
				Metadata:     map[string]string{"test": "value"},
			},
		}

		var artifactList *azdext.ArtifactList
		err := mapper.Convert(collection, &artifactList)
		require.NoError(t, err)
		require.NotNil(t, artifactList)

		assert.Len(t, artifactList.Artifacts, 1)
		assert.Equal(t, "value", artifactList.Artifacts[0].Metadata["test"])
		assert.Equal(t, "/tmp/test.txt", artifactList.Artifacts[0].Location)
	})

	t.Run("ArtifactList -> ArtifactCollection", func(t *testing.T) {
		artifactList := &azdext.ArtifactList{
			Artifacts: []*azdext.Artifact{
				{
					Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
					Location:     "test:latest",
					LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
					Metadata:     map[string]string{"image": "test"},
				},
			},
		}

		var collection ArtifactCollection
		err := mapper.Convert(artifactList, &collection)
		require.NoError(t, err)

		assert.Len(t, collection, 1)
		assert.Equal(t, "test", collection[0].Metadata["image"])
		assert.Equal(t, "test:latest", collection[0].Location)
		assert.Equal(t, ArtifactKindContainer, collection[0].Kind)
	})
}
