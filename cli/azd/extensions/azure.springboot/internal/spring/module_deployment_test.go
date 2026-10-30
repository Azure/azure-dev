// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

type fakeBicepCompiler struct {
	result bicep.BuildResult
	err    error
	path   string
}

func (c *fakeBicepCompiler) Build(_ context.Context, path string) (bicep.BuildResult, error) {
	c.path = path
	return c.result, c.err
}

type fakeARMDeploymentExecutor struct {
	subscriptionID string
	resourceGroup  string
	deploymentName string
	template       map[string]any
	parameters     map[string]any
	outputs        map[string]any
	err            error
}

type staticTokenCredential struct{}

func (staticTokenCredential) GetToken(
	context.Context,
	policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "test-token",
		ExpiresOn: time.Now().Add(time.Hour),
	}, nil
}

type captureARMTransport struct {
	body       map[string]any
	statusCode int
	response   string
}

func (t *captureARMTransport) Do(request *http.Request) (*http.Response, error) {
	rawBody, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rawBody, &t.body); err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: t.statusCode,
		Status:     http.StatusText(t.statusCode),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(t.response)),
		Request:    request,
	}, nil
}

func (e *fakeARMDeploymentExecutor) Deploy(
	_ context.Context,
	subscriptionID string,
	resourceGroup string,
	deploymentName string,
	template map[string]any,
	parameters map[string]any,
) (map[string]any, error) {
	e.subscriptionID = subscriptionID
	e.resourceGroup = resourceGroup
	e.deploymentName = deploymentName
	e.template = template
	e.parameters = parameters
	return e.outputs, e.err
}

func TestAzureModuleDeployerCompilesNestedParametersAndMapsOutputs(t *testing.T) {
	project, service := moduleFixture(t, "custom/petclinic")
	compiler := &fakeBicepCompiler{result: bicep.BuildResult{
		Compiled: `{"$schema":"https://schema.management.azure.com/schemas/2019-04-01/` +
			`deploymentTemplate.json#","contentVersion":"1.0.0.0","resources":[]}`,
	}}
	executor := &fakeARMDeploymentExecutor{outputs: map[string]any{
		"SERVICE_PETCLINIC_URI": map[string]any{
			"type":  "String",
			"value": "https://petclinic.example",
		},
		"DO_NOT_PERSIST_SECRET": map[string]any{
			"type":  "SecureString",
			"value": "secret-password",
		},
	}}
	deployer := &azureModuleDeployer{compiler: compiler, executor: executor}

	result, err := deployer.Deploy(t.Context(), &moduleDeploymentRequest{
		SubscriptionID:  "sub",
		ResourceGroup:   "custom-rg",
		ServiceName:     "petclinic",
		Project:         project,
		Service:         service,
		EnvironmentName: "dev",
		EnvironmentValue: map[string]string{
			"AZURE_ENV_NAME":                   "dev",
			"SPRING_DATABASE_PASSWORD":         "secret-password",
			"SERVICE_PETCLINIC_IMAGE_NAME":     "registry.example/petclinic:tag",
			"AZURE_CONTAINER_REGISTRY_NAME":    "customregistry",
			"AZURE_CONTAINER_ENVIRONMENT_NAME": "custom-environment",
			"SERVICE_PETCLINIC_IDENTITY_ID": "/subscriptions/sub/resourceGroups/custom-rg/providers/" +
				"Microsoft.ManagedIdentity/userAssignedIdentities/custom",
			"AZURE_CONTAINER_APP_NAME": "custom-app",
			"SPRING_DATABASE_TYPE":     "postgresql",
			"AZURE_POSTGRES_HOST":      "postgres.example",
		},
		ImageName: "registry.example/petclinic:tag",
	}, func(string) {})

	require.NoError(t, err)
	expectedModule, err := os.Stat(filepath.Join(project.Path, "infra", "custom", "petclinic.bicep"))
	require.NoError(t, err)
	actualModule, err := os.Stat(compiler.path)
	require.NoError(t, err)
	require.True(t, os.SameFile(expectedModule, actualModule))
	require.Equal(t, "sub", executor.subscriptionID)
	require.Equal(t, "custom-rg", executor.resourceGroup)
	require.Contains(t, executor.deploymentName, "azd-petclinic-")
	password := executor.parameters["databasePassword"].(map[string]any)["value"]
	require.Equal(t, "secret-password", password)
	image := executor.parameters["imageName"].(map[string]any)["value"]
	require.Equal(t, "registry.example/petclinic:tag", image)
	require.Equal(t, "https://petclinic.example", result.Outputs["SERVICE_PETCLINIC_URI"])
	require.NotContains(t, result.Outputs, "DO_NOT_PERSIST_SECRET")
}

