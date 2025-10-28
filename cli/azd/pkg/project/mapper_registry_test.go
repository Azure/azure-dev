// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// compareValues recursively compares expected and actual values, accounting for protobuf type conversions
func compareValues(t *testing.T, expected, actual any, context string) {
	switch exp := expected.(type) {
	case int:
		// Protobuf converts all numbers to float64
		require.Equal(t, float64(exp), actual, "Value at %s should match", context)
	case []any:
		actualSlice, ok := actual.([]any)
		require.True(t, ok, "Expected slice at %s but got %T", context, actual)
		require.Len(t, actualSlice, len(exp), "Slice length at %s should match", context)
		for i, expectedItem := range exp {
			compareValues(t, expectedItem, actualSlice[i], fmt.Sprintf("%s[%d]", context, i))
		}
	case map[string]any:
		actualMap, ok := actual.(map[string]any)
		require.True(t, ok, "Expected map at %s but got %T", context, actual)
		require.Len(t, actualMap, len(exp), "Map length at %s should match", context)
		for key, expectedValue := range exp {
			actualValue, exists := actualMap[key]
			require.True(t, exists, "Key %s.%s should exist", context, key)
			compareValues(t, expectedValue, actualValue, fmt.Sprintf("%s.%s", context, key))
		}
	default:
		require.Equal(t, expected, actual, "Value at %s should match", context)
	}
}

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

