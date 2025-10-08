// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/require"
)

func TestToProtoServiceConfig(t *testing.T) {
	t.Parallel()

	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_ENV_NAME": "my-env",
		"SERVICE_NAME":   "api-service",
	})

	projectConfig := &ProjectConfig{
		Name:              "contoso",
		ResourceGroupName: osutil.NewExpandableString("rg-${AZURE_ENV_NAME}"),
	}

	serviceConfig := &ServiceConfig{
		Name:              "api",
		Host:              ContainerAppTarget,
		Project:           projectConfig,
		RelativePath:      "./src/api",
		Language:          ServiceLanguageDocker,
		ResourceGroupName: osutil.NewExpandableString("rg-${SERVICE_NAME}"),
		ResourceName:      osutil.NewExpandableString("ca-${SERVICE_NAME}"),
		Image:             osutil.NewExpandableString("myregistry.azurecr.io/${SERVICE_NAME}:latest"),
		ApiVersion:        "2023-05-01",
		OutputPath:        "./dist",
	}

	externalTarget := &ExternalServiceTarget{env: env}
	protoConfig, err := externalTarget.toProtoServiceConfig(serviceConfig)
	require.NoError(t, err)
	require.NotNil(t, protoConfig)
	require.Equal(t, "api", protoConfig.Name)
	require.Equal(t, string(ContainerAppTarget), protoConfig.Host)
	require.Equal(t, string(ServiceLanguageDocker), protoConfig.Language)
	require.Equal(t, "./src/api", protoConfig.RelativePath)
	require.Equal(t, "2023-05-01", protoConfig.ApiVersion)
	require.Equal(t, "./dist", protoConfig.OutputPath)
	// Environment variables should be resolved
	require.Equal(t, "rg-api-service", protoConfig.ResourceGroupName)
	require.Equal(t, "ca-api-service", protoConfig.ResourceName)
	require.Equal(t, "myregistry.azurecr.io/api-service:latest", protoConfig.Image)
}

func TestToProtoServiceConfig_EmptyValues(t *testing.T) {
	t.Parallel()

	serviceConfig := &ServiceConfig{
		Name: "api",
		Host: ContainerAppTarget,
		Project: &ProjectConfig{
			Name: "contoso",
		},
	}

	protoConfig, err := (&ExternalServiceTarget{}).toProtoServiceConfig(serviceConfig)
	require.NoError(t, err)
	require.NotNil(t, protoConfig)
	require.Equal(t, "api", protoConfig.Name)
	require.Equal(t, string(ContainerAppTarget), protoConfig.Host)
	require.Empty(t, protoConfig.ResourceGroupName)
	require.Empty(t, protoConfig.ResourceName)
	require.Empty(t, protoConfig.Image)
	require.Empty(t, protoConfig.ApiVersion)
	require.Empty(t, protoConfig.RelativePath)
	require.Empty(t, protoConfig.OutputPath)
}

func TestToProtoServiceConfig_WithoutEnvironment(t *testing.T) {
	t.Parallel()

	serviceConfig := &ServiceConfig{
		Name:              "api",
		Host:              ContainerAppTarget,
		Project:           &ProjectConfig{Name: "contoso"},
		ResourceGroupName: osutil.NewExpandableString("rg-${SERVICE_NAME}"),
		ResourceName:      osutil.NewExpandableString("ca-${SERVICE_NAME}"),
		Image:             osutil.NewExpandableString("${REGISTRY}/${SERVICE_NAME}:${TAG}"),
	}

	// Without environment, variables should be replaced with empty strings
	protoConfig, err := (&ExternalServiceTarget{}).toProtoServiceConfig(serviceConfig)
	require.NoError(t, err)
	require.NotNil(t, protoConfig)
	require.Equal(t, "rg-", protoConfig.ResourceGroupName)
	require.Equal(t, "ca-", protoConfig.ResourceName)
	require.Equal(t, "/:", protoConfig.Image)
}
