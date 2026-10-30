// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// ResolveTerraformMonitoringExport selects a stable data-only or managed graph
// before export. Terraform independently rechecks policy and connections later.
func ResolveTerraformMonitoringExport(
	ctx context.Context, parameters map[string]any, projectID string, environment map[string]string,
	options *arm.ClientOptions,
) error {
	if parameters["monitoringMode"] == nil || parameters["monitoringMode"] == "none" {
		return nil
	}
	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID: environment["AZURE_TENANT_ID"], AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return MonitoringReadError("authenticate for monitoring export", err)
	}
	resolver := &MonitoringResolver{Credential: credential, ClientOptions: options}
	return resolver.ResolveTerraformExport(ctx, parameters, projectID, environment)
}

// ResolveTerraformExport preserves compatible external connections without
// importing them or changing managed resource counts based on live existence.
func (p *MonitoringResolver) ResolveTerraformExport(
	ctx context.Context, parameters map[string]any, projectID string, environment map[string]string,
) error {
	mode, ok := parameters["monitoringMode"].(string)
	if !ok || (mode != "create" && mode != "reuse-connect" && mode != "already-connected" && mode != "none") {
		return fmt.Errorf("invalid monitoringMode for Terraform export")
	}
	if mode == "none" {
		return nil
	}
	project, err := MonitoringResourceID(projectID, "Microsoft.CognitiveServices/accounts/projects")
	if err != nil {
		return fmt.Errorf("monitoring export requires the selected existing project's ARM resource ID: %w", err)
	}
	if saved := environment["AZURE_AI_PROJECT_ID"]; saved != "" && !strings.EqualFold(saved, project.String()) {
		return fmt.Errorf("monitoring export environment identifies a different Foundry project")
	}
	if mode == "create" && environment["AZD_FOUNDRY_MONITORING_MODE"] == "create" &&
		environment["AZURE_APPLICATION_INSIGHTS_RESOURCE_ID"] != "" {
		return fmt.Errorf("Terraform export cannot adopt monitoring previously created by microsoft.foundry; " +
			"keep Bicep/native infrastructure or explicitly reference the component as an existing resource")
	}
	rawComponentID, ok := parameters["applicationInsightsResourceId"].(string)
	if !ok {
		return fmt.Errorf("monitoring export requires a component resource ID string")
	}
	componentID, err := resolveVars(rawComponentID, environment)
	if err != nil {
		return fmt.Errorf("resolve monitoring destination for export: %w", err)
	}
	componentID = strings.TrimSpace(componentID)
	projectResource, found, err := p.Resource(ctx, project, "2025-04-01-preview")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("the selected existing Foundry project does not exist")
	}
	account, found, err := p.Resource(ctx, project.Parent, "2025-04-01-preview")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("the selected existing Foundry account does not exist")
	}
	connection, err := p.DiscoverConnection(ctx, project, true, true)
	if err != nil {
		return err
	}
	if connection != nil {
		if componentID != "" && !strings.EqualFold(componentID, connection.Target) {
			return fmt.Errorf("the selected component conflicts with the effective AppInsights connection")
		}
		if err := p.ValidateComponent(ctx, connection.Target, false); err != nil {
			return err
		}
		parameters["monitoringMode"] = "already-connected"
		if componentID == "" {
			parameters["applicationInsightsResourceId"] = connection.Target
		}
		return nil
	}
	if mode == "already-connected" {
		return fmt.Errorf("the declared existing AppInsights connection was not found")
	}
	for _, resource := range []*armresources.GenericResource{account, projectResource} {
		properties, err := MonitoringProperties(resource)
		if err != nil {
			return err
		}
		if err := MonitoringNetworkPolicy(properties); err != nil {
			return err
		}
	}
	if mode == "reuse-connect" {
		if componentID == "" {
			return fmt.Errorf("monitoring destination must resolve to a nonempty component resource ID")
		}
		return p.ValidateComponent(ctx, componentID, true)
	}
	return nil
}

// TerraformMonitoring contains the monitoring portion of an infrastructure export.
// Rendering is pure; discovery belongs at the export and provisioning boundaries.
type TerraformMonitoring struct {
	Files            map[string][]byte
	Variables        map[string]any
	CreatesResources bool
}

// RenderTerraformMonitoring renders first-class Terraform without reading Azure
// or resolving credentials. A disabled export retains the baseline tool requirements.
func RenderTerraformMonitoring(parameters map[string]any, existingProject bool) (TerraformMonitoring, error) {
	result := TerraformMonitoring{Files: map[string][]byte{}, Variables: map[string]any{}}
	mode, present := parameters["monitoringMode"]
	if !present || mode == "none" {
		return result, nil
	}
	modeText, ok := mode.(string)
	if !ok || (modeText != "create" && modeText != "reuse-connect" && modeText != "already-connected") {
		return result, fmt.Errorf("monitoringMode must be none, create, reuse-connect, or already-connected")
	}
	componentID, ok := parameters["applicationInsightsResourceId"].(string)
	if !ok || (modeText != "create" && strings.TrimSpace(componentID) == "") {
		return result, fmt.Errorf("monitoring requires a component resource ID unless monitoringMode is create")
	}
	result.CreatesResources = modeText == "create"
	result.Variables["monitoring_mode"] = modeText
	result.Variables["application_insights_resource_id"] = componentID

	values := struct {
		ProjectID, AccountID, GroupID string
		CreateSupported               bool
	}{
		ProjectID:       "azapi_resource.project.id",
		AccountID:       "azapi_resource.foundry_account.id",
		GroupID:         "azurerm_resource_group.this.id",
		CreateSupported: true,
	}
	if existingProject {
		values.ProjectID = "local.normalized_project_id"
		values.AccountID = "local.foundry_account_id"
		values.GroupID = `join("/", slice(split("/", local.normalized_project_id), 0, 5))`
		values.CreateSupported = result.CreatesResources
		if result.CreatesResources {
			values.GroupID = "azurerm_resource_group.adjunct.id"
			group, err := existingProjectTerraformTemplatesFS.ReadFile(
				"templates/terraform-existing-project/adjunct-resource-group.tf")
			if err != nil {
				return result, fmt.Errorf("read monitoring resource group: %w", err)
			}
			result.Files["adjunct-resource-group.tf"] = group
		}
	} else {
		provider, err := terraformTemplatesFS.ReadFile("templates/terraform/provider-monitoring.tf.tmpl")
		if err != nil {
			return result, fmt.Errorf("read monitoring provider requirements: %w", err)
		}
		result.Files["provider.tf"] = provider
	}
	source, err := terraformTemplatesFS.ReadFile("templates/terraform/monitoring.tf.tmpl")
	if err != nil {
		return result, fmt.Errorf("read monitoring Terraform: %w", err)
	}
	tmpl, err := template.New("monitoring.tf").Option("missingkey=error").Parse(string(source))
	if err != nil {
		return result, fmt.Errorf("parse monitoring Terraform: %w", err)
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, values); err != nil {
		return result, fmt.Errorf("render monitoring Terraform: %w", err)
	}
	result.Files["monitoring.tf"] = rendered.Bytes()
	return result, nil
}
