// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"gopkg.in/yaml.v3"
)

const (
	projectSchemaAnnotation = "# yaml-language-server: $schema=" +
		"https://raw.githubusercontent.com/Azure/azure-dev/main/schemas/v1.0/azure.yaml.json"
)

type Project struct {
	Name string
	// The resource group targeted by the current project
	ResourceGroupName string
	Config            *ProjectConfig
	Metadata          *ProjectMetadata
	Services          []*Service
}

// ReadProject reads a project file and sets the configured template
// to include the template name in `metadata/template` from the yaml in projectPath.
func ReadProject(
	ctx context.Context,
	console input.Console,
	azCli azcli.AzCli,
	commandRunner exec.CommandRunner,
	projectPath string,
	env *environment.Environment,
) (*Project, error) {
	projectRootDir := filepath.Dir(projectPath)

	// Load Project configuration
	projectConfig, err := LoadProjectConfig(projectRootDir)
	if err != nil {
		return nil, fmt.Errorf("reading project config: %w", err)
	}

	// Evaluate project
	project, err := projectConfig.GetProject(ctx, env, console, azCli, commandRunner)
	if err != nil {
		return nil, fmt.Errorf("reading project: %w", err)
	}

	return project, nil
}

func NewProject(path string, name string) (*Project, error) {
	projectBytes, err := yaml.Marshal(ProjectConfig{
		Name: name,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling project file to yaml: %w", err)
	}

	projectFileContents := bytes.NewBufferString(projectSchemaAnnotation + "\n\n")
	_, err = projectFileContents.Write(projectBytes)
	if err != nil {
		return nil, fmt.Errorf("preparing new project file contents: %w", err)
	}

	err = os.WriteFile(path, projectFileContents.Bytes(), osutil.PermissionFile)
	if err != nil {
		return nil, fmt.Errorf("writing project file: %w", err)
	}

	return &Project{
		Name:     name,
		Services: make([]*Service, 0),
	}, nil
}

// GetResourceGroupName gets the resource group name for the project.
//
// The resource group name is resolved in the following order:
//   - The user defined value in `azure.yaml`
//   - The user defined environment value `AZURE_RESOURCE_GROUP`
//
// - Resource group discovery by querying Azure Resources
// (see `resourceManager.FindResourceGroupForEnvironment` for more
// details)
func GetResourceGroupName(
	ctx context.Context,
	azCli azcli.AzCli,
	projectConfig *ProjectConfig,
	env *environment.Environment,
) (string, error) {

	name, err := projectConfig.ResourceGroupName.Envsubst(env.Getenv)
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(name) != "" {
		return name, nil
	}

	envResourceGroupName := environment.GetResourceGroupNameFromEnvVar(env)
	if envResourceGroupName != "" {
		return envResourceGroupName, nil
	}

	resourceManager := infra.NewAzureResourceManager(azCli)
	resourceGroupName, err := resourceManager.FindResourceGroupForEnvironment(ctx, env)
	if err != nil {
		return "", err
	}

	return resourceGroupName, nil
}
