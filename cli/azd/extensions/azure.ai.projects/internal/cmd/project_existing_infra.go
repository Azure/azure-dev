// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/provisioning"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
)

type projectEjectAcrMode string

const (
	projectEjectAcrNone             projectEjectAcrMode = "none"
	projectEjectAcrCreate           projectEjectAcrMode = "create"
	projectEjectAcrReuseConnect     projectEjectAcrMode = "reuse-connect"
	projectEjectAcrAlreadyConnected projectEjectAcrMode = "already-connected"
)

// ejectProjectInfraWithTarget preserves the target selected by init.
// The endpoint is needed when the daemon persists the service after
// the local azure.yaml has been read.
func ejectProjectInfraWithTarget(
	ctx context.Context,
	client *azdext.AzdClient,
	projectRoot, serviceName, provider, endpoint, resourceID string,
	environments ...map[string]string,
) error {
	projectFile, err := projectFilePath(projectRoot)
	if err != nil {
		return err
	}
	// #nosec G304
	raw, err := os.ReadFile(projectFile)
	if err != nil {
		return fmt.Errorf("read %s for infrastructure ejection: %w", projectFile, err)
	}

	configuredEndpoint, err := synthesis.ProjectEndpoint(
		raw, serviceName, projectRoot,
	)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("read endpoint for foundry project service %q: %s",
				serviceName, err),
			"check the endpoint field under your azure.ai.project service",
		)
	}
	if configuredEndpoint == "" && strings.TrimSpace(endpoint) == "" {
		return ejectProjectInfra(ctx, client, projectRoot, serviceName, provider)
	}
	if strings.TrimSpace(endpoint) == "" {
		endpoint = configuredEndpoint
	}
	if configuredEndpoint == "" {
		raw, err = projectEjectSetServiceEndpoint(raw, serviceName, endpoint)
		if err != nil {
			return err
		}
	}

	return ejectExistingProjectInfra(
		ctx, client, projectRoot, serviceName, provider, raw, endpoint,
		resourceID, environments...,
	)
}

