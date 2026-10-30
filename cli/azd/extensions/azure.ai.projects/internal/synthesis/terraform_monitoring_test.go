// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderTerraformMonitoring(t *testing.T) {
	const component = "/subscriptions/sub/resourceGroups/monitoring/providers/Microsoft.Insights/components/component"
	for _, existing := range []bool{false, true} {
		for _, mode := range []string{"none", "create", "reuse-connect", "already-connected"} {
			name := mode
			if existing {
				name = "existing/" + name
			}
			t.Run(name, func(t *testing.T) {
				result, err := RenderTerraformMonitoring(map[string]any{
					"monitoringMode": mode, "applicationInsightsResourceId": component,
				}, existing)
				require.NoError(t, err)
				if mode == "none" {
					assert.Empty(t, result.Files)
					assert.Empty(t, result.Variables)
					return
				}
				source := string(result.Files["monitoring.tf"])
				assert.Contains(t, source, `ephemeral "azapi_resource_action" "monitoring_credential"`)
				assert.Contains(t, source, `sensitive_body_version = { "properties.credentials.key" = "1" }`)
				assert.Contains(t, source, `ignore_not_found`)
				assert.NotContains(t, source, `response_export_values = ["*"]`)
				assert.NotContains(t, source, `ApplicationInsightsConnectionString`)
				assert.Contains(t, source, `count     = local.monitoring_managed ? 1 : 0`)
				assert.NotContains(t, source, `count = length(local.monitoring_appinsights)`)
				assert.Equal(t, mode, result.Variables["monitoring_mode"])
				assert.Equal(t, component, result.Variables["application_insights_resource_id"])
				assert.Equal(t, mode == "create", result.CreatesResources)
				if existing {
					assert.Contains(t, source, "= local.normalized_project_id")
					assert.NotContains(t, result.Files, "provider.tf", "retain the existing >=2.11 provider baseline")
					if mode == "create" {
						assert.Contains(t, result.Files, "adjunct-resource-group.tf")
					} else {
						assert.NotContains(t, result.Files, "adjunct-resource-group.tf")
					}
				} else {
					assert.Contains(t, string(result.Files["provider.tf"]), `required_version = ">= 1.11.0, < 2.0.0"`)
					assert.Contains(t, string(result.Files["provider.tf"]), `version = "~> 2.7"`)
					assert.Contains(t, source, "= azapi_resource.project.id")
				}
			})
		}
	}
}

func TestRenderTerraformMonitoringRejectsInvalidModes(t *testing.T) {
	for _, parameters := range []map[string]any{
		{"monitoringMode": false},
		{"monitoringMode": "invalid"},
		{"monitoringMode": "create"},
		{"monitoringMode": "reuse-connect", "applicationInsightsResourceId": ""},
		{"monitoringMode": "already-connected", "applicationInsightsResourceId": 42},
	} {
		_, err := RenderTerraformMonitoring(parameters, false)
		assert.Error(t, err)
	}
	result, err := RenderTerraformMonitoring(map[string]any{}, false)
	require.NoError(t, err)
	assert.Empty(t, result.Files, "older callers without monitoring retain their existing Terraform graph")
}
