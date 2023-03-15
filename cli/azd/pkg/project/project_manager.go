package project

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

const (
	ProjectEventDeploy    ext.Event = "deploy"
	ProjectEventProvision ext.Event = "provision"
)

var (
	ProjectEvents []ext.Event = []ext.Event{
		ProjectEventProvision,
		ProjectEventDeploy,
	}
)

// ProjectManager provides a layer for working with root level azd projects
// and invoking project specific commands
type ProjectManager interface {
	// Initializes the project and all child services defined within the project configuration
	//
	// The initialization process will instantiate the framework & service target associated
	// with the service config that enables the scenario for these components to add event
	// handlers to participate in the lifecycle of an azd project
	//
	// The initialization process will also ensure that all required tools are installed
	Initialize(ctx context.Context, projectConfig *ProjectConfig) error

	// TODO: Add lifecycle functions to perform action on all services.
	// Restore, build, package, publish & deploy
}

type projectManager struct {
	serviceManager ServiceManager
}

// NewProjectManager creates a new instance of the ProjectManager
func NewProjectManager(
	serviceManager ServiceManager,
) ProjectManager {
	return &projectManager{
		serviceManager: serviceManager,
	}
}

// Initializes the project and all child services defined within the project configuration

func (pm *projectManager) Initialize(ctx context.Context, projectConfig *ProjectConfig) error {
	var projectTools []tools.ExternalTool

	for _, svc := range projectConfig.Services {
		if err := pm.serviceManager.Initialize(ctx, svc); err != nil {
			return fmt.Errorf("initializing service '%s', %w", svc.Name, err)
		}

		svcTools, err := pm.serviceManager.GetRequiredTools(ctx, svc)
		if err != nil {
			return fmt.Errorf("getting service required tools: %w", err)
		}

		projectTools = append(projectTools, svcTools...)
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(projectTools)...); err != nil {
		return err
	}

	return nil
}
