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
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"gopkg.in/yaml.v3"
)

const (
	//nolint:lll
	projectSchemaAnnotation = "# yaml-language-server: $schema=https://raw.githubusercontent.com/Azure/azure-dev/main/schemas/v1.0/azure.yaml.json"

	ProjectEventDeploy    ext.Event = "deploy"
	ProjectEventProvision ext.Event = "provision"
)

var (
	ProjectEvents []ext.Event = []ext.Event{
		ProjectEventProvision,
		ProjectEventDeploy,
	}
)

type ProjectManager interface {
	New(ctx context.Context, projectFilePath string, projectName string) (*ProjectConfig, error)
	Initialize(ctx context.Context, projectConfig *ProjectConfig) error
	Parse(ctx context.Context, yamlContent string) (*ProjectConfig, error)
	Load(ctx context.Context, projectPath string) (*ProjectConfig, error)
	Save(ctx context.Context, projectConfig *ProjectConfig, projectFilePath string) error

	// TODO: Add lifecycle functions to perform action on all services.
	// Restore, build, package & publish
}

type projectManager struct {
	azdContext     *azdcontext.AzdContext
	env            *environment.Environment
	commandRunner  exec.CommandRunner
	azCli          azcli.AzCli
	console        input.Console
	serviceManager ServiceManager
}

func NewProjectManager(
	azdContext *azdcontext.AzdContext,
	env *environment.Environment,
	commandRunner exec.CommandRunner,
	azCli azcli.AzCli,
	console input.Console,
	serviceManager ServiceManager,
) ProjectManager {
	return &projectManager{
		azdContext:     azdContext,
		env:            env,
		commandRunner:  commandRunner,
		azCli:          azCli,
		console:        console,
		serviceManager: serviceManager,
	}
}

func (pm *projectManager) New(ctx context.Context, projectFilePath string, projectName string) (*ProjectConfig, error) {
	newProject := &ProjectConfig{
		Name: projectName,
	}

	err := pm.Save(ctx, newProject, projectFilePath)
	if err != nil {
		return nil, fmt.Errorf("marshaling project file to yaml: %w", err)
	}

	return pm.Load(ctx, projectFilePath)
}

func (pm *projectManager) Initialize(ctx context.Context, projectConfig *ProjectConfig) error {
	var allTools []tools.ExternalTool

	for _, svc := range projectConfig.Services {
		frameworkService, err := pm.serviceManager.GetFrameworkService(ctx, svc)
		if err != nil {
			return fmt.Errorf("getting framework services: %w", err)
		}
		if err := frameworkService.Initialize(ctx, svc); err != nil {
			return err
		}

		requiredTools := frameworkService.RequiredExternalTools(ctx)
		allTools = append(allTools, requiredTools...)
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(allTools)...); err != nil {
		return err
	}

	return nil
}

// Parse will parse a project from a yaml string and return the project configuration
func (pm *projectManager) Parse(ctx context.Context, yamlContent string) (*ProjectConfig, error) {
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

// LoadProjectConfig loads the azure.yaml configuring into an viewable structure
// This does not evaluate any tooling
func (pm *projectManager) Load(ctx context.Context, projectFilePath string) (*ProjectConfig, error) {
	log.Printf("Reading project from file '%s'\n", projectFilePath)
	bytes, err := os.ReadFile(projectFilePath)
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}

	yaml := string(bytes)

	projectConfig, err := pm.Parse(ctx, yaml)
	if err != nil {
		return nil, fmt.Errorf("parsing project file: %w", err)
	}

	if projectConfig.Metadata != nil {
		telemetry.SetUsageAttributes(fields.StringHashed(fields.TemplateIdKey, projectConfig.Metadata.Template))
	}

	projectConfig.Path = filepath.Dir(projectFilePath)
	return projectConfig, nil
}

// Saves the current instance back to the azure.yaml file
func (pm *projectManager) Save(ctx context.Context, projectConfig *ProjectConfig, projectFilePath string) error {
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