func TestServiceConfigMappingWithConfig(t *testing.T) {
	// Test ServiceConfig with various Config field scenarios
	tests := []struct {
		name        string
		config      map[string]any
		expectError bool
	}{
		{
			name:   "nil config",
			config: nil,
		},
		{
			name:   "empty config",
			config: make(map[string]any),
		},
		{
			name: "simple config",
			config: map[string]any{
				"key1": "value1",
				"key2": 42,
				"key3": true,
			},
		},
		{
			name: "nested config",
			config: map[string]any{
				"database": map[string]any{
					"host": "localhost",
					"port": 5432,
				},
				"features": []any{"auth", "logging"}, // Use []any instead of []string for protobuf compatibility
				"settings": map[string]any{
					"debug":   true,
					"timeout": 30,
				},
			},
		},
		{
			name: "complex config with various types",
			config: map[string]any{
				"string_val":  "test",
				"int_val":     123,
				"float_val":   3.14,
				"bool_val":    true,
				"array_val":   []any{"a", "b", "c"},
				"null_val":    nil,
				"nested_map":  map[string]any{"inner": "value"},
				"mixed_array": []any{1, "two", true, map[string]any{"key": "value"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceConfig := &ServiceConfig{
				Name:         "test-service",
				Host:         ContainerAppTarget,
				Language:     ServiceLanguageDotNet,
				RelativePath: "./src/api",
				Config:       tt.config,
			}

			var protoConfig *azdext.ServiceConfig
			err := mapper.Convert(serviceConfig, &protoConfig)

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, protoConfig)
			require.Equal(t, "test-service", protoConfig.Name)
			require.Equal(t, string(ContainerAppTarget), protoConfig.Host)
			require.Equal(t, string(ServiceLanguageDotNet), protoConfig.Language)
			require.Equal(t, "./src/api", protoConfig.RelativePath)

			if tt.config == nil {
				require.Nil(t, protoConfig.Config)
			} else if len(tt.config) == 0 {
				require.NotNil(t, protoConfig.Config)
				// Empty map should convert to empty struct
				actualMap := protoConfig.Config.AsMap()
				require.Empty(t, actualMap)
			} else {
				require.NotNil(t, protoConfig.Config)
				// Verify the config was properly converted to structpb.Struct
				actualMap := protoConfig.Config.AsMap()
				// Note: protobuf converts all numbers to float64, so we need to do a more nuanced comparison
				require.Len(t, actualMap, len(tt.config))
				for key, expectedValue := range tt.config {
					actualValue, exists := actualMap[key]
					require.True(t, exists, "Key %s should exist", key)

					// Handle type conversions that happen with protobuf recursively
					compareValues(t, expectedValue, actualValue, key)
				}
			}
		})
	}
}

func TestServiceConfigReverseMapping(t *testing.T) {
	// Test proto ServiceConfig -> ServiceConfig conversion
	tests := []struct {
		name        string
		setupConfig func() *azdext.ServiceConfig
		validateFn  func(t *testing.T, result *ServiceConfig)
	}{
		{
			name: "nil config in proto",
			setupConfig: func() *azdext.ServiceConfig {
				return &azdext.ServiceConfig{
					Name:         "test-service",
					Host:         string(ContainerAppTarget),
					Language:     string(ServiceLanguageDotNet),
					RelativePath: "./src/api",
					Config:       nil,
				}
			},
			validateFn: func(t *testing.T, result *ServiceConfig) {
				require.Equal(t, "test-service", result.Name)
				require.Equal(t, ContainerAppTarget, result.Host)
				require.Equal(t, ServiceLanguageDotNet, result.Language)
				require.Equal(t, "./src/api", result.RelativePath)
				require.Nil(t, result.Config)
			},
		},
		{
			name: "empty config in proto",
			setupConfig: func() *azdext.ServiceConfig {
				config, err := structpb.NewStruct(map[string]any{})
				require.NoError(t, err)
				return &azdext.ServiceConfig{
					Name:         "test-service",
					Host:         string(ContainerAppTarget),
					Language:     string(ServiceLanguageDotNet),
					RelativePath: "./src/api",
					Config:       config,
				}
			},
			validateFn: func(t *testing.T, result *ServiceConfig) {
				require.Equal(t, "test-service", result.Name)
				require.NotNil(t, result.Config)
				require.Empty(t, result.Config)
			},
		},
		{
			name: "complex config in proto",
			setupConfig: func() *azdext.ServiceConfig {
				configData := map[string]any{
					"database": map[string]any{
						"host": "localhost",
						"port": 5432,
					},
					"features": []any{"auth", "logging"},
					"debug":    true,
				}
				config, err := structpb.NewStruct(configData)
				require.NoError(t, err)
				return &azdext.ServiceConfig{
					Name:         "test-service",
					Host:         string(ContainerAppTarget),
					Language:     string(ServiceLanguageDotNet),
					RelativePath: "./src/api",
					Config:       config,
				}
			},
			validateFn: func(t *testing.T, result *ServiceConfig) {
				require.Equal(t, "test-service", result.Name)
				require.NotNil(t, result.Config)
				require.Equal(t, true, result.Config["debug"])

				// Check nested objects
				database, ok := result.Config["database"].(map[string]any)
				require.True(t, ok)
				require.Equal(t, "localhost", database["host"])
				require.Equal(t, float64(5432), database["port"]) // JSON numbers become float64

				// Check arrays
				features, ok := result.Config["features"].([]any)
				require.True(t, ok)
				require.Len(t, features, 2)
				require.Equal(t, "auth", features[0])
				require.Equal(t, "logging", features[1])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protoConfig := tt.setupConfig()

			var serviceConfig *ServiceConfig
			err := mapper.Convert(protoConfig, &serviceConfig)
			require.NoError(t, err)
			require.NotNil(t, serviceConfig)

			tt.validateFn(t, serviceConfig)
		})
	}
}

func TestServiceConfigRoundTripMapping(t *testing.T) {
	// Test that ServiceConfig -> proto -> ServiceConfig preserves Config data
	originalConfig := map[string]any{
		"string_val": "test",
		"int_val":    123,
		"float_val":  3.14,
		"bool_val":   true,
		"array_val":  []any{"a", "b", "c"},
		"nested": map[string]any{
			"inner_key": "inner_value",
			"inner_num": 456,
		},
	}

	originalServiceConfig := &ServiceConfig{
		Name:         "test-service",
		Host:         ContainerAppTarget,
		Language:     ServiceLanguageDotNet,
		RelativePath: "./src/api",
		Config:       originalConfig,
	}

	// Convert to proto
	var protoConfig *azdext.ServiceConfig
	err := mapper.Convert(originalServiceConfig, &protoConfig)
	require.NoError(t, err)
	require.NotNil(t, protoConfig)

	// Convert back to ServiceConfig
	var roundTripServiceConfig *ServiceConfig
	err = mapper.Convert(protoConfig, &roundTripServiceConfig)
	require.NoError(t, err)
	require.NotNil(t, roundTripServiceConfig)

	// Verify basic fields
	require.Equal(t, originalServiceConfig.Name, roundTripServiceConfig.Name)
	require.Equal(t, originalServiceConfig.Host, roundTripServiceConfig.Host)
	require.Equal(t, originalServiceConfig.Language, roundTripServiceConfig.Language)
	require.Equal(t, originalServiceConfig.RelativePath, roundTripServiceConfig.RelativePath)

	// Verify config data (note: some type conversions are expected due to JSON/protobuf handling)
	require.NotNil(t, roundTripServiceConfig.Config)
	require.Equal(t, "test", roundTripServiceConfig.Config["string_val"])
	require.Equal(t, float64(123), roundTripServiceConfig.Config["int_val"]) // Numbers become float64
	require.Equal(t, 3.14, roundTripServiceConfig.Config["float_val"])
	require.Equal(t, true, roundTripServiceConfig.Config["bool_val"])

	// Check array
	arrayVal, ok := roundTripServiceConfig.Config["array_val"].([]any)
	require.True(t, ok)
	require.Equal(t, []any{"a", "b", "c"}, arrayVal)

	// Check nested object
	nested, ok := roundTripServiceConfig.Config["nested"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "inner_value", nested["inner_key"])
	require.Equal(t, float64(456), nested["inner_num"]) // Numbers become float64
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

func TestServiceBuildResultMapping(t *testing.T) {
	buildResult := &ServiceBuildResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     "./build",
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"buildOutput": "success",
					"buildTime":   "2023-01-01T10:00:00Z",
				},
			},
		},
	}

	var protoResult *azdext.ServiceBuildResult
	err := mapper.Convert(buildResult, &protoResult)
	require.NoError(t, err)
	require.NotNil(t, protoResult)
	require.Len(t, protoResult.Artifacts, 1)
	require.Equal(t, "./build", protoResult.Artifacts[0].Location)
	require.Equal(t, azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY, protoResult.Artifacts[0].Kind)
	require.Equal(t, "success", protoResult.Artifacts[0].Metadata["buildOutput"])
}

