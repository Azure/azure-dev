// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azure.ai.projects/internal/provisioning"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"google.golang.org/protobuf/types/known/structpb"
)

const projectDeploymentsEnvKey = provisioning.ProjectDeploymentsEnvKey

type projectServiceConfig struct {
	Endpoint    string                 `json:"endpoint,omitempty"`
	Deployments []synthesis.Deployment `json:"deployments,omitempty"`
}

func firstProjectDeploymentName(value map[string]any) string {
	deployments, ok := value["deployments"].([]any)
	if !ok || len(deployments) == 0 {
		return ""
	}
	deployment, ok := deployments[0].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := deployment["name"].(string)
	return strings.TrimSpace(name)
}

// defaultModelDeploymentName chooses AZURE_AI_MODEL_DEPLOYMENT_NAME.
// A present environment override supplies the name, and [] stays empty.
// YAML is used only when AI_PROJECT_DEPLOYMENTS is missing.
func defaultModelDeploymentName(envValue string, service map[string]any) (string, error) {
	deployments, present, err := provisioning.DecodeProjectDeployments(envValue)
	if err != nil {
		return "", err
	}
	if present {
		if len(deployments) == 0 {
			return "", nil
		}
		return strings.TrimSpace(deployments[0].Name), nil
	}
	return firstProjectDeploymentName(service), nil
}

func projectLifecycleHandler(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	args *azdext.ProjectEventArgs,
) error {
	if args == nil || args.Project == nil {
		return fmt.Errorf("project lifecycle event has no project")
	}

	cfg, found, err := loadProjectServiceConfig(
		args.Project.Services,
		args.Project.Path,
	)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	current, err := azdClient.Environment().GetCurrent(
		ctx,
		&azdext.EmptyRequest{},
	)
	if err != nil {
		return fmt.Errorf("resolving current azd environment: %w", err)
	}
	if current.GetEnvironment().GetName() == "" {
		return fmt.Errorf("current azd environment has no name")
	}

	envName := current.GetEnvironment().GetName()
	values, err := currentProjectEnvironment(ctx, azdClient, envName)
	if err != nil {
		return err
	}
	onDisk, err := projectOnDiskFoundryTemplatePresent(args.Project)
	if err != nil {
		return err
	}
	if deployments, present, err := provisioning.DecodeProjectDeployments(values[projectDeploymentsEnvKey]); err != nil {
		return err
	} else if present {
		if onDisk {
			return nil
		}
		return seedProjectModelDeploymentName(ctx, azdClient, envName, values, deployments)
	}
	if len(cfg.Deployments) > 0 && !azdext.DetectInteractive().NoPrompt {
		if !onDisk {
			location := values["AZURE_AI_DEPLOYMENTS_LOCATION"]
			if location == "" {
				location = values["AZURE_LOCATION"]
			}
			cfg.Deployments, err = provisioning.PromptProjectDeployments(ctx, azdClient, &azdext.AzureContext{
				Scope: &azdext.AzureScope{
					SubscriptionId: values["AZURE_SUBSCRIPTION_ID"],
					TenantId:       values["AZURE_TENANT_ID"],
					Location:       location,
				},
			}, cfg.Deployments)
			if err != nil {
				return err
			}
		}
	}
	value, err := encodeProjectDeployments(cfg.Deployments)
	if err != nil {
		return err
	}
	if _, err := azdClient.Environment().SetValue(
		ctx,
		&azdext.SetEnvRequest{
			EnvName: envName,
			Key:     projectDeploymentsEnvKey,
			Value:   value,
		},
	); err != nil {
		return fmt.Errorf(
			"setting %s in azd environment: %w",
			projectDeploymentsEnvKey,
			err,
		)
	}

	if onDisk {
		return nil
	}
	return seedProjectModelDeploymentName(ctx, azdClient, envName, values, cfg.Deployments)
}

func seedProjectModelDeploymentName(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	values map[string]string,
	deployments []synthesis.Deployment,
) error {
	const key = "AZURE_AI_MODEL_DEPLOYMENT_NAME"
	if strings.TrimSpace(values[key]) != "" || len(deployments) == 0 {
		return nil
	}
	if _, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     key,
		Value:   deployments[0].Name,
	}); err != nil {
		return fmt.Errorf("setting %s in azd environment: %w", key, err)
	}
	return nil
}

func loadProjectServiceConfig(
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
) (*projectServiceConfig, bool, error) {
	var names []string
	for name, service := range services {
		if service.GetHost() == aiProjectHost {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	switch len(names) {
	case 0:
		return nil, false, nil
	case 1:
	default:
		return nil, false, fmt.Errorf(
			"multiple services use host %q: %s",
			aiProjectHost,
			strings.Join(names, ", "),
		)
	}

	service := services[names[0]]
	props := projectServiceProperties(service)
	cfg := &projectServiceConfig{}
	if props == nil {
		return cfg, true, nil
	}

	values := props.AsMap()
	if projectRoot != "" {
		resolved, err := foundry.ResolveFileRefs(values, projectRoot)
		if err != nil {
			return nil, false, fmt.Errorf(
				"resolve project service %q $ref includes: %w",
				names[0],
				err,
			)
		}
		values = resolved
	}
	data, err := json.Marshal(values)
	if err != nil {
		return nil, false, fmt.Errorf(
			"encoding project service %q config: %w",
			names[0],
			err,
		)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, false, fmt.Errorf(
			"parsing project service %q config: %w",
			names[0],
			err,
		)
	}
	return cfg, true, nil
}

func projectServiceProperties(
	service *azdext.ServiceConfig,
) *structpb.Struct {
	if props := service.GetAdditionalProperties(); props != nil &&
		len(props.GetFields()) > 0 {
		return props
	}
	return service.GetConfig()
}

func encodeProjectDeployments(
	deployments []synthesis.Deployment,
) (string, error) {
	return provisioning.EncodeProjectDeployments(deployments)
}

// projectOnDiskFoundryTemplatePresent detects ejected infrastructure
// and user-authored Bicep. Their parameters own the deployments, so
// model prompts and inferred runtime defaults must be skipped.
func projectOnDiskFoundryTemplatePresent(project *azdext.ProjectConfig) (bool, error) {
	if project == nil || project.GetPath() == "" {
		return false, nil
	}
	if ejected, err := findEjectedFoundryProjectInfrastructure(project); err != nil {
		return false, err
	} else if ejected != nil {
		return true, nil
	}
	declaration, err := readProjectInfraDeclaration(project)
	if err != nil {
		return false, err
	}

	path := "infra"
	module := "main"
	if declaration.layerCount > 0 {
		if declaration.foundryLayer == nil {
			return false, nil
		}
		if declaration.foundryLayer.path != "" {
			path = declaration.foundryLayer.path
		}
		if declaration.foundryLayer.module != "" {
			module = normalizeProjectInfraEjectModule(declaration.foundryLayer.module)
		}
	} else {
		if declaration.rootModule != "" {
			module = normalizeProjectInfraEjectModule(declaration.rootModule)
		}
		if _, infraPath := projectInfraConfig(project); strings.TrimSpace(infraPath) != "" {
			path = strings.TrimSpace(infraPath)
		}
	}

	infraDir := filepath.Join(project.GetPath(), filepath.FromSlash(path))
	for _, name := range []string{module + ".bicepparam", module + ".bicep"} {
		info, err := os.Stat(filepath.Join(infraDir, name))
		if err == nil && !info.IsDir() {
			return true, nil
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("inspect on-disk infrastructure %q: %w", filepath.Join(infraDir, name), err)
		}
	}
	return false, nil
}