func ejectExistingProjectInfra(
	ctx context.Context,
	client *azdext.AzdClient,
	projectRoot, serviceName, provider string,
	raw []byte,
	endpoint, resourceID string,
	environments ...map[string]string,
) error {
	if provider != provisioning.BicepProviderName &&
		provider != provisioning.TerraformProviderName {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unsupported infrastructure provider %q", provider),
			"pass --infra=bicep or --infra=terraform",
		)
	}

	projectResponse, err := client.Project().Get(
		ctx, &azdext.EmptyRequest{},
	)
	if err != nil {
		return fmt.Errorf(
			"read project configuration before infrastructure ejection: %w",
			err,
		)
	}
	project := projectResponse.GetProject()
	declaration, err := readProjectInfraDeclaration(project)
	if err != nil {
		return err
	}
	target, err := resolveProjectInfraEjectTarget(projectRoot, declaration)
	if err != nil {
		return err
	}
	targetDir := filepath.Join(
		projectRoot, filepath.FromSlash(target.path),
	)
	if err := validateProjectEjectTarget(targetDir); err != nil {
		return err
	}

	environment := map[string]string{}
	if len(environments) > 0 && environments[0] != nil {
		maps.Copy(environment, environments[0])
	}
	if strings.TrimSpace(endpoint) != "" {
		environment["FOUNDRY_PROJECT_ENDPOINT"] = endpoint
	}
	if strings.TrimSpace(resourceID) != "" {
		environment["AZURE_AI_PROJECT_ID"] = resourceID
	}
	identity, err := projectEjectIdentity(endpoint, resourceID, environment)
	if err != nil {
		return err
	}
	if err := validateProjectEjectEnvironment(
		endpoint, identity, environment,
	); err != nil {
		return err
	}

	result, err := synthesis.SynthesizeExistingProject(synthesis.Input{
		RawAzureYAML:    raw,
		ServiceName:     serviceName,
		AcceptedHosts:   provisioning.FoundryProvisioningServiceHosts,
		Env:             environment,
		ProjectRoot:     projectRoot,
		PreserveVarRefs: true,
	})
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("synthesize Foundry project service %q: %s",
				serviceName, err),
			"check the endpoint, deployments, and connections fields "+
				"under your azure.ai.project service",
		)
	}

	acrMode, err := resolveProjectEjectAcrMode(
		result.Parameters, environment,
	)
	if err != nil {
		return err
	}
	if provider == provisioning.TerraformProviderName &&
		acrMode == projectEjectAcrCreate &&
		projectEjectHasAcrState(environment) {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"Terraform eject cannot adopt the container registry previously "+
				"created by microsoft.foundry",
			"run `azd down` before ejecting Terraform, or keep Bicep/"+
				"microsoft.foundry for the existing resources",
		)
	}

	stageDir, err := os.MkdirTemp(projectRoot, ".azd-foundry-eject-*")
	if err != nil {
		return exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("create infrastructure staging directory: %s", err),
		)
	}
	defer os.RemoveAll(stageDir)

	if provider == provisioning.TerraformProviderName {
		if err := writeExistingProjectTerraform(
			stageDir, target.module, result.Parameters, acrMode, environment,
		); err != nil {
			return err
		}
	} else if err := writeExistingProjectBicep(
		stageDir, target.module, result.Parameters, acrMode, environment,
	); err != nil {
		return err
	}

	restoreFiles, err := installProjectEjectStage(stageDir, targetDir)
	if err != nil {
		return err
	}
	oldProvider, oldPath := projectInfraConfig(project)
	layersChanged := target.layer &&
		provider == provisioning.TerraformProviderName
	rollback := func(operationErr error) error {
		var rollbackErrs []error
		if restoreErr := restoreFiles(); restoreErr != nil {
			rollbackErrs = append(
				rollbackErrs,
				fmt.Errorf("restore generated infrastructure: %w", restoreErr),
			)
		}
		var restoreErr error
		if layersChanged {
			restoreErr = restoreProjectInfraLayers(ctx, client, declaration.layers)
		} else if !target.layer {
			restoreErr = restoreProjectInfraConfig(
				ctx, client, oldProvider, oldPath,
			)
		}
		if restoreErr != nil {
			rollbackErrs = append(
				rollbackErrs,
				fmt.Errorf("restore project infrastructure config: %w",
					restoreErr),
			)
		}
		return errors.Join(append([]error{operationErr}, rollbackErrs...)...)
	}

	if layersChanged {
		updatedLayers, updateErr := updateFoundryLayerProvider(
			declaration, provisioning.TerraformProviderName,
		)
		if updateErr != nil {
			return rollback(updateErr)
		}
		if err := setProjectConfigValue(
			ctx, client, "infra.layers", mapsToValues(updatedLayers),
		); err != nil {
			return rollback(fmt.Errorf(
				"stamp Terraform layer provider: %w", err,
			))
		}
	} else if !target.layer {
		if provider == provisioning.TerraformProviderName {
			if err := setProjectConfigString(
				ctx, client, "infra.provider",
				provisioning.TerraformProviderName,
			); err != nil {
				return rollback(fmt.Errorf(
					"stamp Terraform provider: %w", err,
				))
			}
		}
		if err := unsetProjectConfigValue(ctx, client, "infra.path"); err != nil {
			return rollback(fmt.Errorf(
				"remove infra.path: %w", err,
			))
		}
	}

	return nil
}

func validateProjectEjectTarget(targetDir string) error {
	info, err := os.Lstat(targetDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect infrastructure destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return exterrors.Validation(
			"infra_eject_exists",
			fmt.Sprintf("infrastructure destination %q is a symbolic link",
				filepath.ToSlash(targetDir)),
			"remove the symbolic link or choose another infrastructure path",
		)
	}
	if !info.IsDir() {
		return exterrors.Validation(
			"infra_eject_exists",
			fmt.Sprintf("infrastructure destination %q is not a directory",
				filepath.ToSlash(targetDir)),
			"remove the conflicting path or choose another infrastructure path",
		)
	}
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return fmt.Errorf("inspect infrastructure destination: %w", err)
	}
	if len(entries) != 0 {
		return exterrors.Validation(
			"infra_eject_exists",
			fmt.Sprintf("infrastructure destination %q already contains files",
				filepath.ToSlash(targetDir)),
			"remove or rename the existing infrastructure directory and retry",
		)
	}
	return nil
}

