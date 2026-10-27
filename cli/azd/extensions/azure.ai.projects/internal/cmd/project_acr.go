// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/provisioning"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func validateProjectACRFlags(flags *projectAddFlags) error {
	if !flags.acrResourceIDSet {
		return nil
	}
	if strings.TrimSpace(flags.acrResourceID) == "" {
		return projectACRValidation("--acr-resource-id cannot be empty")
	}
	if !flags.newProject || flags.projectIDSet || flags.projectEndpointSet ||
		flags.projectID != "" || flags.projectEndpoint != "" || flags.forceSet {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--acr-resource-id requires --new-project and cannot be combined with "+
				"--project-id, --project-endpoint, or --force",
			"use --new-project for source-built agents with an external registry",
		)
	}
	if err := synthesis.ValidateACRSelector(flags.acrResourceID, "", ""); err != nil {
		return projectACRValidation(fmt.Sprintf("invalid --acr-resource-id: %s", err))
	}
	if flags.infra == provisioning.TerraformProviderName {
		return projectACRValidation("--acr-resource-id is not supported with --infra=terraform")
	}
	return nil
}

func projectACRValidation(message string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		message,
		"use an external registry ARM resource ID with --new-project and Bicep source builds; "+
			"registry migration is not supported",
	)
}

func readProjectACRBinding(
	ctx context.Context, client *azdext.AzdClient, envName string,
) (*synthesis.ACRBinding, error) {
	if client == nil || envName == "" {
		return nil, nil
	}
	response, err := client.Environment().GetConfig(ctx, &azdext.GetConfigRequest{
		EnvName: envName,
		Path:    synthesis.ACRBindingConfigKey,
	})
	if err != nil {
		return nil, fmt.Errorf("read deployed registry binding: %w", err)
	}
	if !response.GetFound() {
		return nil, nil
	}
	binding, err := synthesis.ParseACRBinding(response.GetValue())
	if err != nil {
		return nil, projectACRValidation(err.Error())
	}
	if binding == nil {
		return nil, projectACRValidation("deployed registry binding is empty")
	}
	return binding, nil
}

func (a *ProjectAddAction) preflightProjectACR(ctx context.Context, root string) error {
	a.flags.deployedBinding = nil
	a.flags.preserveProjectID = ""
	envName := a.environmentName()
	if envName == "" {
		current, err := a.client.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		if err == nil {
			envName = current.GetEnvironment().GetName()
		}
	}
	plannedEnvName := envName
	if plannedEnvName == "" {
		plannedEnvName = deriveProjectEnvironmentName(root)
	}
	values := map[string]string{}
	if envName != "" {
		var err error
		values, err = currentProjectEnvironment(ctx, a.client, envName)
		if err != nil && projectACREnvironmentMissing(root, envName, err) {
			envName = ""
			values = map[string]string{}
		} else if err != nil {
			return fmt.Errorf("read environment before registry selection: %w", err)
		}
	}
	if envName != "" {
		binding, err := readProjectACRBinding(ctx, a.client, envName)
		if err != nil {
			return err
		}
		if binding != nil {
			if a.flags.acrResourceIDSet {
				if err := synthesis.ValidateACRBinding(binding, "reuse-connect", a.flags.acrResourceID); err != nil {
					return projectACRValidation(err.Error())
				}
			} else if err := synthesis.ValidateACRBinding(binding,
				values["AZD_FOUNDRY_ACR_MODE"], values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]); err != nil {
				return projectACRValidation(err.Error())
			}
			a.flags.deployedBinding = binding
			a.flags.preserveProjectID = binding.ProjectResourceID
			if id := values["AZURE_AI_PROJECT_ID"]; id != "" && !strings.EqualFold(id, binding.ProjectResourceID) {
				return projectACRValidation(
					"persisted project identity differs from the deployed registry binding; use a new azd environment",
				)
			}
			if explicitProjectTarget(a.flags) {
				project, err := projectFromResourceID(binding.ProjectResourceID)
				if err != nil {
					return projectACRValidation("deployed registry binding has an invalid project resource ID")
				}
				if (a.flags.projectID != "" && !strings.EqualFold(a.flags.projectID, binding.ProjectResourceID)) ||
					(a.flags.projectEndpoint != "" && !equalProjectEndpoint(a.flags.projectEndpoint, project.Endpoint)) {
					return projectACRValidation(
						"cannot replace a project while its deployed registry binding is still in use",
					)
				}
			}
		}
		legacyRegistry := values["AZURE_AI_PROJECT_ID"] != "" &&
			values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] != "" &&
			(values["AZURE_CONTAINER_REGISTRY_ENDPOINT"] != "" ||
				values["AZURE_AI_PROJECT_ACR_CONNECTION_NAME"] != "" ||
				values["AZD_FOUNDRY_ACR_PULL_ASSIGNED"] != "")
		if binding == nil && a.flags.acrResourceIDSet && legacyRegistry &&
			(values["AZD_FOUNDRY_ACR_MODE"] == "create" ||
				!strings.EqualFold(values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"], a.flags.acrResourceID)) {
			return projectACRValidation("cannot change the registry of a provisioned Foundry project")
		}
		if binding == nil && legacyRegistry && !strings.EqualFold(values["USE_EXISTING_AI_PROJECT"], "true") {
			a.flags.preserveProjectID = values["AZURE_AI_PROJECT_ID"]
			if explicitProjectTarget(a.flags) {
				project, err := projectFromResourceID(a.flags.preserveProjectID)
				if err != nil {
					return projectACRValidation("existing registry outputs have an invalid project resource ID")
				}
				if (a.flags.projectID != "" && !strings.EqualFold(a.flags.projectID, a.flags.preserveProjectID)) ||
					(a.flags.projectEndpoint != "" && !equalProjectEndpoint(a.flags.projectEndpoint, project.Endpoint)) {
					return projectACRValidation(
						"cannot replace a project while its deployed registry outputs are still in use",
					)
				}
			}
		}
	}
	greenfield := a.flags.newProject ||
		(!explicitProjectTarget(a.flags) && !strings.EqualFold(values["USE_EXISTING_AI_PROJECT"], "true"))
	if greenfield && a.flags.infra == provisioning.TerraformProviderName &&
		(values["AZD_FOUNDRY_ACR_MODE"] == "reuse-connect" ||
			(a.flags.newProject && values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] != "" &&
				values["AZD_FOUNDRY_ACR_MODE"] != "create")) {
		return projectACRValidation("external registry reuse is not supported with --infra=terraform")
	}
	if !a.flags.acrResourceIDSet {
		return nil
	}
	raw, err := readProjectACRManifest(root)
	if err != nil {
		return err
	}
	project := &resolvedProject{
		Mode: projectModeNew, ACRResourceID: a.flags.acrResourceID,
		PreserveDeployed: a.flags.preserveProjectID != "",
	}
	plan := planProjectEnvironment(values, projectModeNew, project, projectIdentityChanged(values, "", project))
	effective := applyProjectEnvironmentPlan(values, plan)
	effective["AZURE_ENV_NAME"] = plannedEnvName
	if err := synthesis.ValidateGreenfieldACRSelectionForInput(synthesis.Input{
		RawAzureYAML: raw, ProjectRoot: root, Env: effective,
	}, a.flags.acrResourceID); err != nil {
		return projectACRValidation(err.Error())
	}
	if len(raw) == 0 {
		return nil
	}
	return validateProjectACRManifest(raw, root, effective)
}

