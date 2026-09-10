// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

const (
	ProjectEventProvision ext.Event = "provision"
	ProjectEventRestore   ext.Event = "restore"
	ProjectEventBuild     ext.Event = "build"
	ProjectEventPackage   ext.Event = "package"
	ProjectEventPublish   ext.Event = "publish"
	ProjectEventDeploy    ext.Event = "deploy"
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

	// InitializeServices initializes exactly the supplied services.
	// Callers must select and filter services before calling this method.
	InitializeServices(ctx context.Context, services []*ServiceConfig) error

	// InitializeFrameworks initializes only the framework service for each service in the project,
	// best-effort: unlike Initialize, it never resolves service targets, and a per-service failure
	// is skipped rather than fatal. It exists for read-only flows such as `env refresh`, which
	// need framework lifecycle hooks (for example the .NET ServiceEventEnvUpdated handler) but
	// must tolerate hosts provided by extensions that are not loaded. It returns the initialized
	// services and the skipped services with their causes.
	InitializeFrameworks(
		ctx context.Context,
		projectConfig *ProjectConfig,
	) ([]*ServiceConfig, []ServiceFrameworkInitFailure, error)

	// Returns the default service name to target based on the current working directory.
	//
	//   - If the working directory is the project directory, then an empty string is returned to indicate all services.
	//   - If the working directory is a service directory, then the name of the service is returned.
	//   - If the working directory is neither the project directory nor a service directory, then
	//     ErrNoDefaultService is returned.
	DefaultServiceFromWd(ctx context.Context, projectConfig *ProjectConfig) (serviceConfig *ServiceConfig, err error)

	// EnsureAllTools ensures required tools for the supplied services.
	// Callers must select and filter services before calling this method.
	EnsureAllTools(ctx context.Context, services []*ServiceConfig) error

	// EnsureFrameworkTools ensures framework tools for supplied services.
	// Callers must select and filter services before calling this method.
	EnsureFrameworkTools(ctx context.Context, services []*ServiceConfig) error

	// EnsureServiceTargetTools ensures target tools for these services.
	// Callers must select and filter services before calling this method.
	EnsureServiceTargetTools(ctx context.Context, services []*ServiceConfig) error

	// EnsureRestoreTools ensures restore tools for supplied services.
	// Restore uses the inner project for docker services.
	EnsureRestoreTools(ctx context.Context, services []*ServiceConfig) error
}

// ServiceFrameworkInitFailure describes a service whose framework service could not be initialized
// during best-effort initialization, along with the cause.
type ServiceFrameworkInitFailure struct {
	Service *ServiceConfig
	Err     error
}

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

	setProjectServiceTargets(servicesStable)
	return pm.InitializeServices(ctx, servicesStable)
}

func (pm *projectManager) InitializeServices(ctx context.Context, services []*ServiceConfig) error {
	for _, svc := range services {
		if err := pm.serviceManager.Initialize(ctx, svc); err != nil {
			return fmt.Errorf("initializing service '%s', %w", svc.Name, err)
		}
	}

	return nil
}

// InitializeFrameworks initializes only the framework service for each service in the
// project, on a best-effort, per-service basis. See the ProjectManager interface for details.
func (pm *projectManager) InitializeFrameworks(
	ctx context.Context,
	projectConfig *ProjectConfig,
) ([]*ServiceConfig, []ServiceFrameworkInitFailure, error) {
	servicesStable, err := pm.importManager.ServiceStable(ctx, projectConfig)
	if err != nil {
		return nil, nil, err
	}

	setProjectServiceTargets(servicesStable)

	initialized := make([]*ServiceConfig, 0, len(servicesStable))
	var skipped []ServiceFrameworkInitFailure
	for _, svc := range servicesStable {
		if err := pm.serviceManager.InitializeFrameworkService(ctx, svc); err != nil {
			log.Printf(
				"skipping service events for service '%s'; its framework could not be initialized: %v",
				svc.Name, err,
			)
			skipped = append(skipped, ServiceFrameworkInitFailure{Service: svc, Err: err})
			continue
		}

		initialized = append(initialized, svc)
	}

	return initialized, skipped, nil
}