func installProjectEjectStage(
	stageDir, targetDir string,
) (func() error, error) {
	parent := filepath.Dir(targetDir)
	var createdParents []string
	for current := parent; ; current = filepath.Dir(current) {
		if _, err := os.Lstat(current); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return func() error { return nil }, fmt.Errorf(
				"inspect infrastructure parent directory: %w", err,
			)
		}
		createdParents = append(createdParents, current)
		if filepath.Dir(current) == current {
			break
		}
	}
	// #nosec G301
	if err := os.MkdirAll(parent, 0755); err != nil {
		return func() error { return nil }, fmt.Errorf(
			"create infrastructure parent directory: %w", err,
		)
	}

	emptyTarget := false
	if info, err := os.Lstat(targetDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return func() error { return nil }, exterrors.Validation(
				"infra_eject_exists",
				fmt.Sprintf("infrastructure destination %q already exists",
					filepath.ToSlash(targetDir)),
				"remove the conflicting path and retry",
			)
		}
		entries, readErr := os.ReadDir(targetDir)
		if readErr != nil {
			return func() error { return nil }, fmt.Errorf(
				"inspect infrastructure destination: %w", readErr,
			)
		}
		if len(entries) != 0 {
			return func() error { return nil }, exterrors.Validation(
				"infra_eject_exists",
				fmt.Sprintf("infrastructure destination %q already contains files",
					filepath.ToSlash(targetDir)),
				"remove or rename the existing infrastructure directory and retry",
			)
		}
		if err := os.Remove(targetDir); err != nil {
			return func() error { return nil }, fmt.Errorf(
				"prepare infrastructure destination: %w", err,
			)
		}
		emptyTarget = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return func() error { return nil }, fmt.Errorf(
			"inspect infrastructure destination: %w", err,
		)
	}

	if err := os.Rename(stageDir, targetDir); err != nil {
		var restoreErr error
		if emptyTarget {
			// #nosec G301
			restoreErr = os.Mkdir(targetDir, 0755)
		}
		for index := len(createdParents) - 1; index >= 0; index-- {
			if removeErr := os.Remove(createdParents[index]); removeErr != nil &&
				!errors.Is(removeErr, fs.ErrNotExist) {
				restoreErr = errors.Join(restoreErr, removeErr)
			}
		}
		if restoreErr != nil {
			return func() error { return nil }, errors.Join(
				fmt.Errorf("install infrastructure directory: %w", err),
				fmt.Errorf("restore infrastructure destination: %w",
					restoreErr),
			)
		}
		return func() error { return nil }, fmt.Errorf(
			"install infrastructure directory: %w", err,
		)
	}
	return func() error {
		var restoreErrs []error
		if err := os.RemoveAll(targetDir); err != nil {
			restoreErrs = append(restoreErrs, err)
		}
		if emptyTarget {
			// #nosec G301
			if err := os.Mkdir(targetDir, 0755); err != nil {
				restoreErrs = append(restoreErrs, err)
			}
		}
		for index := len(createdParents) - 1; index >= 0; index-- {
			if err := os.Remove(createdParents[index]); err != nil &&
				!errors.Is(err, fs.ErrNotExist) {
				restoreErrs = append(restoreErrs, err)
			}
		}
		return errors.Join(restoreErrs...)
	}, nil
}

