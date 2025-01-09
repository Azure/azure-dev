// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
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
	ErrNoDefaultService = errors.New("no default service selection matches the working directory")
)

// ProjectManager provides a layer for working with root level azd projects
// and invoking project specific commands
type ProjectManager interface {
	// Initializes the project and all child services defined within the project configuration
	//
	// The initialization process will instantiate the framework & service target associated
	// with the service config that enables the scenario for these components to add event
	// handlers to participate in the lifecycle of an azd project
	Initialize(ctx context.Context, projectConfig *ProjectConfig) error

	// Returns the default service name to target based on the current working directory.
	//
	//   - If the working directory is the project directory, then an empty string is returned to indicate all services.
	//   - If the working directory is a service directory, then the name of the service is returned.
	//   - If the working directory is neither the project directory nor a service directory, then
	//     ErrNoDefaultService is returned.
	DefaultServiceFromWd(ctx context.Context, projectConfig *ProjectConfig) (serviceConfig *ServiceConfig, err error)

	// Ensures that all required tools are installed for the project and all child services
	// This includes tools required by the framework and tools required by the service target
	EnsureAllTools(ctx context.Context, projectConfig *ProjectConfig, serviceFilterFn ServiceFilterPredicate) error

	// Ensures that all required framework tools are installed for the project and all child services
	EnsureFrameworkTools(ctx context.Context, projectConfig *ProjectConfig, serviceFilterFn ServiceFilterPredicate) error

	// Ensures that all required service target tools are installed for the project and all child services
	EnsureServiceTargetTools(ctx context.Context, projectConfig *ProjectConfig, serviceFilterFn ServiceFilterPredicate) error

	// Ensures that all required tools for restore are installed for the project and all child services. This is like
	// EnsureFrameworkTools but treats docker projects differently - it requires the tools for the inner project (i.e. npm)
	// instead of needing docker tools themselves, since when doing a project restore, docker is not invoked.
	EnsureRestoreTools(ctx context.Context, projectConfig *ProjectConfig, serviceFilterFn ServiceFilterPredicate) error
}

// ServiceFilterPredicate is a function that can be used to filter services that match a given criteria
type ServiceFilterPredicate func(svc *ServiceConfig) bool

type projectManager struct {
	azdContext     *azdcontext.AzdContext
	serviceManager ServiceManager
	importManager  *ImportManager
}

// NewProjectManager creates a new instance of the ProjectManager
func NewProjectManager(
	azdContext *azdcontext.AzdContext,
	serviceManager ServiceManager,
	importManager *ImportManager,
) ProjectManager {
	return &projectManager{
		azdContext:     azdContext,
		serviceManager: serviceManager,
		importManager:  importManager,
	}
}

// Initializes the project and all child services defined within the project configuration
func (pm *projectManager) Initialize(ctx context.Context, projectConfig *ProjectConfig) error {
	servicesStable, err := pm.importManager.ServiceStable(ctx, projectConfig)
	if err != nil {
		return err
	}

	serviceTargets := make([]string, 0, len(servicesStable))
	for _, svc := range servicesStable {
		serviceTargets = append(serviceTargets, string(svc.Host))
	}

	tracing.SetUsageAttributes(fields.ProjectServiceTargetsKey.StringSlice(serviceTargets))

	for _, svc := range servicesStable {
		if err := pm.serviceManager.Initialize(ctx, svc); err != nil {
			return fmt.Errorf("initializing service '%s', %w", svc.Name, err)
		}
	}

	return nil
}

// Returns the default service name to target based on the current working directory.
func (pm *projectManager) DefaultServiceFromWd(
	ctx context.Context,
	projectConfig *ProjectConfig,
) (serviceConfig *ServiceConfig, err error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	if wd == pm.azdContext.ProjectDirectory() {
		return nil, nil
	}

	servicesStable, err := pm.importManager.ServiceStable(ctx, projectConfig)
	if err != nil {
		return nil, err
	}

	for _, svcConfig := range servicesStable {
		if wd == svcConfig.Path() {
			return svcConfig, nil
		}
	}

	return nil, ErrNoDefaultService
}

func (pm *projectManager) EnsureAllTools(
	ctx context.Context,
	projectConfig *ProjectConfig,
	serviceFilterFn ServiceFilterPredicate,
) error {
	var projectTools []tools.ExternalTool

	servicesStable, err := pm.importManager.ServiceStable(ctx, projectConfig)
	if err != nil {
		return err
	}

	for _, svc := range servicesStable {
		if serviceFilterFn != nil && !serviceFilterFn(svc) {
			continue
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

func (pm *projectManager) EnsureFrameworkTools(
	ctx context.Context,
	projectConfig *ProjectConfig,
	serviceFilterFn ServiceFilterPredicate,
) error {
	var requiredTools []tools.ExternalTool

	servicesStable, err := pm.importManager.ServiceStable(ctx, projectConfig)
	if err != nil {
		return err
	}

	for _, svc := range servicesStable {
		if serviceFilterFn != nil && !serviceFilterFn(svc) {
			continue
		}

		frameworkService, err := pm.serviceManager.GetFrameworkService(ctx, svc)
		if err != nil {
			return fmt.Errorf("getting framework service: %w", err)
		}

		frameworkTools := frameworkService.RequiredExternalTools(ctx, svc)
		requiredTools = append(requiredTools, frameworkTools...)
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(requiredTools)...); err != nil {
		return err
	}

	return nil
}

func (pm *projectManager) EnsureServiceTargetTools(
	ctx context.Context,
	projectConfig *ProjectConfig,
	serviceFilterFn ServiceFilterPredicate,
) error {
	var requiredTools []tools.ExternalTool

	servicesStable, err := pm.importManager.ServiceStable(ctx, projectConfig)
	if err != nil {
		return err
	}

	for _, svc := range servicesStable {
		if serviceFilterFn != nil && !serviceFilterFn(svc) {
			continue
		}

		serviceTarget, err := pm.serviceManager.GetServiceTarget(ctx, svc)
		if err != nil {
			return fmt.Errorf("getting service target: %w", err)
		}

		serviceTargetTools := serviceTarget.RequiredExternalTools(ctx, svc)
		requiredTools = append(requiredTools, serviceTargetTools...)
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(requiredTools)...); err != nil {
		return err
	}

	return nil
}

func (pm *projectManager) EnsureRestoreTools(
	ctx context.Context,
	projectConfig *ProjectConfig,
	serviceFilterFn ServiceFilterPredicate,
) error {
	var requiredTools []tools.ExternalTool

	servicesStable, err := pm.importManager.ServiceStable(ctx, projectConfig)
	if err != nil {
		return err
	}

	for _, svc := range servicesStable {
		if serviceFilterFn != nil && !serviceFilterFn(svc) {
			continue
		}

		frameworkService, err := pm.serviceManager.GetFrameworkService(ctx, svc)
		if err != nil {
			return fmt.Errorf("getting framework service: %w", err)
		}

		var frameworkTools []tools.ExternalTool
		if dp, ok := frameworkService.(*dockerProject); ok {
			frameworkTools = dp.framework.RequiredExternalTools(ctx, svc)
		} else {
			frameworkTools = frameworkService.RequiredExternalTools(ctx, svc)
		}

		requiredTools = append(requiredTools, frameworkTools...)
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(requiredTools)...); err != nil {
		return err
	}

	return nil
}
