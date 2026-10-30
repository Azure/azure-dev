// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"crypto/sha1" //nolint:gosec // Mirrors Terraform resource naming, not a security primitive.
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Terraform 1.11 cannot mock ephemeral resources. Exercise the unchanged read-only
// preflight graph here; full exports and credential wiring require live acceptance.
func TestTerraformMonitoringSafetyPlans(t *testing.T) {
	terraform := os.Getenv("AZD_TEST_TERRAFORM_PATH")
	if terraform == "" {
		t.Skip("set AZD_TEST_TERRAFORM_PATH to opt in to Terraform/provider integration tests")
	}
	rendered, err := RenderTerraformMonitoring(map[string]any{
		"monitoringMode": "create", "applicationInsightsResourceId": "",
	}, true)
	require.NoError(t, err)
	preflight, managed, found := strings.Cut(string(rendered.Files["monitoring.tf"]), "# Managed counts")
	require.True(t, found)
	require.Contains(t, managed, `ephemeral "azapi_resource_action"`)
	require.Contains(t, managed, `resource "azapi_resource" "monitoring_connection"`)
	preflight = strings.ReplaceAll(preflight, "azurerm_resource_group.adjunct.id", "local.test_group_id")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "monitoring.tf"), []byte(preflight), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(monitoringPlanTestRoot), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "monitoring.tftest.hcl"),
		[]byte(monitoringPlanCases(t)), 0o600))
	run := func(arguments ...string) {
		t.Helper()
		//nolint:gosec // This opt-in test explicitly selects the Terraform executable.
		cmd := exec.CommandContext(t.Context(), terraform, arguments...)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
		t.Logf("%s", output)
	}
	initArguments := []string{"init", "-backend=false", "-input=false", "-no-color"}
	if pluginDir := os.Getenv("AZD_TEST_TERRAFORM_PLUGIN_DIR"); pluginDir != "" {
		initArguments = append(initArguments, "-plugin-dir="+pluginDir)
	}
	run(initArguments...)
	run("test", "-no-color")
}

const (
	monitoringPlanAccount = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/project/" +
		"providers/Microsoft.CognitiveServices/accounts/account"
	monitoringPlanProject  = monitoringPlanAccount + "/projects/project"
	monitoringPlanGroup    = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/monitoring"
	monitoringPlanTestRoot = `
terraform {
  required_version = ">= 1.11.0, < 2.0.0"
  required_providers {
    azapi = { source = "Azure/azapi", version = "~> 2.7" }
  }
}
variable "resource_token_salt" { default = "" }
variable "environment_name" { default = "guard-test" }
variable "tags" { default = {} }
locals {
  normalized_project_id = "` + monitoringPlanProject + `"
  foundry_account_id = "` + monitoringPlanAccount + `"
  test_group_id = "` + monitoringPlanGroup + `"
}
`
)

