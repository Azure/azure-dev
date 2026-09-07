// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"os"
	"path/filepath"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOnDiskConnectionsUsePayloadNamesAndServiceKeyScopes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	raw := []byte(`services:
  project:
    host: azure.ai.project
  connection-service:
    host: azure.ai.connection
    name: '  Deployed Connection  '
    category: RemoteTool
    target: ${ENDPOINT}
    authType: ApiKey
    credentials: {key: '${KEY}'}
    env:
      ENDPOINT: ${SERVICE_ENDPOINT}
      KEY: ${SERVICE_KEY}
  isolated-service:
    host: azure.ai.connection
    name: Isolated
    category: RemoteTool
    target: ${ENDPOINT}
    authType: ApiKey
    credentials: {key: '${KEY}'}
    env: {}
`)
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), raw, 0o600))
	infraDir := filepath.Join(root, onDiskInfraDir)
	require.NoError(t, os.MkdirAll(infraDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepFile), []byte("// bicep\n"), 0o600))
	// Generate the same public and secure parameter shapes as init --infra.
	ejected, err := synthesis.Synthesize(synthesis.Input{
		RawAzureYAML: raw, ServiceName: "project", ProjectRoot: root, PreserveVarRefs: true,
	})
	require.NoError(t, err)
	params := minimalARMParametersFile(t, ejected.Parameters)
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskParamsFile), []byte(params), 0o600))
	client := newValidateTestClient(t,
		&validateStubProjectServer{project: &azdext.ProjectConfig{
			Path: root,
			Services: map[string]*azdext.ServiceConfig{
				"connection-service": {Environment: map[string]string{
					"ENDPOINT": "https://service.example", "KEY": "service-key",
				}},
				// A different service happens to be keyed by the resource name.
				"Deployed Connection": {Environment: map[string]string{"KEY": "wrong-service-key"}},
			},
		}},
		&validateStubEnvServer{envName: "test", get: map[string]string{
			envKeySubscriptionID: "sub-id", envKeyLocation: "eastus",
			"ENDPOINT": "https://project.example", "KEY": "project-key",
		}},
	)
	provider := &FoundryProvisioningProvider{
		azdClient:        client,
		bicepCliInstance: &stubCompiler{buildResult: bicep.BuildResult{Compiled: minimalARMTemplate()}},
	}
	require.NoError(t, provider.Initialize(t.Context(), root, &azdext.ProvisioningOptions{Provider: FoundryProviderName}))
	source, err := provider.resolveTemplate(t.Context(), func(string) {})
	require.NoError(t, err)
	asMap := func(value any) map[string]any {
		t.Helper()
		result, ok := value.(map[string]any)
		require.True(t, ok, "expected object, got %T", value)
		return result
	}
	connections, ok := asMap(source.parameters["connections"])["value"].([]any)
	require.True(t, ok)
	require.Len(t, connections, 2)
	assert.Equal(t, "Deployed Connection", asMap(connections[0])["name"])
	assert.Equal(t, "https://service.example", asMap(connections[0])["target"])
	assert.Equal(t, "Isolated", asMap(connections[1])["name"])
	assert.Equal(t, "", asMap(connections[1])["target"])
	credentials := asMap(asMap(source.parameters["connectionCredentials"])["value"])
	require.Len(t, credentials, 2)
	assert.Equal(t, "service-key", asMap(credentials["Deployed Connection"])["key"])
	assert.Equal(t, "", asMap(credentials["Isolated"])["key"])
	assert.NotContains(t, credentials, "connection-service")
}