func projectEjectIdentity(
	endpoint, resourceID string, values map[string]string,
) (*resolvedProject, error) {
	if strings.TrimSpace(resourceID) == "" {
		resourceID = strings.TrimSpace(values["AZURE_AI_PROJECT_ID"])
	}
	if resourceID == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeInfraEjectRequiresProjectID,
			"infrastructure ejection requires a verified Foundry project "+
				"resource ID",
			"rerun `azd ai project add --project-id <resource-id> --infra`",
		)
	}
	project, err := projectFromResourceID(resourceID)
	if err != nil {
		return nil, err
	}
	if endpoint != "" && !equalProjectEndpoint(endpoint, project.Endpoint) {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"the Foundry project endpoint does not match the project resource ID",
			"rerun `project add` against the same existing project",
		)
	}
	return project, nil
}

func validateProjectEjectEnvironment(
	endpoint string, identity *resolvedProject, values map[string]string,
) error {
	if configured := strings.TrimSpace(values["FOUNDRY_PROJECT_ENDPOINT"]); configured != "" && !equalProjectEndpoint(configured, endpoint) {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"FOUNDRY_PROJECT_ENDPOINT does not match the existing project "+
				"configured in azure.yaml",
			"rerun `project add` against the configured project",
		)
	}
	if configuredID := strings.TrimSpace(values["AZURE_AI_PROJECT_ID"]); configuredID != "" && !strings.EqualFold(
		configuredID, identity.ResourceId,
	) {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"AZURE_AI_PROJECT_ID does not match the existing project "+
				"configured in azure.yaml",
			"rerun `project add` against the configured project",
		)
	}
	return nil
}

func resolveProjectEjectAcrMode(
	params map[string]any, values map[string]string,
) (projectEjectAcrMode, error) {
	includeAcr, _ := params["includeAcr"].(bool)
	if !includeAcr ||
		strings.EqualFold(strings.TrimSpace(values["AZD_AGENT_SKIP_ACR"]), "true") {
		return projectEjectAcrNone, nil
	}

	mode := projectEjectAcrMode(strings.TrimSpace(
		values["AZD_FOUNDRY_ACR_MODE"],
	))
	switch mode {
	case "":
	case projectEjectAcrNone, projectEjectAcrCreate,
		projectEjectAcrReuseConnect, projectEjectAcrAlreadyConnected:
	default:
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("AZD_FOUNDRY_ACR_MODE has unsupported value %q",
				mode),
			"select a supported container registry mode and retry",
		)
	}

	endpoint := strings.TrimSpace(
		values["AZURE_CONTAINER_REGISTRY_ENDPOINT"],
	)
	resourceID := strings.TrimSpace(
		values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"],
	)
	connection := strings.TrimSpace(
		values["AZURE_AI_PROJECT_ACR_CONNECTION_NAME"],
	)
	if mode == projectEjectAcrReuseConnect ||
		mode == projectEjectAcrAlreadyConnected {
		if endpoint == "" || resourceID == "" {
			return "", exterrors.Validation(
				exterrors.CodeInvalidAzureYaml,
				fmt.Sprintf("%s requires both container registry endpoint and "+
					"resource ID", mode),
				"set AZURE_CONTAINER_REGISTRY_ENDPOINT and "+
					"AZURE_CONTAINER_REGISTRY_RESOURCE_ID, then retry",
			)
		}
		if mode == projectEjectAcrAlreadyConnected && connection == "" {
			return "", exterrors.Validation(
				exterrors.CodeInvalidAzureYaml,
				"already-connected requires "+
					"AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
				"set the existing project connection name and retry",
			)
		}
		return mode, nil
	}
	if mode != "" {
		return mode, nil
	}
	if endpoint == "" && resourceID == "" && connection == "" {
		return projectEjectAcrCreate, nil
	}
	if endpoint == "" || resourceID == "" {
		return "", exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			"existing container registry state is incomplete",
			"set both registry endpoint and resource ID, or clear both",
		)
	}
	if connection == "" {
		return projectEjectAcrReuseConnect, nil
	}
	return projectEjectAcrAlreadyConnected, nil
}

