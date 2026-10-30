// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ejectMonitoringEndpoint  = "https://acct.services.ai.azure.com/api/projects/p1"
	ejectMonitoringProjectID = "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/acct/projects/p1"
	ejectMonitoringComponentID = "/subscriptions/sub/resourceGroups/monitoring/providers/" +
		"Microsoft.Insights/components/byo"
)

func monitoringEjectFixture(t *testing.T, monitoring string) (string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "azure.yaml"), fmt.Sprintf(`name: monitoring-test
services:
  project:
    host: azure.ai.project
    endpoint: %s
    monitoring: {%s}
`, ejectMonitoringEndpoint, monitoring))
	return root, map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT": ejectMonitoringEndpoint,
		"AZURE_AI_PROJECT_ID":      ejectMonitoringProjectID,
	}
}

func TestEjectInfra_TerraformMonitoringIsOffline(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	for _, tt := range []struct {
		name       string
		monitoring string
		mode       string
		component  string
	}{
		{
			name: "BYO", monitoring: "applicationInsightsResourceId: " + ejectMonitoringComponentID,
			mode: "reuse-connect", component: ejectMonitoringComponentID,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root, environment := monitoringEjectFixture(t, tt.monitoring)
			withCapturedStdout(t, func() {
				require.NoError(t, ejectInfra(root, "terraform", environment))
			})
			data, err := os.ReadFile(filepath.Join(root, "infra", "main.tfvars.json"))
			require.NoError(t, err)
			var values map[string]any
			require.NoError(t, json.Unmarshal(data, &values))
			assert.Equal(t, tt.mode, values["monitoring_mode"])
			assert.Equal(t, tt.component, values["application_insights_resource_id"])
			assert.NoFileExists(t, filepath.Join(root, "infra", "adjunct-resource-group.tf"))
		})
	}
}

func TestEjectInfra_TerraformExistingProjectMonitoringCreateUnsupported(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	root, environment := monitoringEjectFixture(t, "enabled: true")
	err := ejectInfra(root, "terraform", environment)
	require.ErrorContains(t, err, "not supported")
	assert.NoDirExists(t, filepath.Join(root, "infra"))
}

func TestWriteInfraEject_UsesResolvedMonitoringParameters(t *testing.T) {
	root, environment := monitoringEjectFixture(t, "applicationInsightsResourceId: "+ejectMonitoringComponentID)
	prepared, err := prepareInfraEject(root, "terraform", environment)
	require.NoError(t, err)
	require.Equal(t, "reuse-connect", prepared.parameters["monitoringMode"])
	assert.NoDirExists(t, filepath.Join(root, "infra"))

	prepared.parameters["monitoringMode"] = "already-connected"
	withCapturedStdout(t, func() {
		require.NoError(t, writeInfraEject(root, "terraform", prepared))
	})
	data, err := os.ReadFile(filepath.Join(root, "infra", "main.tfvars.json"))
	require.NoError(t, err)
	var values map[string]any
	require.NoError(t, json.Unmarshal(data, &values))
	assert.Equal(t, "already-connected", values["monitoring_mode"])
	assert.Equal(t, ejectMonitoringComponentID, values["application_insights_resource_id"])
	assert.NoFileExists(t, filepath.Join(root, "infra", "adjunct-resource-group.tf"))
}

func TestWriteInfraEject_RejectsManifestChangedSincePreparation(t *testing.T) {
	root, environment := monitoringEjectFixture(t, "applicationInsightsResourceId: "+ejectMonitoringComponentID)
	prepared, err := prepareInfraEject(root, "terraform", environment)
	require.NoError(t, err)
	prepared.parameters["monitoringMode"] = "already-connected"
	changed := append(append([]byte(nil), prepared.rawYAML...), '\n')
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), changed, 0o600))

	err = writeInfraEject(root, "terraform", prepared)
	require.ErrorContains(t, err, "azure.yaml changed")
	assert.NoDirExists(t, filepath.Join(root, "infra"))
	actual, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
	require.NoError(t, err)
	assert.Equal(t, changed, actual)
}

func TestInitInfra_TerraformExistingProjectMonitoringCreateRejected(t *testing.T) {
	for _, standalone := range []bool{false, true} {
		t.Run(fmt.Sprintf("standalone=%t", standalone), func(t *testing.T) {
			t.Setenv("AZD_EXEC_PROJECT_DIR", "")
			root, environment := monitoringEjectFixture(t, "enabled: true")
			t.Chdir(root)
			environment["AZD_FOUNDRY_MONITORING_MODE"] = "create"
			environment["AZURE_APPLICATION_INSIGHTS_RESOURCE_ID"] = ejectMonitoringComponentID
			server := &testEnvironmentServiceServer{
				current: &azdext.Environment{Name: "dev"},
				values:  map[string]map[string]string{"dev": environment},
			}
			var err error
			if standalone {
				t.Setenv("AZD_SERVER", newTestAzdServer(t, server, &testWorkflowServiceServer{}))
				command := newInitCommand(&azdext.ExtensionContext{})
				command.SetArgs([]string{"--infra=terraform"})
				command.SetOut(io.Discard)
				command.SetErr(io.Discard)
				err = command.Execute()
			} else {
				client := newTestAzdClient(t, server, &testWorkflowServiceServer{})
				err = ejectInfraAfterInit(t.Context(), "terraform", client)
			}
			require.ErrorContains(t, err, "creating Application Insights for an existing project is not supported")
			assert.NoDirExists(t, filepath.Join(root, "infra"))
			unchanged, readErr := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, readErr)
			assert.Contains(t, string(unchanged), "monitoring: {enabled: true}")
		})
	}
}
