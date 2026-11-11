// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore tftest bicepconfig

package synthesis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"
)

func TestBicepExistingProjectIdentityGate(t *testing.T) {
	bicep := lookupBicep(t)
	for _, tt := range []struct {
		AcrMode         string
		AcrPullAssigned bool
	}{
		{AcrMode: "none"},
		{AcrMode: "create"},
		{AcrMode: "reuse-connect"},
		{AcrMode: "reuse-connect", AcrPullAssigned: true},
		{AcrMode: "already-connected"},
	} {
		t.Run(fmt.Sprintf("%s/pull-assigned-%t", tt.AcrMode, tt.AcrPullAssigned), func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(dir, "modules"), 0o750))
			entries, err := fs.ReadDir(templatesFS, "templates/modules")
			require.NoError(t, err)
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".bicep") {
					content, err := templatesFS.ReadFile("templates/modules/" + entry.Name())
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(filepath.Join(dir, "modules", entry.Name()), content, 0o600))
				}
			}
			for source, target := range map[string]string{
				"templates/existing-project-eject.bicep.tmpl": "main.bicep",
				"templates/modules/container-registry-eject.bicep.tmpl": filepath.Join(
					"modules", "container-registry.bicep",
				),
			} {
				content, err := templatesFS.ReadFile(source)
				require.NoError(t, err)
				parsed, err := template.New("bicep").Parse(string(content))
				require.NoError(t, err)
				var rendered bytes.Buffer
				require.NoError(t, parsed.Execute(&rendered, tt))
				require.NoError(t, os.WriteFile(filepath.Join(dir, target), rendered.Bytes(), 0o600))
			}
			//nolint:gosec // The validated local Bicep compiler.
			command := exec.CommandContext(t.Context(), bicep, "build", filepath.Join(dir, "main.bicep"), "--stdout")
			output, err := command.Output()
			require.NoError(t, err)
			type resource struct {
				Existing   bool
				Type       string
				DependsOn  []string
				Properties struct {
					Template json.RawMessage
				}
			}
			var compiled struct{ Resources map[string]resource }
			require.NoError(t, json.Unmarshal(output, &compiled))
			const gateName = "projectIdentityValidation"
			require.Contains(t, compiled.Resources, gateName)
			gate := compiled.Resources[gateName]
			require.Equal(t, "Microsoft.Resources/deployments", gate.Type)
			require.Empty(t, gate.DependsOn)
			var validation struct {
				Resources map[string]struct{ Existing bool }
				Outputs   map[string]struct{ Value string }
			}
			require.NoError(t, json.Unmarshal(gate.Properties.Template, &validation))
			require.NotEmpty(t, validation.Resources)
			for name, resource := range validation.Resources {
				require.True(t, resource.Existing, "identity validation must not change resource %s", name)
			}
			require.Contains(t, validation.Outputs["valid"].Value, "fail(")
			for name, resource := range compiled.Resources {
				if !resource.Existing && name != gateName {
					require.Contains(t, resource.DependsOn, gateName,
						"every resource-changing path must wait for successful identity validation: %s", name)
				}
			}
		})
	}
}

