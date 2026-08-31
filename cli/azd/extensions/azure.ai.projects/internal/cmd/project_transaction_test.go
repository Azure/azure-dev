// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestRollbackProjectInitUsesApplicationOrder(t *testing.T) {
	var order []string
	operationErr := errors.New("operation failed")

	err := rollbackProjectInit(
		operationErr,
		func() error {
			order = append(order, "environment")
			return nil
		},
		func() error {
			order = append(order, "service")
			return nil
		},
		func() error {
			order = append(order, "infra")
			return nil
		},
	)

	require.ErrorIs(t, err, operationErr)
	assert.Equal(t, []string{"environment", "service", "infra"}, order)
}

func TestReconcileProjectEnvironmentRollbackRestoresValues(t *testing.T) {
	envServer := &transactionEnvironmentServer{
		values: map[string]string{
			"AZURE_LOCATION":          "westus2",
			"USE_EXISTING_AI_PROJECT": "true",
		},
		failAt: 2,
	}
	client := newProjectEnvironmentClient(t, envServer)

	_, err := reconcileProjectEnvironmentWithRollback(
		t.Context(),
		client,
		"test",
		projectModeNew,
		&resolvedProject{Location: "eastus"},
		false,
	)

	require.Error(t, err)
	assert.Equal(t, "westus2", envServer.values["AZURE_LOCATION"])
	assert.Equal(t, "true", envServer.values["USE_EXISTING_AI_PROJECT"])
	assert.Equal(t, "", envServer.values["AZURE_AI_DEPLOYMENTS_LOCATION"])
	assert.Equal(t, 5, envServer.calls)
}

func TestReconcileProjectServiceRollbackRestoresSection(t *testing.T) {
	section, err := structpb.NewStruct(map[string]any{
		"project": map[string]any{
			"endpoint": "https://account.services.ai.azure.com/api/projects/old",
			"custom":   "preserve",
		},
	})
	require.NoError(t, err)

	projectServer := &transactionProjectServer{
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"project": {Name: "project", Host: aiProjectHost},
			},
		},
		section: section,
	}
	client := newTransactionProjectClient(t, projectServer)
	reconciler := &projectServiceReconciler{client: client}

	name, mutation, rollback, err := reconciler.reconcileEndpoint(
		t.Context(),
		"project",
		"https://account.services.ai.azure.com/api/projects/new",
		projectModeExistingID,
	)
	require.NoError(t, err)
	assert.Equal(t, "project", name)
	assert.Equal(t, "updated", mutation)
	require.NotNil(t, projectServer.serviceValue)

	require.NoError(t, rollback())
	require.NotNil(t, projectServer.serviceSection)
	assert.Equal(t, map[string]any{
		"endpoint": "https://account.services.ai.azure.com/api/projects/old",
		"custom":   "preserve",
	}, projectServer.serviceSection.Section.AsMap())
}

func TestEjectExistingProjectBicepAcceptsPostInitIdentity(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "azure.yaml"),
		[]byte(`name: test
services:
  project:
    host: azure.ai.project
    endpoint: https://account.services.ai.azure.com/api/projects/new
`),
		0600,
	))
	projectServer := &transactionProjectServer{
		project: &azdext.ProjectConfig{
			Name:  "test",
			Path:  root,
			Infra: &azdext.InfraOptions{Provider: provisioningFoundryProvider},
		},
	}
	client := newTransactionProjectClient(t, projectServer)
	oldID := "/subscriptions/old/resourceGroups/old/providers/" +
		"Microsoft.CognitiveServices/accounts/old/projects/old"
	newID := "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/new"

	env := map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT": "https://account.services.ai.azure.com/api/projects/old",
		"AZURE_AI_PROJECT_ID":      oldID,
		"AZD_AGENT_SKIP_ACR":       "true",
	}
	err := ejectExistingProjectInfra(
		t.Context(),
		client,
		root,
		"project",
		"bicep",
		mustReadProjectFile(t, root),
		"https://account.services.ai.azure.com/api/projects/new",
		newID,
		env,
	)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(root, "infra", "main.bicep"))
}

func TestEjectExistingProjectRollbackRemovesStagedFiles(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "azure.yaml"),
		[]byte(`name: test
services:
  project:
    host: azure.ai.project
    endpoint: https://account.services.ai.azure.com/api/projects/project
`),
		0600,
	))
	projectServer := &transactionProjectServer{
		project:     &azdext.ProjectConfig{Name: "test", Path: root},
		unsetErrors: []error{errors.New("stamp failed"), nil, nil},
	}
	client := newTransactionProjectClient(t, projectServer)
	resourceID := "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"

	err := ejectExistingProjectInfra(
		t.Context(),
		client,
		root,
		"project",
		"bicep",
		mustReadProjectFile(t, root),
		"https://account.services.ai.azure.com/api/projects/project",
		resourceID,
		map[string]string{"AZD_AGENT_SKIP_ACR": "true"},
	)

	require.Error(t, err)
	assert.NoDirExists(t, filepath.Join(root, "infra"))
	assert.Len(t, projectServer.unsetPaths, 3)
}

