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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func projectACRValidation(message string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		message,
		"use an external registry in the project's subscription, outside its resource group, "+
			"with --new-project and Bicep source builds; registry migration is not supported",
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
	if !greenfield || (!a.flags.acrResourceIDSet && values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] == "") {
		return nil
	}
	raw, err := readProjectACRManifest(root)
	if err != nil {
		return err
	}
	project := &resolvedProject{
		Mode:             projectModeNew,
		PreserveDeployed: a.flags.preserveProjectID != "",
	}
	if a.flags.acrResourceIDSet {
		project.ACRResourceID = a.flags.acrResourceID
	}
	selection, err := projectACRSelection(raw, root, plannedEnvName, values, project, a.flags.deployedBinding)
	if err != nil {
		return err
	}
	if selection != "" && a.flags.infra == provisioning.TerraformProviderName {
		return projectACRValidation("external registry reuse is not supported with --infra=terraform")
	}
	return nil
}

func projectACRSelection(
	raw []byte, root, envName string, values map[string]string, project *resolvedProject, binding *synthesis.ACRBinding,
) (string, error) {
	plannedProject := *project
	if plannedProject.ACRResourceID == "" && !strings.EqualFold(values["USE_EXISTING_AI_PROJECT"], "true") {
		mode, err := synthesis.GreenfieldACRMode(synthesis.Input{
			RawAzureYAML: raw, ProjectRoot: root, Env: values, ACRBinding: binding,
		})
		if err != nil {
			return "", projectACRValidation(err.Error())
		}
		if mode == "reuse-connect" {
			plannedProject.ACRResourceID = values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]
		}
	}
	plan := planProjectEnvironment(values, projectModeNew, &plannedProject,
		projectIdentityChanged(values, "", &plannedProject))
	effective := applyProjectEnvironmentPlan(values, plan)
	effective["AZURE_ENV_NAME"] = envName
	input := synthesis.Input{RawAzureYAML: raw, ProjectRoot: root, Env: effective, ACRBinding: binding}
	mode, err := synthesis.GreenfieldACRMode(input)
	if err != nil {
		return "", projectACRValidation(err.Error())
	}
	if mode != "reuse-connect" {
		return "", nil
	}
	id := effective["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]
	if err := synthesis.ValidateGreenfieldACRSelectionForInput(input, id); err != nil {
		return "", projectACRValidation(err.Error())
	}
	return id, nil
}

func projectACREnvironmentMissing(root, name string, err error) bool {
	if status.Code(err) == codes.NotFound {
		return true
	}
	if status.Code(err) != codes.Unknown || filepath.Base(name) != name {
		return false
	}
	message := status.Convert(err).Message()
	if strings.HasPrefix(message, "no project exists;") {
		exists, inspectErr := projectFileExists(root)
		if inspectErr != nil || exists {
			return false
		}
	} else if !strings.Contains(message, "environment not found") {
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