func projectEjectHasAcrState(values map[string]string) bool {
	for _, key := range []string{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID",
		"AZURE_CONTAINER_REGISTRY_ENDPOINT",
		"AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
		"AZD_FOUNDRY_RESOURCE_GROUP_ID",
	} {
		if strings.TrimSpace(values[key]) != "" {
			return true
		}
	}
	return false
}

func writeExistingProjectBicep(
	infraDir, module string,
	params map[string]any,
	mode projectEjectAcrMode,
	values map[string]string,
) error {
	source, err := fs.ReadFile(
		synthesis.TemplatesFS(),
		"templates/existing-project-eject.bicep.tmpl",
	)
	if err != nil {
		return fmt.Errorf("read existing-project Bicep template: %w", err)
	}
	tmpl, err := template.New("existing-project.bicep").Parse(
		string(source),
	)
	if err != nil {
		return fmt.Errorf("parse existing-project Bicep template: %w", err)
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, struct {
		AcrMode         string
		AcrPullAssigned bool
	}{
		AcrMode: string(mode),
		AcrPullAssigned: strings.EqualFold(
			strings.TrimSpace(values["AZD_FOUNDRY_ACR_PULL_ASSIGNED"]),
			"true",
		),
	}); err != nil {
		return fmt.Errorf("render existing-project Bicep template: %w", err)
	}
	// #nosec G306
	if err := os.WriteFile(
		filepath.Join(infraDir, module+".bicep"),
		rendered.Bytes(),
		0644,
	); err != nil {
		return fmt.Errorf("write existing-project Bicep entrypoint: %w",
			err)
	}
	moduleDir := filepath.Join(infraDir, "modules")
	// #nosec G301
	if err := os.MkdirAll(moduleDir, 0755); err != nil {
		return fmt.Errorf("create existing-project Bicep module directory: %w",
			err)
	}
	projectModule, err := fs.ReadFile(
		synthesis.TemplatesFS(),
		"templates/modules/foundry-project.bicep",
	)
	if err != nil {
		return fmt.Errorf("read existing-project Bicep module: %w", err)
	}
	// #nosec G306
	if err := os.WriteFile(
		filepath.Join(moduleDir, "foundry-project.bicep"),
		projectModule,
		0644,
	); err != nil {
		return fmt.Errorf("write existing-project Bicep module: %w", err)
	}
	if mode == projectEjectAcrCreate ||
		(mode == projectEjectAcrReuseConnect &&
			!strings.EqualFold(
				strings.TrimSpace(values["AZD_FOUNDRY_ACR_PULL_ASSIGNED"]),
				"true",
			)) {
		registrySource, err := fs.ReadFile(
			synthesis.TemplatesFS(),
			"templates/modules/container-registry-eject.bicep.tmpl",
		)
		if err != nil {
			return fmt.Errorf("read container registry Bicep template: %w",
				err)
		}
		registryTemplate, err := template.New("container-registry.bicep").
			Parse(string(registrySource))
		if err != nil {
			return fmt.Errorf("parse container registry Bicep template: %w",
				err)
		}
		var registry bytes.Buffer
		if err := registryTemplate.Execute(&registry, struct {
			AcrMode         string
			AcrPullAssigned bool
		}{
			AcrMode:         string(mode),
			AcrPullAssigned: false,
		}); err != nil {
			return fmt.Errorf("render container registry Bicep template: %w",
				err)
		}
		// #nosec G306
		if err := os.WriteFile(
			filepath.Join(moduleDir, "container-registry.bicep"),
			registry.Bytes(),
			0644,
		); err != nil {
			return fmt.Errorf("write container registry Bicep module: %w",
				err)
		}
	}

	outputParams := map[string]any{}
	maps.Copy(outputParams, params)
	outputParams["projectResourceId"] = "${AZURE_AI_PROJECT_ID}"
	outputParams["projectEndpoint"] = "${FOUNDRY_PROJECT_ENDPOINT}"
	delete(outputParams, "includeAcr")
	switch mode {
	case projectEjectAcrCreate:
		outputParams["resourceGroupName"] =
			"${AZURE_FOUNDRY_RESOURCE_GROUP=rg-${AZURE_ENV_NAME}-foundry}"
		outputParams["location"] = "${AZURE_LOCATION}"
		outputParams["resourceTokenSalt"] = "${AZD_RESOURCE_TOKEN_SALT}"
		outputParams["tags"] = map[string]string{
			"azd-env-name": "${AZURE_ENV_NAME}",
		}
	case projectEjectAcrReuseConnect, projectEjectAcrAlreadyConnected:
		outputParams["existingAcrEndpoint"] =
			values["AZURE_CONTAINER_REGISTRY_ENDPOINT"]
		outputParams["existingAcrResourceId"] =
			values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]
		if mode == projectEjectAcrAlreadyConnected {
			outputParams["existingAcrConnectionName"] =
				values["AZURE_AI_PROJECT_ACR_CONNECTION_NAME"]
		}
	}
	return writeJSONFile(
		filepath.Join(infraDir, module+".parameters.json"),
		map[string]any{
			"$schema": "https://schema.management.azure.com/schemas/" +
				"2019-04-01/deploymentParameters.json#",
			"contentVersion": "1.0.0.0",
			"parameters":     projectEjectParameterValues(outputParams),
		},
	)
}