func TestBicepExistingProjectEndpointIdentity(t *testing.T) {
	bicep := lookupBicep(t)
	const endpoint = "https://custom-dns.services.ai.azure.com/api/projects/project"
	cases := []struct {
		name, endpoint, subdomain, advertised string
		valid                                 bool
	}{
		{name: "custom_domain", endpoint: endpoint, subdomain: "custom-dns", advertised: endpoint, valid: true},
		{name: "wrong_account", endpoint: strings.Replace(endpoint, "custom-dns", "another", 1),
			subdomain: "custom-dns", advertised: endpoint},
		{name: "wrong_project", endpoint: endpoint + "-other", subdomain: "custom-dns", advertised: endpoint},
		{name: "missing_project_metadata", endpoint: endpoint, subdomain: "custom-dns", valid: true},
		{name: "missing_account_metadata", endpoint: endpoint, advertised: endpoint, valid: true},
		{name: "legacy_fallback", endpoint: strings.Replace(endpoint, "custom-dns", "resource-account", 1), valid: true},
		{name: "normalized_endpoint", endpoint: " HTTPS://CUSTOM-DNS.services.ai.azure.com/projects/%70roject/// ",
			subdomain: "custom-dns", advertised: endpoint, valid: true},
		{name: "encoded_origin", endpoint: strings.Replace(endpoint, "https://", "https%3A%2F%2F", 1),
			subdomain: "custom-dns", advertised: endpoint},
		{name: "encoded_hostname", endpoint: strings.Replace(endpoint, "-", "%2d", 1),
			subdomain: "custom-dns", advertised: endpoint},
		{name: "credentials", endpoint: strings.Replace(endpoint, "https://", "https://user:password@", 1),
			subdomain: "custom-dns", advertised: endpoint},
		{name: "query", endpoint: endpoint + "?sig=test-token", subdomain: "custom-dns", advertised: endpoint},
		{name: "invalid_subdomain", endpoint: endpoint, subdomain: "invalid.example.com", advertised: endpoint},
		{name: "invalid_advertised_endpoint", endpoint: endpoint, advertised: "https://example.com/api/projects/project"},
		{name: "conflicting_metadata", endpoint: endpoint, subdomain: "custom-dns",
			advertised: strings.Replace(endpoint, "custom-dns", "another", 1)},
	}
	dir := t.TempDir()
	module, err := templatesFS.ReadFile("templates/modules/foundry-project-identity.bicep")
	require.NoError(t, err)
	var tests strings.Builder
	for _, tt := range cases {
		fmt.Fprintf(&tests, "test %s 'case.bicep' = {\n  params: {\n"+
			"    endpoint: '%s'\n    subdomain: '%s'\n    advertised: '%s'\n    expected: %t\n  }\n}\n",
			tt.name, tt.endpoint, tt.subdomain, tt.advertised, tt.valid)
	}
	for name, content := range map[string][]byte{
		"identity.bicep": module, "tests.bicep": []byte(tests.String()),
		"bicepconfig.json": []byte(`{"experimentalFeaturesEnabled":{"testFramework":true,"assertions":true}}`),
		"case.bicep": []byte(`
import { projectEndpointIdentityMatches } from './identity.bicep'
param endpoint string
param subdomain string
param advertised string
param expected bool
assert matches = projectEndpointIdentityMatches(endpoint, 'resource-account', 'project', subdomain, advertised) == expected
`),
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), content, 0o600))
	}
	//nolint:gosec // The validated local Bicep compiler.
	command := exec.CommandContext(t.Context(), bicep, "test", filepath.Join(dir, "tests.bicep"), "--no-restore")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.Contains(t, string(output), fmt.Sprintf("All %d evaluations passed!", len(cases)))
	t.Logf("%s", output)
}

func TestExistingProjectOpenAIOutputUsesARMSubdomain(t *testing.T) {
	raw, err := ExistingProjectARMTemplate()
	require.NoError(t, err)
	var compiled struct {
		Outputs map[string]struct{ Value string }
	}
	require.NoError(t, json.Unmarshal(raw, &compiled))
	expression := compiled.Outputs["AZURE_OPENAI_ENDPOINT"].Value
	require.Contains(t, expression, "reference('foundryAccount').customSubDomainName")
	require.Contains(t, expression, "parameters('projectEndpoint')")
	require.NotContains(t, expression, "variables('accountName')")
	for _, path := range []string{
		"templates/existing-project.bicep",
		"templates/existing-project-eject.bicep.tmpl",
	} {
		source, err := templatesFS.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(source), "foundryAccount.properties.?customSubDomainName")
		require.Contains(t, string(source), "'https://${accountSubdomain}.openai.azure.com/'")
	}
}

func TestTerraformExistingProjectEndpointPlans(t *testing.T) {
	terraform := os.Getenv("AZD_TEST_TERRAFORM_PATH")
	if terraform == "" {
		t.Skip("set AZD_TEST_TERRAFORM_PATH to opt in to Terraform/provider integration tests")
	}
	main, err := existingProjectTerraformTemplatesFS.ReadFile("templates/terraform-existing-project/main.tf")
	require.NoError(t, err)
	outputTemplate, err := existingProjectTerraformTemplatesFS.ReadFile(
		"templates/terraform-existing-project/outputs.tf.tmpl")
	require.NoError(t, err)
	parsed, err := template.New("outputs").Parse(string(outputTemplate))
	require.NoError(t, err)
	var outputs bytes.Buffer
	require.NoError(t, parsed.Execute(&outputs, map[string]any{"IncludeAdjunct": false, "AcrMode": "none"}))
	dir := t.TempDir()
	for name, content := range map[string][]byte{
		"main.tf": main, "outputs.tf": outputs.Bytes(),
		"providers.tf": []byte(endpointPlanTestRoot), "endpoints.tftest.hcl": []byte(endpointPlanTestCases),
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), content, 0o600))
	}
	run := func(arguments ...string) {
		t.Helper()
		//nolint:gosec // The opt-in selects a Terraform executable.
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
	run("validate", "-no-color")
	run("test", "-no-color")
}