func TestLoadServiceModuleParametersRejectsFlattenedAndRefValues(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "flattened",
			content: `{"parameters":{"imageName":"image"}}`,
			wantErr: "canonical",
		},
		{
			name:    "ref",
			content: `{"parameters":{"imageName":{"$ref":"secret"}}}`,
			wantErr: "only value",
		},
		{
			name:    "unknown root",
			content: `{"parameters":{},"variables":{}}`,
			wantErr: "unsupported root field",
		},
		{
			name:    "missing required value",
			content: `{"parameters":{"imageName":{"value":"${SERVICE_API_IMAGE_NAME}"}}}`,
			wantErr: "requires environment value",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "service.parameters.json")
			require.NoError(t, os.WriteFile(path, []byte(test.content), 0o600))
			_, err := loadServiceModuleParameters(path, map[string]string{})
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestResolveServiceModulePathsRejectsEscapeAndBicepParam(t *testing.T) {
	t.Run("escape", func(t *testing.T) {
		project, service := moduleFixture(t, "petclinic")
		properties, err := structpb.NewStruct(map[string]any{"module": "../outside"})
		require.NoError(t, err)
		service.AdditionalProperties = properties

		_, _, err = resolveServiceModulePaths(project, service)
		require.ErrorContains(t, err, "invalid module")
	})

	t.Run("bicepparam", func(t *testing.T) {
		project, service := moduleFixture(t, "petclinic")
		require.NoError(t, os.WriteFile(
			filepath.Join(project.Path, "infra", "petclinic.bicepparam"),
			[]byte("using './petclinic.bicep'"),
			0o600,
		))

		_, _, err := resolveServiceModulePaths(project, service)
		require.ErrorContains(t, err, ".bicepparam")
	})
}

func TestAzureModuleDeployerRedactsCredentialBearingURLs(t *testing.T) {
	project, service := moduleFixture(t, "petclinic")
	deployer := &azureModuleDeployer{
		compiler: &fakeBicepCompiler{
			err: errors.New("download https://user:password@example.test/tool?sig=secret#fragment failed"),
		},
		executor: &fakeARMDeploymentExecutor{},
	}

	_, err := deployer.Deploy(t.Context(), &moduleDeploymentRequest{
		Project: project,
		Service: service,
		EnvironmentValue: map[string]string{
			"AZURE_ENV_NAME":                   "dev",
			"AZURE_CONTAINER_REGISTRY_NAME":    "registry",
			"AZURE_CONTAINER_ENVIRONMENT_NAME": "environment",
			"SERVICE_PETCLINIC_IDENTITY_ID":    "identity",
			"AZURE_CONTAINER_APP_NAME":         "app",
			"SPRING_DATABASE_TYPE":             "h2",
			"SERVICE_PETCLINIC_IMAGE_NAME":     "registry.example/app:tag",
		},
	}, func(string) {})

	require.Error(t, err)
	require.NotContains(t, err.Error(), "user")
	require.NotContains(t, err.Error(), "password")
	require.NotContains(t, err.Error(), "sig=")
	require.NotContains(t, err.Error(), "secret")
	require.NotContains(t, err.Error(), "fragment")
	require.Contains(t, err.Error(), "https://example.test/tool")
}

func TestAzureARMDeploymentExecutorUsesIncrementalModeAndReturnsOutputs(t *testing.T) {
	transport := &captureARMTransport{
		statusCode: http.StatusOK,
		response: `{
  "id": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Resources/deployments/deployment",
  "name": "deployment",
  "type": "Microsoft.Resources/deployments",
  "properties": {
    "provisioningState": "Succeeded",
    "outputs": {
      "SERVICE_PETCLINIC_URI": {"type": "String", "value": "https://app.example.test"}
    }
  }
}`,
	}
	executor := &azureARMDeploymentExecutor{
		credential: func(
			context.Context,
			*azdext.AzdClient,
			string,
		) (azcore.TokenCredential, error) {
			return staticTokenCredential{}, nil
		},
		options: &arm.ClientOptions{
			ClientOptions: policy.ClientOptions{Transport: transport},
		},
	}

	outputs, err := executor.Deploy(
		t.Context(),
		"sub",
		"rg",
		"deployment",
		map[string]any{"resources": []any{}},
		map[string]any{
			"databasePassword": map[string]any{"value": "secret-password"},
		},
	)

	require.NoError(t, err)
	properties := transport.body["properties"].(map[string]any)
	require.Equal(t, "Incremental", properties["mode"])
	parameters := properties["parameters"].(map[string]any)
	require.Equal(t, "secret-password", parameters["databasePassword"].(map[string]any)["value"])
	require.Equal(
		t,
		"https://app.example.test",
		outputs["SERVICE_PETCLINIC_URI"].(map[string]any)["value"],
	)
}