func projectEjectParameterValues(
	params map[string]any,
) map[string]any {
	wrapped := make(map[string]any, len(params))
	for key, value := range params {
		wrapped[key] = map[string]any{"value": value}
	}
	return wrapped
}

func writeExistingProjectTerraform(
	infraDir, module string,
	params map[string]any,
	mode projectEjectAcrMode,
	values map[string]string,
) error {
	filesystem := synthesis.ExistingProjectTerraformTemplatesFS()
	entries, err := fs.ReadDir(
		filesystem, "templates/terraform-existing-project",
	)
	if err != nil {
		return fmt.Errorf("read existing-project Terraform templates: %w",
			err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".tf") ||
			name == "container-registry-create.tf" ||
			name == "container-registry-reuse.tf" ||
			name == "container-registry-connect.tf" {
			continue
		}
		data, err := fs.ReadFile(
			filesystem,
			"templates/terraform-existing-project/"+name,
		)
		if err != nil {
			return fmt.Errorf("read Terraform template %s: %w", name, err)
		}
		// #nosec G306
		if err := os.WriteFile(
			filepath.Join(infraDir, name), data, 0644,
		); err != nil {
			return fmt.Errorf("write Terraform template %s: %w", name, err)
		}
	}

	var registrySource string
	switch mode {
	case projectEjectAcrCreate:
		registrySource = "container-registry-create.tf"
	case projectEjectAcrReuseConnect:
		registrySource = "container-registry-reuse.tf"
		if strings.EqualFold(
			strings.TrimSpace(values["AZD_FOUNDRY_ACR_PULL_ASSIGNED"]),
			"true",
		) {
			registrySource = "container-registry-connect.tf"
		}
	}
	if registrySource != "" {
		data, err := fs.ReadFile(
			filesystem,
			"templates/terraform-existing-project/"+registrySource,
		)
		if err != nil {
			return fmt.Errorf("read Terraform registry template: %w", err)
		}
		// #nosec G306
		if err := os.WriteFile(
			filepath.Join(infraDir, "container-registry.tf"),
			data, 0644,
		); err != nil {
			return fmt.Errorf("write container-registry.tf: %w", err)
		}
	}

	outputs, err := fs.ReadFile(
		filesystem,
		"templates/terraform-existing-project/outputs.tf.tmpl",
	)
	if err != nil {
		return fmt.Errorf("read Terraform outputs template: %w", err)
	}
	outputTemplate, err := template.New("outputs.tf").Parse(string(outputs))
	if err != nil {
		return fmt.Errorf("parse Terraform outputs template: %w", err)
	}
	var rendered bytes.Buffer
	if err := outputTemplate.Execute(&rendered, struct {
		IncludeAcr bool
		Layer      bool
		AcrMode    string
	}{
		IncludeAcr: mode != projectEjectAcrNone,
		AcrMode:    string(mode),
	}); err != nil {
		return fmt.Errorf("render Terraform outputs: %w", err)
	}
	// #nosec G306
	if err := os.WriteFile(
		filepath.Join(infraDir, "outputs.tf"),
		rendered.Bytes(), 0644,
	); err != nil {
		return fmt.Errorf("write Terraform outputs: %w", err)
	}

	// #nosec G101
	tfvars := map[string]any{
		"subscription_id":              "${AZURE_SUBSCRIPTION_ID}",
		"tenant_id":                    "${AZURE_TENANT_ID}",
		"project_resource_id":          "${AZURE_AI_PROJECT_ID}",
		"project_endpoint":             "${FOUNDRY_PROJECT_ENDPOINT}",
		"location":                     "${AZURE_LOCATION}",
		"resource_group_name":          "${AZURE_FOUNDRY_RESOURCE_GROUP=rg-${AZURE_ENV_NAME}-foundry}",
		"environment_name":             "${AZURE_ENV_NAME}",
		"resource_token_salt":          "${AZD_RESOURCE_TOKEN_SALT}",
		"existing_acr_endpoint":        values["AZURE_CONTAINER_REGISTRY_ENDPOINT"],
		"existing_acr_resource_id":     values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"],
		"existing_acr_connection_name": values["AZURE_AI_PROJECT_ACR_CONNECTION_NAME"],
	}
	if deployments, ok := params["deployments"]; ok {
		tfvars["deployments"] = deployments
	}
	connections, ok := params["connections"].([]synthesis.Connection)
	if !ok {
		return fmt.Errorf(
			"connections parameter has unexpected type %T",
			params["connections"],
		)
	}
	credentials, ok := params["connectionCredentials"].(map[string]map[string]any)
	if !ok {
		return fmt.Errorf(
			"connectionCredentials parameter has unexpected type %T",
			params["connectionCredentials"],
		)
	}
	tfvars["connections"] = synthesis.JoinConnectionCredentials(
		connections, credentials,
	)
	if err := writeJSONFile(
		filepath.Join(infraDir, module+".tfvars.json"), tfvars,
	); err != nil {
		return fmt.Errorf("write Terraform variables: %w", err)
	}
	// #nosec G306
	if err := os.WriteFile(
		filepath.Join(infraDir, foundryTerraformMarker),
		[]byte(foundryTerraformMarkerVersion),
		0644,
	); err != nil {
		return fmt.Errorf("write Terraform ownership marker: %w", err)
	}
	return nil
}