func TestWriteExistingProjectTerraformAcrModes(t *testing.T) {
	params := map[string]any{
		"deployments":           []synthesis.Deployment{},
		"connections":           []synthesis.Connection{},
		"connectionCredentials": map[string]map[string]any{},
	}
	tests := []struct {
		name       string
		mode       projectEjectAcrMode
		connection string
		wantFile   bool
	}{
		{name: "none", mode: projectEjectAcrNone, wantFile: false},
		{name: "create", mode: projectEjectAcrCreate, wantFile: true},
		{name: "reuse", mode: projectEjectAcrReuseConnect, wantFile: true},
		{
			name:       "already connected",
			mode:       projectEjectAcrAlreadyConnected,
			connection: "existing-connection",
			wantFile:   false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			values := map[string]string{
				"AZURE_CONTAINER_REGISTRY_ENDPOINT": "https://registry.azurecr.io",
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": "/subscriptions/sub/resourceGroups/rg/providers/" +
					"Microsoft.ContainerRegistry/registries/registry",
				"AZURE_AI_PROJECT_ACR_CONNECTION_NAME": test.connection,
			}
			require.NoError(t, writeExistingProjectTerraform(
				dir, "main", params, test.mode, values,
			))
			registryPath := filepath.Join(dir, "container-registry.tf")
			if test.wantFile {
				assert.FileExists(t, registryPath)
			} else {
				assert.NoFileExists(t, registryPath)
			}
			outputs, err := os.ReadFile(filepath.Join(dir, "outputs.tf"))
			require.NoError(t, err)
			assert.Contains(t, string(outputs), `AZD_FOUNDRY_ACR_MODE`)
		})
	}
}

func TestResolveProjectEjectAcrMode(t *testing.T) {
	const (
		endpoint   = "https://registry.azurecr.io"
		resourceID = "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.ContainerRegistry/registries/registry"
	)
	tests := []struct {
		name      string
		params    map[string]any
		values    map[string]string
		want      projectEjectAcrMode
		wantError bool
	}{
		{
			name:   "no registry required",
			params: map[string]any{"includeAcr": false},
			want:   projectEjectAcrNone,
		},
		{
			name:   "skip registry",
			params: map[string]any{"includeAcr": true},
			values: map[string]string{"AZD_AGENT_SKIP_ACR": "true"},
			want:   projectEjectAcrNone,
		},
		{
			name:   "create by default",
			params: map[string]any{"includeAcr": true},
			want:   projectEjectAcrCreate,
		},
		{
			name:   "reuse existing registry",
			params: map[string]any{"includeAcr": true},
			values: map[string]string{
				"AZURE_CONTAINER_REGISTRY_ENDPOINT":    endpoint,
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": resourceID,
			},
			want: projectEjectAcrReuseConnect,
		},
		{
			name:   "keep existing connection",
			params: map[string]any{"includeAcr": true},
			values: map[string]string{
				"AZURE_CONTAINER_REGISTRY_ENDPOINT":    endpoint,
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": resourceID,
				"AZURE_AI_PROJECT_ACR_CONNECTION_NAME": "registry-connection",
			},
			want: projectEjectAcrAlreadyConnected,
		},
		{
			name:   "incomplete existing registry",
			params: map[string]any{"includeAcr": true},
			values: map[string]string{
				"AZURE_CONTAINER_REGISTRY_ENDPOINT": endpoint,
			},
			wantError: true,
		},
		{
			name:   "invalid explicit mode",
			params: map[string]any{"includeAcr": true},
			values: map[string]string{
				"AZD_FOUNDRY_ACR_MODE": "invalid",
			},
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveProjectEjectAcrMode(test.params, test.values)
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestWriteExistingProjectBicepAcrModes(t *testing.T) {
	params := map[string]any{
		"deployments":           []synthesis.Deployment{},
		"connections":           []synthesis.Connection{},
		"connectionCredentials": map[string]map[string]any{},
	}
	tests := []struct {
		name     string
		mode     projectEjectAcrMode
		values   map[string]string
		wantFile bool
	}{
		{name: "none", mode: projectEjectAcrNone, wantFile: false},
		{name: "create", mode: projectEjectAcrCreate, wantFile: true},
		{
			name: "reuse",
			mode: projectEjectAcrReuseConnect,
			values: map[string]string{
				"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "https://registry.azurecr.io",
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerRegistry/registries/registry",
			},
			wantFile: true,
		},
		{
			name: "already connected",
			mode: projectEjectAcrAlreadyConnected,
			values: map[string]string{
				"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "https://registry.azurecr.io",
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerRegistry/registries/registry",
				"AZURE_AI_PROJECT_ACR_CONNECTION_NAME": "registry-connection",
			},
			wantFile: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, writeExistingProjectBicep(
				dir, "main", params, test.mode, test.values,
			))
			registryPath := filepath.Join(
				dir, "modules", "container-registry.bicep",
			)
			if test.wantFile {
				assert.FileExists(t, registryPath)
			} else {
				assert.NoFileExists(t, registryPath)
			}
			assert.FileExists(t, filepath.Join(dir, "main.bicep"))
			assert.FileExists(t, filepath.Join(dir, "main.parameters.json"))
			assert.FileExists(t, filepath.Join(
				dir, "modules", "foundry-project.bicep",
			))
			assert.NoFileExists(t, filepath.Join(
				dir, "modules", "network.bicep",
			))
		})
	}
}

func mustReadProjectFile(t *testing.T, root string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
	require.NoError(t, err)
	return raw
}

const provisioningFoundryProvider = "microsoft.foundry"

type transactionEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	values map[string]string
	failAt int
	calls  int
}