func TestAzureARMDeploymentExecutorPropagatesFailedDeployment(t *testing.T) {
	transport := &captureARMTransport{
		statusCode: http.StatusBadRequest,
		response: `{
  "error": {
    "code": "InvalidTemplateDeployment",
    "message": "template validation failed"
  }
}`,
	}
	executor := &azureARMDeploymentExecutor{
		credential: func(
			context.Context,
			*azdext.AzdClient,
			string,
		) (azcore.TokenCredential, error) {
			return staticTokenCredential{}, nil
		},
		options: &arm.ClientOptions{
			ClientOptions: policy.ClientOptions{Transport: transport},
		},
	}

	_, err := executor.Deploy(
		t.Context(),
		"sub",
		"rg",
		"deployment",
		map[string]any{"resources": []any{}},
		map[string]any{},
	)

	require.Error(t, err)
	require.ErrorContains(t, err, "InvalidTemplateDeployment")
}

func TestRealBicepCompilerBuildsServiceModule(t *testing.T) {
	if os.Getenv("SPRING_TEST_REAL_BICEP") != "1" {
		t.Skip("set SPRING_TEST_REAL_BICEP=1 to run the public Bicep CLI integration test")
	}
	path := filepath.Join(t.TempDir(), "service.bicep")
	require.NoError(t, os.WriteFile(path, []byte(`
param imageName string
output SERVICE_TEST_IMAGE string = imageName
`), 0o600))
	deployer := newAzureModuleDeployer(nil, io.Discard).(*azureModuleDeployer)

	result, err := deployer.compiler.Build(t.Context(), path)

	require.NoError(t, err)
	var template map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Compiled), &template))
	require.Contains(t, template["$schema"], "deploymentTemplate.json")
	require.Contains(t, template["parameters"].(map[string]any), "imageName")
	require.Contains(t, template["outputs"].(map[string]any), "SERVICE_TEST_IMAGE")
}

func moduleFixture(t *testing.T, module string) (*azdext.ProjectConfig, *azdext.ServiceConfig) {
	t.Helper()
	root := t.TempDir()
	modulePath := filepath.Join(root, "infra", filepath.FromSlash(module))
	require.NoError(t, os.MkdirAll(filepath.Dir(modulePath), 0o755))
	require.NoError(t, os.WriteFile(modulePath+".bicep", []byte("param imageName string"), 0o600))
	parameters := `{
  "parameters": {
    "environmentName": {"value": "${AZURE_ENV_NAME}"},
    "containerRegistryName": {"value": "${AZURE_CONTAINER_REGISTRY_NAME}"},
    "containerAppsEnvironmentName": {"value": "${AZURE_CONTAINER_ENVIRONMENT_NAME}"},
    "identityId": {"value": "${SERVICE_PETCLINIC_IDENTITY_ID}"},
    "containerAppName": {"value": "${AZURE_CONTAINER_APP_NAME}"},
    "databaseType": {"value": "${SPRING_DATABASE_TYPE:-h2}"},
    "databasePassword": {"value": "${SPRING_DATABASE_PASSWORD:-}"},
    "postgresHost": {"value": "${AZURE_POSTGRES_HOST:-}"},
    "postgresDatabase": {"value": "${AZURE_POSTGRES_DATABASE:-petclinic}"},
    "postgresUser": {"value": "${AZURE_POSTGRES_USER:-petclinicadmin}"},
    "imageName": {"value": "${SERVICE_PETCLINIC_IMAGE_NAME}"}
  }
}`
	require.NoError(t, os.WriteFile(modulePath+".parameters.json", []byte(parameters), 0o600))
	properties, err := structpb.NewStruct(map[string]any{"module": module})
	require.NoError(t, err)
	service := &azdext.ServiceConfig{
		Name:                 "petclinic",
		Host:                 SpringHost,
		AdditionalProperties: properties,
	}
	project := &azdext.ProjectConfig{
		Path: root,
		Infra: &azdext.InfraOptions{
			Provider: "bicep",
			Path:     "infra",
			Module:   "main",
		},
		Services: map[string]*azdext.ServiceConfig{"petclinic": service},
	}
	return project, service
}