func projectEjectSetServiceEndpoint(
	raw []byte, serviceName, endpoint string,
) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("parse azure.yaml: %s", err),
			"verify azure.yaml is valid YAML",
		)
	}
	if len(root.Content) == 0 ||
		root.Content[0].Kind != yaml.MappingNode {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			"azure.yaml is not a YAML mapping",
			"verify azure.yaml is a valid azd project file",
		)
	}
	services := projectEjectMappingValue(root.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			"azure.yaml does not declare a services mapping",
			"add the Foundry project service and retry",
		)
	}
	service := projectEjectMappingValue(services, serviceName)
	if service == nil || service.Kind != yaml.MappingNode {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("azure.yaml service %q is not a mapping", serviceName),
			"configure the Foundry project service and retry",
		)
	}
	projectEjectSetMappingScalar(service, "endpoint", endpoint)
	updated, err := yaml.Marshal(&root)
	if err != nil {
		return nil, fmt.Errorf("marshal azure.yaml: %w", err)
	}
	return updated, nil
}

func projectEjectMappingValue(
	mapping *yaml.Node, key string,
) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func projectEjectSetMappingScalar(
	mapping *yaml.Node, key, value string,
) {
	if current := projectEjectMappingValue(mapping, key); current != nil {
		current.Kind = yaml.ScalarNode
		current.Tag = "!!str"
		current.Value = value
		return
	}
	mapping.Content = append(
		mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}
