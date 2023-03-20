package project

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
)

const (
	//nolint:lll
	projectSchemaAnnotation = "# yaml-language-server: $schema=https://raw.githubusercontent.com/Azure/azure-dev/main/schemas/v1.0/azure.yaml.json"
)

func New(ctx context.Context, projectFilePath string, projectName string) (*ProjectConfig, error) {
	newProject := &ProjectConfig{
		Name: projectName,
	}

	err := Save(ctx, newProject, projectFilePath)
	if err != nil {
		return nil, fmt.Errorf("marshaling project file to yaml: %w", err)
	}

	return Load(ctx, projectFilePath)
}

// Parse will parse a project from a yaml string and return the project configuration
func Parse(ctx context.Context, yamlContent string) (*ProjectConfig, error) {
	var projectConfig ProjectConfig

	if err := yaml.Unmarshal([]byte(yamlContent), &projectConfig); err != nil {
		return nil, fmt.Errorf(
			"unable to parse azure.yaml file. Please check the format of the file, "+
				"and also verify you have the latest version of the CLI: %w",
			err,
		)
	}

	projectConfig.EventDispatcher = ext.NewEventDispatcher[ProjectLifecycleEventArgs]()

	for key, svc := range projectConfig.Services {
		svc.Name = key
		svc.Project = &projectConfig
		svc.EventDispatcher = ext.NewEventDispatcher[ServiceLifecycleEventArgs]()

		// By convention, the name of the infrastructure module to use when doing an IaC based deployment is the friendly
		// name of the service. This may be overridden by the `module` property of `azure.yaml`
		if svc.Module == "" {
			svc.Module = key
		}

		if svc.Language == "" || svc.Language == "csharp" || svc.Language == "fsharp" {
			svc.Language = "dotnet"
		}
	}

	return &projectConfig, nil
}

// Load hydrates the azure.yaml configuring into an viewable structure
// This does not evaluate any tooling
func Load(ctx context.Context, projectFilePath string) (*ProjectConfig, error) {
	log.Printf("Reading project from file '%s'\n", projectFilePath)
	bytes, err := os.ReadFile(projectFilePath)
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}

	yaml := string(bytes)

	projectConfig, err := Parse(ctx, yaml)
	if err != nil {
		return nil, fmt.Errorf("parsing project file: %w", err)
	}

	if projectConfig.Metadata != nil {
		telemetry.SetUsageAttributes(fields.StringHashed(fields.ProjectTemplateIdKey, projectConfig.Metadata.Template))
	}

	if projectConfig.Name != "" {
		telemetry.SetUsageAttributes(fields.StringHashed(fields.ProjectNameKey, projectConfig.Name))
	}

	if projectConfig.Services != nil {
		hosts := make([]string, len(projectConfig.Services))
		languages := make([]string, len(projectConfig.Services))
		i := 0
		for _, svcConfig := range projectConfig.Services {
			hosts[i] = svcConfig.Host
			languages[i] = svcConfig.Language
			i++
		}

		slices.Sort(hosts)
		slices.Sort(languages)

		telemetry.SetUsageAttributes(fields.StringSliceHashed(fields.ProjectServiceLanguagesKey, languages))
		telemetry.SetUsageAttributes(fields.StringSliceHashed(fields.ProjectServiceHostsKey, hosts))
	}

	projectConfig.Path = filepath.Dir(projectFilePath)
	return projectConfig, nil
}

// Saves the current instance back to the azure.yaml file
func Save(ctx context.Context, projectConfig *ProjectConfig, projectFilePath string) error {
	projectBytes, err := yaml.Marshal(projectConfig)
	if err != nil {
		return fmt.Errorf("marshalling project yaml: %w", err)
	}

	projectFileContents := bytes.NewBufferString(projectSchemaAnnotation + "\n\n")
	_, err = projectFileContents.Write(projectBytes)
	if err != nil {
		return fmt.Errorf("preparing new project file contents: %w", err)
	}

	err = os.WriteFile(projectFilePath, projectFileContents.Bytes(), osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("saving project file: %w", err)
	}

	projectConfig.Path = projectFilePath

	return nil
}