func setProjectServiceTargets(services []*ServiceConfig) {
	serviceTargets := make([]string, 0, len(services))
	for _, svc := range services {
		serviceTargets = append(serviceTargets, string(svc.Host))
	}

	tracing.SetUsageAttributes(fields.ProjectServiceTargetsKey.StringSlice(serviceTargets))
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
	services []*ServiceConfig,
) error {
	var projectTools []tools.ExternalTool

	for _, svc := range services {
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
	services []*ServiceConfig,
) error {
	var requiredTools []tools.ExternalTool

	for _, svc := range services {
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

// svcToolInfo tracks whether a service's target required Docker.
type svcToolInfo struct {
	svc         *ServiceConfig
	needsDocker bool
}

func (pm *projectManager) EnsureServiceTargetTools(
	ctx context.Context,
	services []*ServiceConfig,
) error {
	var requiredTools []tools.ExternalTool

	var svcTools []svcToolInfo

	for _, svc := range services {
		serviceTarget, err := pm.serviceManager.GetServiceTarget(ctx, svc)
		if err != nil {
			return fmt.Errorf("getting service target: %w", err)
		}

		targetTools := serviceTarget.RequiredExternalTools(ctx, svc)
		requiredTools = append(requiredTools, targetTools...)

		needsDocker := false
		for _, tool := range targetTools {
			if tool.Name() == "Docker" {
				needsDocker = true
				break
			}
		}
		svcTools = append(svcTools, svcToolInfo{svc: svc, needsDocker: needsDocker})
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(requiredTools)...); err != nil {
		if toolErr, ok := errors.AsType[*tools.MissingToolErrors](err); ok {
			if suggestion := suggestRemoteBuild(svcTools, toolErr); suggestion != nil {
				return suggestion
			}
		}
		return err
	}

	return nil
}

// suggestRemoteBuild checks if Docker is in the missing tools list and whether any
// services that required it could use remote builds instead. Only services whose
// service target actually listed Docker as required are included in the suggestion.
func suggestRemoteBuild(
	svcTools []svcToolInfo,
	toolErr *tools.MissingToolErrors,
) *internal.ErrorWithSuggestion {
	if !slices.Contains(toolErr.ToolNames, "Docker") {
		return nil
	}

	// Find services that actually required Docker (per their service target)
	// and could use remoteBuild instead.
	var remoteBuildCapable []string
	for _, info := range svcTools {
		if !info.needsDocker {
			continue
		}
		remoteBuildCapable = append(remoteBuildCapable, info.svc.Name)
	}

	if len(remoteBuildCapable) == 0 {
		return nil
	}

	// Check whether the container runtime is not installed or just not running
	errMsg := toolErr.Error()
	isNotRunning := strings.Contains(errMsg, "is not running")

	serviceList := strings.Join(remoteBuildCapable, ", ")
	var suggestion string
	if isNotRunning {
		suggestion = fmt.Sprintf(
			"Services [%s] can build on Azure instead of locally.\n"+
				"Set 'remoteBuild: true' under the 'docker:' section for each service in azure.yaml,\n"+
				"or start your container runtime (Docker/Podman).",
			serviceList)
	} else {
		suggestion = fmt.Sprintf(
			"Services [%s] can build on Azure instead of locally.\n"+
				"Set 'remoteBuild: true' under the 'docker:' section for each service in azure.yaml,\n"+
				"or install Docker (https://aka.ms/azure-dev/docker-install)\n"+
				"or Podman (https://aka.ms/azure-dev/podman-install).",
			serviceList)
	}

	return &internal.ErrorWithSuggestion{
		Err:        toolErr,
		Suggestion: suggestion,
	}
}

func (pm *projectManager) EnsureRestoreTools(
	ctx context.Context,
	services []*ServiceConfig,
) error {
	var requiredTools []tools.ExternalTool

	for _, svc := range services {
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