func (s *transactionEnvironmentServer) Select(
	context.Context,
	*azdext.SelectEnvironmentRequest,
) (*azdext.EmptyResponse, error) {
	return &azdext.EmptyResponse{}, nil
}

func (s *transactionEnvironmentServer) GetValues(
	context.Context,
	*azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	response := &azdext.KeyValueListResponse{}
	for key, value := range s.values {
		response.KeyValues = append(response.KeyValues, &azdext.KeyValue{
			Key: key, Value: value,
		})
	}
	return response, nil
}

func (s *transactionEnvironmentServer) SetValue(
	_ context.Context,
	request *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	s.calls++
	if s.failAt == s.calls {
		return nil, errors.New("environment write failed")
	}
	s.values[request.Key] = request.Value
	return &azdext.EmptyResponse{}, nil
}

type transactionProjectServer struct {
	azdext.UnimplementedProjectServiceServer
	project        *azdext.ProjectConfig
	section        *structpb.Struct
	serviceValue   *azdext.SetServiceConfigValueRequest
	serviceSection *azdext.SetServiceConfigSectionRequest
	setConfig      []*azdext.SetProjectConfigValueRequest
	unsetPaths     []string
	unsetErrors    []error
}

func (s *transactionProjectServer) Get(
	context.Context,
	*azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{Project: s.project}, nil
}

func (s *transactionProjectServer) GetConfigSection(
	context.Context,
	*azdext.GetProjectConfigSectionRequest,
) (*azdext.GetProjectConfigSectionResponse, error) {
	return &azdext.GetProjectConfigSectionResponse{
		Section: s.section,
		Found:   s.section != nil,
	}, nil
}

func (s *transactionProjectServer) AddService(
	context.Context,
	*azdext.AddServiceRequest,
) (*azdext.EmptyResponse, error) {
	return &azdext.EmptyResponse{}, nil
}

func (s *transactionProjectServer) SetServiceConfigValue(
	_ context.Context,
	request *azdext.SetServiceConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	s.serviceValue = request
	return &azdext.EmptyResponse{}, nil
}

func (s *transactionProjectServer) SetServiceConfigSection(
	_ context.Context,
	request *azdext.SetServiceConfigSectionRequest,
) (*azdext.EmptyResponse, error) {
	s.serviceSection = request
	return &azdext.EmptyResponse{}, nil
}

func (s *transactionProjectServer) SetConfigValue(
	_ context.Context,
	request *azdext.SetProjectConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	s.setConfig = append(s.setConfig, request)
	return &azdext.EmptyResponse{}, nil
}

func (s *transactionProjectServer) UnsetConfig(
	_ context.Context,
	request *azdext.UnsetProjectConfigRequest,
) (*azdext.EmptyResponse, error) {
	s.unsetPaths = append(s.unsetPaths, request.Path)
	if len(s.unsetErrors) > 0 {
		err := s.unsetErrors[0]
		s.unsetErrors = s.unsetErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	return &azdext.EmptyResponse{}, nil
}

func newTransactionProjectClient(
	t *testing.T,
	projectServer azdext.ProjectServiceServer,
) *azdext.AzdClient {
	t.Helper()
	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, projectServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	client, err := azdext.NewAzdClient(
		azdext.WithAddress(listener.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}