func TestServiceBuildResultMappingNil(t *testing.T) {
	// Test with nil input - should return nil
	var protoResult *azdext.ServiceBuildResult
	err := mapper.Convert((*ServiceBuildResult)(nil), &protoResult)
	require.NoError(t, err)
	require.Nil(t, protoResult)
}

func TestServicePackageResultMapping(t *testing.T) {
	packageResult := &ServicePackageResult{
		Artifacts: ArtifactCollection{
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
	err := mapper.Convert(packageResult, &protoResult)
	require.NoError(t, err)
	require.NotNil(t, protoResult)
	require.Len(t, protoResult.Artifacts, 1)
	require.Equal(t, "./dist", protoResult.Artifacts[0].Location)
}

func TestFromProtoServiceBuildResultMapping(t *testing.T) {
	// Create test input
	protoResult := &azdext.ServiceBuildResult{
		Artifacts: []*azdext.Artifact{
			{
				Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY,
				Location:     "/app/build",
				LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
				Metadata: map[string]string{
					"buildOutput": "success",
					"buildTime":   "2023-01-01T10:00:00Z",
				},
			},
		},
	}

	var result *ServiceBuildResult
	err := mapper.Convert(protoResult, &result)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify the conversion worked correctly
	require.Len(t, result.Artifacts, 1)
	require.Equal(t, "/app/build", result.Artifacts[0].Location)
	require.Equal(t, ArtifactKindDirectory, result.Artifacts[0].Kind)
	require.Equal(t, "success", result.Artifacts[0].Metadata["buildOutput"])
}

func TestFromProtoServiceBuildResultMappingNilProto(t *testing.T) {
	// Test with nil proto result - should return empty result
	var result *ServiceBuildResult
	err := mapper.Convert((*azdext.ServiceBuildResult)(nil), &result)
	require.NoError(t, err)
	require.Nil(t, result)
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
	require.Nil(t, result)
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

func TestPublishOptionsMapping(t *testing.T) {
	t.Run("PublishOptions -> proto PublishOptions", func(t *testing.T) {
		publishOptions := &PublishOptions{
			Image: "example.azurecr.io/myapp:v1.2.3",
		}

		var protoOptions *azdext.PublishOptions
		err := mapper.Convert(publishOptions, &protoOptions)
		require.NoError(t, err)
		require.NotNil(t, protoOptions)
		require.Equal(t, "example.azurecr.io/myapp:v1.2.3", protoOptions.Image)
	})

	t.Run("proto PublishOptions -> PublishOptions", func(t *testing.T) {
		protoOptions := &azdext.PublishOptions{
			Image: "registry.io/test:latest",
		}

		var publishOptions *PublishOptions
		err := mapper.Convert(protoOptions, &publishOptions)
		require.NoError(t, err)
		require.NotNil(t, publishOptions)
		require.Equal(t, "registry.io/test:latest", publishOptions.Image)
	})

	t.Run("nil proto PublishOptions -> PublishOptions", func(t *testing.T) {
		var publishOptions *PublishOptions
		err := mapper.Convert((*azdext.PublishOptions)(nil), &publishOptions)
		require.NoError(t, err)
		require.Nil(t, publishOptions)
	})

	t.Run("round-trip PublishOptions mapping", func(t *testing.T) {
		original := &PublishOptions{
			Image: "test.azurecr.io/roundtrip:tag",
		}

		// Go -> Proto
		var protoOptions *azdext.PublishOptions
		err := mapper.Convert(original, &protoOptions)
		require.NoError(t, err)

		// Proto -> Go
		var roundTrip *PublishOptions
		err = mapper.Convert(protoOptions, &roundTrip)
		require.NoError(t, err)
		require.NotNil(t, roundTrip)
		require.Equal(t, original.Image, roundTrip.Image)
	})
}

func TestProjectConfigMapping(t *testing.T) {
	t.Run("ProjectConfig -> proto ProjectConfig", func(t *testing.T) {
		projectConfig := &ProjectConfig{
			Name:              "test-project",
			ResourceGroupName: osutil.NewExpandableString("test-rg-${ENVIRONMENT_NAME}"),
			Path:              "/path/to/project",
			Metadata: &ProjectMetadata{
				Template: "todo-python-mongo@1.0.0",
			},
			Services: map[string]*ServiceConfig{
				"web": {
					Name:         "web",
					Host:         ContainerAppTarget,
					Language:     ServiceLanguagePython,
					RelativePath: "./src",
				},
				"api": {
					Name:         "api",
					Host:         AppServiceTarget,
					Language:     ServiceLanguageJavaScript,
					RelativePath: "./api",
				},
			},
		}

		testResolver := func(key string) string {
			if key == "ENVIRONMENT_NAME" {
				return "dev"
			}
			return ""
		}

		var protoConfig *azdext.ProjectConfig
		err := mapper.WithResolver(testResolver).Convert(projectConfig, &protoConfig)
		require.NoError(t, err)
		require.NotNil(t, protoConfig)
		require.Equal(t, "test-project", protoConfig.Name)
		require.Equal(t, "test-rg-dev", protoConfig.ResourceGroupName)
		require.Equal(t, "/path/to/project", protoConfig.Path)
		require.NotNil(t, protoConfig.Metadata)
		require.Equal(t, "todo-python-mongo@1.0.0", protoConfig.Metadata.Template)
		require.Len(t, protoConfig.Services, 2)
		require.Contains(t, protoConfig.Services, "web")
		require.Contains(t, protoConfig.Services, "api")
		require.Equal(t, "containerapp", protoConfig.Services["web"].Host)
		require.Equal(t, "appservice", protoConfig.Services["api"].Host)
	})

	t.Run("proto ProjectConfig -> ProjectConfig", func(t *testing.T) {
		protoConfig := &azdext.ProjectConfig{
			Name:              "reverse-test-project",
			ResourceGroupName: "reverse-test-rg",
			Path:              "/reverse/path",
			Metadata: &azdext.ProjectMetadata{
				Template: "reverse-template@2.0.0",
			},
			Services: map[string]*azdext.ServiceConfig{
				"backend": {
					Name:         "backend",
					Host:         "containerapp",
					Language:     "go",
					RelativePath: "./backend",
				},
			},
		}

		var projectConfig *ProjectConfig
		err := mapper.Convert(protoConfig, &projectConfig)
		require.NoError(t, err)
		require.NotNil(t, projectConfig)
		require.Equal(t, "reverse-test-project", projectConfig.Name)
		require.Equal(t, "reverse-test-rg", projectConfig.ResourceGroupName.MustEnvsubst(func(string) string { return "" }))
		require.Equal(t, "/reverse/path", projectConfig.Path)
		require.NotNil(t, projectConfig.Metadata)
		require.Equal(t, "reverse-template@2.0.0", projectConfig.Metadata.Template)
		require.Len(t, projectConfig.Services, 1)
		require.Contains(t, projectConfig.Services, "backend")
		require.Equal(t, ContainerAppTarget, projectConfig.Services["backend"].Host)
		require.Equal(t, ServiceLanguageKind("go"), projectConfig.Services["backend"].Language)
	})

	t.Run("nil proto ProjectConfig -> ProjectConfig", func(t *testing.T) {
		var projectConfig *ProjectConfig
		err := mapper.Convert((*azdext.ProjectConfig)(nil), &projectConfig)
		require.NoError(t, err)
		require.NotNil(t, projectConfig)
		require.Equal(t, "", projectConfig.Name)
		require.Equal(t, "", projectConfig.ResourceGroupName.MustEnvsubst(func(string) string { return "" }))
	})

	t.Run("round-trip ProjectConfig mapping", func(t *testing.T) {
		original := &ProjectConfig{
			Name:              "roundtrip-project",
			ResourceGroupName: osutil.NewExpandableString("roundtrip-rg"),
			Path:              "/roundtrip/path",
			Metadata: &ProjectMetadata{
				Template: "roundtrip@3.0.0",
			},
			Services: map[string]*ServiceConfig{
				"service1": {
					Name:         "service1",
					Host:         AppServiceTarget,
					Language:     ServiceLanguageTypeScript,
					RelativePath: "./service1",
				},
			},
		}

		// Go -> Proto
		var protoConfig *azdext.ProjectConfig
		err := mapper.Convert(original, &protoConfig)
		require.NoError(t, err)

		// Proto -> Go
		var roundTrip *ProjectConfig
		err = mapper.Convert(protoConfig, &roundTrip)
		require.NoError(t, err)
		require.NotNil(t, roundTrip)
		require.Equal(t, original.Name, roundTrip.Name)
		require.Equal(t, original.Path, roundTrip.Path)
		require.Equal(t, original.Metadata.Template, roundTrip.Metadata.Template)
		require.Len(t, roundTrip.Services, 1)
		require.Equal(t, original.Services["service1"].Name, roundTrip.Services["service1"].Name)
		require.Equal(t, original.Services["service1"].Host, roundTrip.Services["service1"].Host)
		require.Equal(t, original.Services["service1"].Language, roundTrip.Services["service1"].Language)
	})
}

func TestServiceDeployResultMapping(t *testing.T) {
	t.Run("ServiceDeployResult -> proto ServiceDeployResult", func(t *testing.T) {
		deployResult := &ServiceDeployResult{
			Artifacts: ArtifactCollection{
				{
					Kind:         ArtifactKindResource,
					Location:     "/subscriptions/123/resourceGroups/rg/providers/Microsoft.Web/sites/myapp",
					LocationKind: LocationKindRemote,
					Metadata: map[string]string{
						"resourceGroup": "rg",
						"appName":       "myapp",
					},
				},
				{
					Kind:         ArtifactKindEndpoint,
					Location:     "https://myapp.azurewebsites.net",
					LocationKind: LocationKindRemote,
					Metadata: map[string]string{
						"type": "primary",
					},
				},
			},
		}

		var protoResult *azdext.ServiceDeployResult
		err := mapper.Convert(deployResult, &protoResult)
		require.NoError(t, err)
		require.NotNil(t, protoResult)
		require.Len(t, protoResult.Artifacts, 2)
		require.Equal(
			t,
			"/subscriptions/123/resourceGroups/rg/providers/Microsoft.Web/sites/myapp",
			protoResult.Artifacts[0].Location,
		)
		require.Equal(t, "https://myapp.azurewebsites.net", protoResult.Artifacts[1].Location)
		require.Equal(t, "rg", protoResult.Artifacts[0].Metadata["resourceGroup"])
		require.Equal(t, "primary", protoResult.Artifacts[1].Metadata["type"])
	})

	t.Run("proto ServiceDeployResult -> ServiceDeployResult", func(t *testing.T) {
		protoResult := &azdext.ServiceDeployResult{
			Artifacts: []*azdext.Artifact{
				{
					Kind: azdext.ArtifactKind_ARTIFACT_KIND_RESOURCE,
					Location: "/subscriptions/456/resourceGroups/test-rg/providers/" +
						"Microsoft.ContainerInstance/containerGroups/test-app",
					LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
					Metadata: map[string]string{
						"resourceGroup": "test-rg",
						"appName":       "test-app",
					},
				},
			},
		}

		var deployResult *ServiceDeployResult
		err := mapper.Convert(protoResult, &deployResult)
		require.NoError(t, err)
		require.NotNil(t, deployResult)
		require.Len(t, deployResult.Artifacts, 1)
		expectedLocation := "/subscriptions/456/resourceGroups/test-rg/providers/" +
			"Microsoft.ContainerInstance/containerGroups/test-app"
		require.Equal(t, expectedLocation, deployResult.Artifacts[0].Location)
		require.Equal(t, "test-rg", deployResult.Artifacts[0].Metadata["resourceGroup"])
		require.Equal(t, "test-app", deployResult.Artifacts[0].Metadata["appName"])
	})

	t.Run("nil proto ServiceDeployResult -> ServiceDeployResult", func(t *testing.T) {
		var deployResult *ServiceDeployResult
		err := mapper.Convert((*azdext.ServiceDeployResult)(nil), &deployResult)
		require.NoError(t, err)
		require.Nil(t, deployResult)
	})

	t.Run("round-trip ServiceDeployResult mapping", func(t *testing.T) {
		original := &ServiceDeployResult{
			Artifacts: ArtifactCollection{
				{
					Kind:         ArtifactKindDeployment,
					Location:     "deployment-12345",
					LocationKind: LocationKindRemote,
					Metadata: map[string]string{
						"status": "succeeded",
					},
				},
			},
		}

		// Go -> Proto
		var protoResult *azdext.ServiceDeployResult
		err := mapper.Convert(original, &protoResult)
		require.NoError(t, err)

		// Proto -> Go
		var roundTrip *ServiceDeployResult
		err = mapper.Convert(protoResult, &roundTrip)
		require.NoError(t, err)
		require.NotNil(t, roundTrip)
		require.Len(t, roundTrip.Artifacts, 1)
		require.Equal(t, original.Artifacts[0].Kind, roundTrip.Artifacts[0].Kind)
		require.Equal(t, original.Artifacts[0].Location, roundTrip.Artifacts[0].Location)
		require.Equal(t, original.Artifacts[0].LocationKind, roundTrip.Artifacts[0].LocationKind)
		require.Equal(t, original.Artifacts[0].Metadata["status"], roundTrip.Artifacts[0].Metadata["status"])
	})
}

func TestTargetResourceToArtifactMapping(t *testing.T) {
	// Create a target resource using the environment package
	targetResource := environment.NewTargetResource(
		"12345678-1234-1234-1234-123456789012",
		"test-rg",
		"test-app",
		"Microsoft.Web/sites",
	)

	var artifact *Artifact
	err := mapper.Convert(targetResource, &artifact)
	require.NoError(t, err)

	expectedResourceId := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/" +
		"Microsoft.Web/sites/test-app"
	require.Equal(t, ArtifactKindResource, artifact.Kind)
	require.Equal(t, expectedResourceId, artifact.Location)
	require.Equal(t, LocationKindRemote, artifact.LocationKind)
	require.Equal(t, "12345678-1234-1234-1234-123456789012", artifact.Metadata["subscriptionId"])
	require.Equal(t, "test-rg", artifact.Metadata["resourceGroup"])
	require.Equal(t, "test-app", artifact.Metadata["name"])
	require.Equal(t, "Microsoft.Web/sites", artifact.Metadata["type"])
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