func monitoringPlanCases(t *testing.T) string {
	t.Helper()
	//nolint:gosec // Mirrors the production Terraform naming expression.
	digest := sha1.Sum([]byte(strings.ToLower(monitoringPlanProject + "|" + monitoringPlanGroup + "|")))
	token := hex.EncodeToString(digest[:])[:13]
	component := monitoringPlanGroup + "/providers/Microsoft.Insights/components/appi-" + token
	workspace := monitoringPlanGroup + "/providers/Microsoft.OperationalInsights/workspaces/log-" + token
	ownedTags := map[string]any{
		"azd-created-by": "azure.ai.projects", "azd-environment": "guard-test",
		"azd-monitoring-project": monitoringPlanProject,
	}
	connection := func(name, target, auth string, shared bool) map[string]any {
		return map[string]any{
			"name": name, "category": "AppInsights", "target": target, "auth": auth, "shared": shared,
			"restricted": 0, "resource_id": nil, "creator": nil, "environment": nil, "project": nil,
		}
	}
	name := MonitoringConnectionName("project")
	owned := connection(name, component, "ApiKey", false)
	maps.Copy(owned, map[string]any{
		"resource_id": component, "creator": "azure.ai.projects", "environment": "guard-test",
		"project": monitoringPlanProject,
	})
	changed := func(row map[string]any, key string, value any) map[string]any {
		result := maps.Clone(row)
		result[key] = value
		return result
	}
	type planCase struct {
		name        string
		mode        string
		project     []any
		account     []any
		slot        map[string]any
		component   map[string]any
		workspace   map[string]any
		accountRule map[string]any
		owned       bool
		missingRows bool
		failure     string
	}
	cases := []planCase{
		{name: "fresh_create", mode: "create"},
		{name: "missing_connection_reuse", mode: "reuse-connect"},
		{name: "disabled_mode", mode: "none"},
		{
			name: "preserve_managed_identity", mode: "already-connected",
			project:   []any{connection("external", component, "ManagedIdentity", false)},
			component: map[string]any{"local_auth": true, "ingestion": "Disabled"},
		},
		{
			name: "preserve_inherited_aad", mode: "already-connected",
			account:   []any{connection("shared", component, "AAD", true)},
			component: map[string]any{"local_auth": true},
		},
		{
			name: "preserve_account_mirror", mode: "already-connected",
			project: []any{connection("external", component, "ApiKey", false)},
			account: []any{connection("external", component, "ApiKey", false)},
		},
		{
			name: "ignore_unshared_sibling", mode: "create",
			account: []any{connection("azd-appinsights-sibling", component+"-sibling", "ApiKey", false)},
		},
		{
			name: "owned_repeat_create", mode: "create", owned: true,
			project: []any{owned}, account: []any{owned}, slot: owned,
		},
		{name: "missing_discovery_list", mode: "create", missingRows: true, failure: "terraform_data.monitoring_safety[0]"},
		{
			name: "component_identity_mismatch", mode: "reuse-connect",
			component: map[string]any{"resource_id": component + "-different"},
			failure:   "data.azapi_resource.monitoring_component[0]",
		},
		{
			name: "workspace_identity_mismatch", mode: "reuse-connect",
			workspace: map[string]any{"resource_id": workspace + "-different"},
			failure:   "data.azapi_resource.monitoring_workspace[0]",
		},
		{
			name: "component_auth_disabled", mode: "reuse-connect",
			component: map[string]any{"local_auth": true}, failure: "terraform_data.monitoring_safety[0]",
		},
		{
			name: "component_ingestion_restricted", mode: "reuse-connect",
			component: map[string]any{"ingestion": "Disabled"}, failure: "terraform_data.monitoring_safety[0]",
		},
		{
			name: "workspace_ingestion_restricted", mode: "reuse-connect",
			workspace: map[string]any{"public_access": "Disabled"}, failure: "terraform_data.monitoring_safety[0]",
		},
		{
			name: "workspace_auth_disabled", mode: "reuse-connect",
			workspace: map[string]any{"workspace_local_auth": true}, failure: "terraform_data.monitoring_safety[0]",
		},
		{
			name: "account_identity_mismatch", mode: "create",
			accountRule: map[string]any{"resource_id": monitoringPlanAccount + "-different"},
			failure:     "terraform_data.monitoring_safety[0]",
		},
		{
			name: "account_network_restricted", mode: "create",
			accountRule: map[string]any{"default_action": "Deny"}, failure: "terraform_data.monitoring_safety[0]",
		},
		{
			name: "protected_project_name", mode: "create",
			project: []any{changed(owned, "category", "AzureOpenAI")}, failure: "terraform_data.monitoring_safety[0]",
		},
		{
			name: "protected_account_name", mode: "create",
			account: []any{connection(name, component, "ApiKey", false)},
			failure: "terraform_data.monitoring_safety[0]",
		},
		{
			name: "protected_slot", mode: "create",
			slot:    connection(name, component, "ApiKey", false),
			failure: "terraform_data.monitoring_safety[0]",
		},
		{
			name: "restricted_account_sharing", mode: "create",
			account: []any{changed(connection("restricted", component, "ApiKey", false), "restricted", 1)},
			failure: "terraform_data.monitoring_safety[0]",
		},
		{
			name: "inherited_conflict", mode: "already-connected",
			project: []any{connection("external", component, "ApiKey", false)},
			account: []any{connection("shared", component+"-different", "AAD", true)},
			failure: "terraform_data.monitoring_safety[0]",
		},
		{
			name: "external_not_adopted", mode: "create",
			project: []any{connection("external", component, "ApiKey", false)}, failure: "terraform_data.monitoring_safety[0]",
		},
		{
			name: "existing_connection_missing", mode: "already-connected", failure: "terraform_data.monitoring_safety[0]",
		},
		{
			name: "changed_owned_auth", mode: "create", owned: true,
			project: []any{changed(owned, "auth", "AAD")}, failure: "terraform_data.monitoring_safety[0]",
		},
		{
			name: "changed_owned_environment", mode: "create", owned: true,
			project: []any{changed(owned, "environment", "other")}, failure: "terraform_data.monitoring_safety[0]",
		},
		{
			name: "existing_unowned_component", mode: "create", owned: true,
			component: map[string]any{"test_unowned": true}, failure: "terraform_data.monitoring_safety[0]",
		},
	}
	var result strings.Builder
	result.WriteString("mock_provider \"azapi\" { override_during = plan }\n")
	encode := func(value any) string {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		return string(encoded)
	}
	policy := func(id string, overrides map[string]any) map[string]any {
		value := map[string]any{
			"present": true, "resource_id": id, "local_auth": nil, "ingestion": nil,
			"public_access": nil, "default_action": nil, "workspace_local_auth": nil,
		}
		maps.Copy(value, overrides)
		return value
	}
	for _, tt := range cases {
		fmt.Fprintf(&result, "run %q {\n command = plan\n variables {\n monitoring_mode = %q\n"+
			" application_insights_resource_id = %q\n }\n", tt.name, tt.mode, component)
		if tt.mode != "none" {
			componentPolicy := policy(component, tt.component)
			maps.Copy(componentPolicy, map[string]any{"app_id": "test-app-id", "workspace_id": workspace})
			if tt.project == nil {
				tt.project = []any{}
			}
			if tt.account == nil {
				tt.account = []any{}
			}
			var rows any = tt.project
			if tt.missingRows {
				rows = nil
			}
			tags := map[string]any{}
			if tt.owned && tt.component["test_unowned"] != true {
				tags = ownedTags
			}
			values := map[string]any{
				"data.azapi_resource.monitoring_project[0]": map[string]any{
					"output": policy(monitoringPlanProject, nil),
				},
				"data.azapi_resource.monitoring_account[0]": map[string]any{
					"output": policy(monitoringPlanAccount, tt.accountRule),
				},
				"data.azapi_resource_list.monitoring_project_connections[0]": map[string]any{
					"output": map[string]any{"connections": rows},
				},
				"data.azapi_resource_list.monitoring_account_connections[0]": map[string]any{
					"output": map[string]any{"connections": tt.account},
				},
				"data.azapi_resource.monitoring_component[0]": map[string]any{
					"exists": tt.mode != "create" || tt.owned, "output": componentPolicy, "tags": tags,
				},
			}
			if tt.mode == "create" {
				values["data.azapi_resource.monitoring_owned_workspace[0]"] = map[string]any{
					"exists": tt.owned, "output": policy(workspace, tt.workspace), "tags": ownedTags,
				}
			} else {
				values["data.azapi_resource.monitoring_workspace[0]"] = map[string]any{
					"exists": true, "output": policy(workspace, tt.workspace),
				}
			}
			if tt.mode != "already-connected" {
				values["data.azapi_resource.monitoring_connection_slot[0]"] = map[string]any{
					"exists": tt.slot != nil, "output": tt.slot,
				}
			}
			for _, target := range slices.Sorted(maps.Keys(values)) {
				fmt.Fprintf(&result, " override_data {\n target = %s\n values = %s\n }\n", target, encode(values[target]))
			}
		}
		if tt.failure != "" {
			fmt.Fprintf(&result, " expect_failures = [%s]\n", tt.failure)
		} else {
			count := 1
			if tt.mode == "none" {
				count = 0
			}
			fmt.Fprintf(&result, " assert {\n condition = length(terraform_data.monitoring_safety) == %d\n"+
				" error_message = \"The monitoring guard count changed.\"\n }\n", count)
		}
		result.WriteString("}\n")
	}
	return result.String()
}