func projectACREnvironmentMissing(root, name string, err error) bool {
	if status.Code(err) == codes.NotFound {
		return true
	}
	if status.Code(err) != codes.Unknown || filepath.Base(name) != name ||
		!strings.Contains(status.Convert(err).Message(), "environment not found") {
		return false
	}
	_, statErr := os.Stat(filepath.Join(root, ".azure", name))
	return os.IsNotExist(statErr)
}

func readProjectACRManifest(root string) ([]byte, error) {
	exists, err := projectFileExists(root)
	if err != nil || !exists {
		return nil, err
	}
	file, err := projectFilePath(root)
	if err != nil {
		return nil, err
	}
	// #nosec G304
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read project before registry selection: %w", err)
	}
	return raw, nil
}

func validateProjectACRInfrastructure(raw []byte, root string) error {
	var manifest struct {
		Infra struct {
			Provider string `yaml:"provider"`
			Layers   []struct {
				Provider string `yaml:"provider"`
				Path     string `yaml:"path"`
				Module   string `yaml:"module"`
			} `yaml:"layers"`
		} `yaml:"infra"`
	}
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		return projectACRValidation(fmt.Sprintf("invalid azure.yaml: %s", err))
	}
	if manifest.Infra.Provider == provisioning.TerraformProviderName && len(manifest.Infra.Layers) == 0 {
		return projectACRValidation("--acr-resource-id is not supported with Terraform infrastructure")
	}
	foundryLayer := false
	terraformLayer := false
	for _, layer := range manifest.Infra.Layers {
		provider := layer.Provider
		if provider == "" {
			provider = manifest.Infra.Provider
		}
		if provider == provisioning.FoundryProviderName {
			foundryLayer = true
		}
		if provider == provisioning.TerraformProviderName {
			terraformLayer = true
			if isFoundryTerraformLayer(root, layer.Path, layer.Module) {
				return projectACRValidation("--acr-resource-id is not supported with Terraform infrastructure")
			}
		}
	}
	if terraformLayer && !foundryLayer {
		return projectACRValidation("--acr-resource-id requires a Bicep Foundry infrastructure layer")
	}
	return nil
}

func validateProjectACRManifest(raw []byte, root string, values map[string]string) error {
	if err := validateProjectACRInfrastructure(raw, root); err != nil {
		return err
	}
	hasAgents, needsACR, err := synthesis.ACRRequirements(synthesis.Input{
		RawAzureYAML: raw,
		ProjectRoot:  root,
		Env:          values,
	})
	if err != nil {
		return projectACRValidation(fmt.Sprintf("cannot select a registry: %s", err))
	}
	if hasAgents && !needsACR {
		return projectACRValidation(
			"--acr-resource-id cannot be used with code, prebuilt-image, or non-container agents without source builds",
		)
	}
	return nil
}
