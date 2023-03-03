package project

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"gopkg.in/yaml.v3"
)

type ConfigManager interface {
	Initialize(projectConfig *ProjectConfig) error
	Load(projectPath string) (*ProjectConfig, error)
	Parse(yaml string) (*ProjectConfig, error)
	Save(projectConfig *ProjectConfig) error
}

type configManager struct {
	env           *environment.Environment
	commandRunner exec.CommandRunner
}

func (cm *configManager) Initialize(ctx context.Context, projectConfig *ProjectConfig) error {
	var allTools []tools.ExternalTool
	for _, svc := range projectConfig.Services {
		frameworkService, err := svc.GetFrameworkService(ctx, cm.env, cm.commandRunner)
		if err != nil {
			return fmt.Errorf("getting framework services: %w", err)
		}
		if err := frameworkService.Initialize(ctx); err != nil {
			return err
		}

		requiredTools := frameworkService.RequiredExternalTools()
		allTools = append(allTools, requiredTools...)
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(allTools)...); err != nil {
		return err
	}

	return nil

}

func (cm *configManager) Load(projectPath string) (*ProjectConfig, error) {
	log.Printf("Reading project from file '%s'\n", projectPath)
	bytes, err := os.ReadFile(projectPath)
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}

	yaml := string(bytes)

	projectConfig, err := ParseProjectConfig(yaml)
	if err != nil {
		return nil, fmt.Errorf("parsing project file: %w", err)
	}

	if projectConfig.Metadata != nil {
		telemetry.SetUsageAttributes(fields.StringHashed(fields.TemplateIdKey, projectConfig.Metadata.Template))
	}

	projectConfig.Path = filepath.Dir(projectPath)
	return projectConfig, nil
}

func (cm *configManager) Parse(yamlContent string) (*ProjectConfig, error) {
	var projectFile ProjectConfig

	if err := yaml.Unmarshal([]byte(yamlContent), &projectFile); err != nil {
		return nil, fmt.Errorf(
			"unable to parse azure.yaml file. Please check the format of the file, "+
				"and also verify you have the latest version of the CLI: %w",
			err,
		)
	}

	projectFile.EventDispatcher = ext.NewEventDispatcher[ProjectLifecycleEventArgs](ProjectEvents...)

	for key, svc := range projectFile.Services {
		svc.Name = key
		svc.Project = &projectFile
		svc.EventDispatcher = ext.NewEventDispatcher[ServiceLifecycleEventArgs](ServiceEvents...)

		// By convention, the name of the infrastructure module to use when doing an IaC based deployment is the friendly
		// name of the service. This may be overridden by the `module` property of `azure.yaml`
		if svc.Module == "" {
			svc.Module = key
		}

		if svc.Language == "" || svc.Language == "csharp" || svc.Language == "fsharp" {
			svc.Language = "dotnet"
		}
	}

	return &projectFile, nil
}

func (cm *configManager) Save(projectConfig *ProjectConfig) error {
	projectBytes, err := yaml.Marshal(projectConfig)
	if err != nil {
		return fmt.Errorf("marshalling project yaml: %w", err)
	}

	err = os.WriteFile(projectConfig.Path, projectBytes, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("saving project file: %w", err)
	}

	return nil
}