const endpointPlanProjectID = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/" +
	"providers/Microsoft.CognitiveServices/accounts/resource-account/projects/project"

const endpointPlanTestRoot = `
terraform {
  required_providers {
    azapi = { source = "Azure/azapi", version = "~> 2.7" }
  }
}
variable "project_resource_id" {
  default = "` + endpointPlanProjectID + `"
}
variable "project_endpoint" {
  default = "https://custom-dns.services.ai.azure.com/api/projects/project"
}
`

const endpointPlanTestCases = `
mock_provider "azapi" {}
override_data {
  target = data.azapi_resource.foundry_account
  values = { output = { properties = { customSubDomainName = "custom-dns" } } }
}
override_data {
  target = data.azapi_resource.project
  values = { output = { properties = {
    endpoints = { "AI Foundry API" = "https://custom-dns.services.ai.azure.com/api/projects/project" }
  } } }
}
run "custom_subdomain" {
  command = plan
  assert {
    condition = output.AZURE_OPENAI_ENDPOINT == "https://custom-dns.openai.azure.com/"
    error_message = "OpenAI must use the account custom subdomain."
  }
  assert {
    condition = output.FOUNDRY_PROJECT_ENDPOINT == var.project_endpoint
    error_message = "The ARM-advertised project endpoint must be preserved."
  }
}
run "different_account_same_project" {
  command = plan
  variables { project_endpoint = "https://other-account.services.ai.azure.com/api/projects/project" }
  expect_failures = [output.FOUNDRY_PROJECT_ENDPOINT]
}
run "missing_project_endpoint" {
  command = plan
  override_data {
    target = data.azapi_resource.project
    values = { output = { properties = {} } }
  }
  assert {
    condition = output.AZURE_OPENAI_ENDPOINT == "https://custom-dns.openai.azure.com/"
    error_message = "Missing project metadata must use the account subdomain."
  }
}
run "missing_account_subdomain" {
  command = plan
  override_data {
    target = data.azapi_resource.foundry_account
    values = { output = { properties = {} } }
  }
  assert {
    condition = output.AZURE_OPENAI_ENDPOINT == "https://custom-dns.openai.azure.com/"
    error_message = "The advertised project endpoint supplies the fallback subdomain."
  }
}
run "missing_all_endpoint_metadata" {
  command = plan
  variables { project_endpoint = "https://resource-account.services.ai.azure.com/api/projects/project" }
  override_data {
    target = data.azapi_resource.foundry_account
    values = { output = { properties = {} } }
  }
  override_data {
    target = data.azapi_resource.project
    values = { output = { properties = {} } }
  }
  assert {
    condition = output.AZURE_OPENAI_ENDPOINT == "https://resource-account.openai.azure.com/"
    error_message = "The legacy account-name fallback must remain compatible."
  }
}
run "different_project" {
  command = plan
  variables { project_endpoint = "https://custom-dns.services.ai.azure.com/api/projects/other" }
  expect_failures = [output.FOUNDRY_PROJECT_ENDPOINT]
}
run "credential_bearing_endpoint" {
  command = plan
  variables { project_endpoint = "https://user:password@custom-dns.services.ai.azure.com/api/projects/project" }
  expect_failures = [output.FOUNDRY_PROJECT_ENDPOINT]
}
run "invalid_account_metadata" {
  command = plan
  override_data {
    target = data.azapi_resource.foundry_account
    values = { output = { properties = { customSubDomainName = "invalid.example.com" } } }
  }
  expect_failures = [output.FOUNDRY_PROJECT_ENDPOINT]
}
run "mismatched_advertised_and_account_subdomain" {
  command = plan
  variables { project_endpoint = "https://other-dns.services.ai.azure.com/api/projects/project" }
  override_data {
    target = data.azapi_resource.project
    values = { output = { properties = {
      endpoints = { "AI Foundry API" = "https://other-dns.services.ai.azure.com/api/projects/project" }
    } } }
  }
  expect_failures = [output.FOUNDRY_PROJECT_ENDPOINT]
}
`
