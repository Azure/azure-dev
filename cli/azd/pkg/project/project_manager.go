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

type ProjectManager interface {
	Initialize(ctx context.Context, projectConfig *ProjectConfig) error

	// TODO: Add lifecycle functions to perform action on all services.
	// Restore, build, package & publish
}

type projectManager struct {
	serviceManager ServiceManager
}

func NewProjectManager(
	serviceManager ServiceManager,
) ProjectManager {
	return &projectManager{
		serviceManager: serviceManager,
	}
}

func (pm *projectManager) Initialize(ctx context.Context, projectConfig *ProjectConfig) error {
	var projectTools []tools.ExternalTool

	for _, svc := range projectConfig.Services {
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
