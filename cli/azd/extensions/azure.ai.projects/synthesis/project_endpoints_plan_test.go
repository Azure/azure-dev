// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore tftest

package synthesis

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"
)

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
`
