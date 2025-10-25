// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/swa"
)

const (
	ServiceEventEnvUpdated ext.Event = "environment updated"
	ServiceEventRestore    ext.Event = "restore"
	ServiceEventBuild      ext.Event = "build"
	ServiceEventPackage    ext.Event = "package"
	ServiceEventPublish    ext.Event = "publish"
	ServiceEventDeploy     ext.Event = "deploy"
)

var (
	ServiceEvents []ext.Event = []ext.Event{
		ServiceEventEnvUpdated,
		ServiceEventRestore,
		ServiceEventBuild,
		ServiceEventPackage,
		ServiceEventPublish,
		ServiceEventDeploy,
	}
)

// UnsupportedServiceHostError represents an error when a service host is not supported,
// including the specific host name and service name for context
type UnsupportedServiceHostError struct {
	Host         string
	ServiceName  string
	ErrorMessage string
}

// Error implements the error interface
func (e *UnsupportedServiceHostError) Error() string {
	return fmt.Sprintf("service host '%s' for service '%s' is unsupported", e.Host, e.ServiceName)
}

// ServiceManager provides a management layer for performing operations against an azd service within a project
// The component performs all of the heavy lifting for executing all lifecycle operations for a service.
//
// All service lifecycle command leverage our async Task library to expose a common interface for handling
// long running operations including how we handle incremental progress updates and error handling.
type ServiceManager interface {
	// Gets all of the required framework/service target tools for the specified service config
	GetRequiredTools(ctx context.Context, serviceConfig *ServiceConfig) ([]tools.ExternalTool, error)

	// Initializes the service configuration and dependent framework & service target
	// This allows frameworks & service targets to hook into a services lifecycle events
	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error

	// Restores the code dependencies for the specified service config
	Restore(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress *async.Progress[ServiceProgress],
	) (*ServiceRestoreResult, error)

	// Builds the code for the specified service config
	// Will call the language compile for compiled languages or
	// may copy build artifacts to a configured output folder
	Build(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress *async.Progress[ServiceProgress],
	) (*ServiceBuildResult, error)

	// Packages the code for the specified service config
	// Depending on the service configuration this will generate an artifact
	// that can be consumed by the hosting Azure service.
	// Common examples could be a zip archive for app service or
	// Docker images for container apps and AKS
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress *async.Progress[ServiceProgress],
		options *PackageOptions,
	) (*ServicePackageResult, error)

	// Publishes the service to make it available to other services
	// A common example would be pushing container images to a container registry.
	Publish(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress *async.Progress[ServiceProgress],
		publishOptions *PublishOptions,
	) (*ServicePublishResult, error)

	// Deploys the generated artifacts to the Azure resource that will
	// host the service application
	// Common examples would be uploading zip archive using ZipDeploy deployment or
	// pushing container images to a container registry and creating a revision.
	Deploy(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress *async.Progress[ServiceProgress],
	) (*ServiceDeployResult, error)

	/// GetTargetResource finds and resolves the target Azure resource for the specified service configuration and host
	GetTargetResource(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceTarget ServiceTarget,
	) (*environment.TargetResource, error)

	// Gets the framework service for the specified service config
	// The framework service performs the restoration and building of the service app code
	GetFrameworkService(ctx context.Context, serviceConfig *ServiceConfig) (FrameworkService, error)

	// Gets the service target service for the specified service config
	// The service target is responsible for packaging & deploying the service app code
	// to the destination Azure resource
	GetServiceTarget(ctx context.Context, serviceConfig *ServiceConfig) (ServiceTarget, error)
}

// ServiceOperationCache is an alias to map used for internal caching of service operation results
// The ServiceManager is a scoped component since it depends on the current environment
// The ServiceOperationCache is used as a singleton cache for all service manager instances
type ServiceOperationCache map[string]any

type serviceManager struct {
	env                 *environment.Environment
	resourceManager     ResourceManager
	serviceLocator      ioc.ServiceLocator
	operationCache      ServiceOperationCache
	alphaFeatureManager *alpha.FeatureManager
	initialized         map[*ServiceConfig]map[any]bool
}

// NewServiceManager creates a new instance of the ServiceManager component
func NewServiceManager(
	env *environment.Environment,
	resourceManager ResourceManager,
	serviceLocator ioc.ServiceLocator,
	operationCache ServiceOperationCache,
	alphaFeatureManager *alpha.FeatureManager,
) ServiceManager {
	return &serviceManager{
		env:                 env,
		resourceManager:     resourceManager,
		serviceLocator:      serviceLocator,
		operationCache:      operationCache,
		alphaFeatureManager: alphaFeatureManager,
		initialized:         map[*ServiceConfig]map[any]bool{},
	}
}

// Gets all of the required framework/service target tools for the specified service config
func (sm *serviceManager) GetRequiredTools(ctx context.Context, serviceConfig *ServiceConfig) ([]tools.ExternalTool, error) {
	frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("getting framework service: %w", err)
	}

	serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("getting service target: %w", err)
	}

	requiredTools := []tools.ExternalTool{}
	requiredTools = append(requiredTools, frameworkService.RequiredExternalTools(ctx, serviceConfig)...)
	requiredTools = append(requiredTools, serviceTarget.RequiredExternalTools(ctx, serviceConfig)...)

	return tools.Unique(requiredTools), nil
}

// Initializes the service configuration and dependent framework & service target
// This allows frameworks & service targets to hook into a services lifecycle events
func (sm *serviceManager) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
	if err != nil {
		return fmt.Errorf("getting framework service: %w", err)
	}

	serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig)
	if err != nil {
		return fmt.Errorf("getting service target: %w", err)
	}

	if ok := sm.isComponentInitialized(serviceConfig, frameworkService); !ok {
		if err := frameworkService.Initialize(ctx, serviceConfig); err != nil {
			return err
		}

		sm.initialized[serviceConfig][frameworkService] = true
	} else {
		log.Printf("frameworkService already initialized for service: %s", serviceConfig.Name)
	}

	if ok := sm.isComponentInitialized(serviceConfig, serviceTarget); !ok {
		if err := serviceTarget.Initialize(ctx, serviceConfig); err != nil {
			return err
		}

		sm.initialized[serviceConfig][serviceTarget] = true
	}

	return nil
}

// Restores the code dependencies for the specified service config
func (sm *serviceManager) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	cachedResult, ok := sm.getOperationResult(serviceConfig, string(ServiceEventRestore))
	if ok && cachedResult != nil {
		return cachedResult.(*ServiceRestoreResult), nil
	}

	frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("getting framework services: %w", err)
	}

	if serviceContext == nil {
		serviceContext = NewServiceContext()
	}

	restoreResult, err := runCommand(
		ctx,
		ServiceEventRestore,
		serviceConfig,
		serviceContext,
		func() (*ServiceRestoreResult, error) {
			return frameworkService.Restore(ctx, serviceConfig, serviceContext, progress)
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed restoring service '%s': %w", serviceConfig.Name, err)
	}

	// Update service context with restore artifacts
	if err := serviceContext.Restore.Add(restoreResult.Artifacts...); err != nil {
		return nil, fmt.Errorf("failed to add restore artifacts: %w", err)
	}

	sm.setOperationResult(serviceConfig, string(ServiceEventRestore), restoreResult)
	return restoreResult, nil
}

// Builds the code for the specified service config
// Will call the language compile for compiled languages or may copy build artifacts to a configured output folder
func (sm *serviceManager) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	cachedResult, ok := sm.getOperationResult(serviceConfig, string(ServiceEventBuild))
	if ok && cachedResult != nil {
		return cachedResult.(*ServiceBuildResult), nil
	}

	frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("getting framework services: %w", err)
	}

	if serviceContext == nil {
		serviceContext = NewServiceContext()
	}

	buildResult, err := runCommand(
		ctx,
		ServiceEventBuild,
		serviceConfig,
		serviceContext,
		func() (*ServiceBuildResult, error) {
			return frameworkService.Build(ctx, serviceConfig, serviceContext, progress)
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed building service '%s': %w", serviceConfig.Name, err)
	}

	// Update service context with build artifacts
	if err := serviceContext.Build.Add(buildResult.Artifacts...); err != nil {
		return nil, fmt.Errorf("failed to add build artifacts: %w", err)
	}

	sm.setOperationResult(serviceConfig, string(ServiceEventBuild), buildResult)
	return buildResult, nil
}

// Packages the code for the specified service config
// Depending on the service configuration this will generate an artifact that can be consumed by the hosting Azure service.
// Common examples could be a zip archive for app service or Docker images for container apps and AKS
func (sm *serviceManager) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
	options *PackageOptions,
) (*ServicePackageResult, error) {
	cachedResult, ok := sm.getOperationResult(serviceConfig, string(ServiceEventPackage))
	if ok && cachedResult != nil {
		return cachedResult.(*ServicePackageResult), nil
	}

	if options == nil {
		options = &PackageOptions{}
	}

	frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("getting framework service: %w", err)
	}

	serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("getting service target: %w", err)
	}

	if serviceContext == nil {
		serviceContext = NewServiceContext()
	}

	// Get the language / framework requirements
	frameworkRequirements := frameworkService.Requirements()

	// Ensure restore has been performed if required
	if frameworkRequirements.Package.RequireRestore && len(serviceContext.Restore) == 0 {
		if _, err := sm.Restore(ctx, serviceConfig, serviceContext, progress); err != nil {
			return nil, err
		}
	}

	// Ensure build has been performed if required
	if frameworkRequirements.Package.RequireBuild && len(serviceContext.Build) == 0 {
		if _, err := sm.Build(ctx, serviceConfig, serviceContext, progress); err != nil {
			return nil, err
		}
	}

	packageResult, err := runCommand(
		ctx,
		ServiceEventPackage,
		serviceConfig,
		serviceContext,
		func() (*ServicePackageResult, error) {
			frameworkPackageResult, err := frameworkService.Package(ctx, serviceConfig, serviceContext, progress)
			if err != nil {
				return nil, err
			}

			if err := serviceContext.Package.Add(frameworkPackageResult.Artifacts...); err != nil {
				return nil, fmt.Errorf("failed to add framework package artifacts to service context: %w", err)
			}

			serviceTargetPackageResult, err := serviceTarget.Package(ctx, serviceConfig, serviceContext, progress)
			if err != nil {
				return nil, err
			}

			if err := serviceContext.Package.Add(serviceTargetPackageResult.Artifacts...); err != nil {
				return nil, fmt.Errorf("failed to add service target package artifacts to service context: %w", err)
			}

			packageResult := &ServicePackageResult{
				Artifacts: serviceContext.Package,
			}

			sm.setOperationResult(serviceConfig, string(ServiceEventPackage), packageResult)
			return packageResult, nil
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed packaging service '%s': %w", serviceConfig.Name, err)
	}

	// Package path can be a file path or a container image name
	// We only move to desired output path for file based packages
	if packageArtifact, has := serviceContext.Package.FindLast(); has {
		_, err = os.Stat(packageArtifact.Location)
		hasPackageFile := err == nil

		if hasPackageFile && options.OutputPath != "" {
			var destFilePath string
			var destDirectory string

			isFilePath := filepath.Ext(options.OutputPath) != ""
			if isFilePath {
				destFilePath = options.OutputPath
				destDirectory = filepath.Dir(options.OutputPath)
			} else {
				destFilePath = filepath.Join(options.OutputPath, filepath.Base(packageArtifact.Location))
				destDirectory = options.OutputPath
			}

			_, err := os.Stat(destDirectory)
			if errors.Is(err, os.ErrNotExist) {
				// Create the desired output directory if it does not already exist
				if err := os.MkdirAll(destDirectory, osutil.PermissionDirectory); err != nil {
					return nil, fmt.Errorf("failed creating output directory '%s': %w", destDirectory, err)
				}
			}

			// Move the package file to the desired path
			// We can't use os.Rename here since that does not work across disks
			if err := moveFile(packageArtifact.Location, destFilePath); err != nil {
				return nil, fmt.Errorf(
					"failed moving package file '%s' to '%s': %w", packageArtifact.Location, destFilePath, err)
			}

			packageArtifact.Location = destFilePath
		}
	}

	return packageResult, nil
}

// Publishes the service to make it available to other services
// Common examples would be pushing a message to a service bus or
// registering the service in a service registry.
func (sm *serviceManager) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
	publishOptions *PublishOptions,
) (*ServicePublishResult, error) {
	cachedResult, ok := sm.getOperationResult(serviceConfig, string(ServiceEventPublish))
	if ok && cachedResult != nil {
		return cachedResult.(*ServicePublishResult), nil
	}

	if serviceContext == nil {
		serviceContext = NewServiceContext()
	}

	// Ensure package has been performed if no package artifacts exist
	if len(serviceContext.Package) == 0 {
		if _, err := sm.Package(ctx, serviceConfig, serviceContext, progress, &PackageOptions{}); err != nil {
			return nil, err
		}
	}

	serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("getting service target: %w", err)
	}

	targetResource, err := sm.GetTargetResource(ctx, serviceConfig, serviceTarget)
	if err != nil {
		return nil, fmt.Errorf("getting target resource: %w", err)
	}

	publishResult, err := runCommand(
		ctx,
		ServiceEventPublish,
		serviceConfig,
		serviceContext,
		func() (*ServicePublishResult, error) {
			return serviceTarget.Publish(ctx, serviceConfig, serviceContext, targetResource, progress, publishOptions)
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed publishing service '%s': %w", serviceConfig.Name, err)
	}

	// Update service context with publish artifacts
	if err := serviceContext.Publish.Add(publishResult.Artifacts...); err != nil {
		return nil, fmt.Errorf("failed to add publish artifacts: %w", err)
	}

	sm.setOperationResult(serviceConfig, string(ServiceEventPublish), publishResult)
	return publishResult, nil
}

// Deploys the generated artifacts to the Azure resource that will host the service application
// Common examples would be uploading zip archive using ZipDeploy deployment or
// pushing container images to a container registry.
func (sm *serviceManager) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	cachedResult, ok := sm.getOperationResult(serviceConfig, string(ServiceEventDeploy))
	if ok && cachedResult != nil {
		return cachedResult.(*ServiceDeployResult), nil
	}

	if serviceContext == nil {
		serviceContext = NewServiceContext()
	}

	// Ensure package has been performed if no package artifacts exist
	if len(serviceContext.Package) == 0 {
		if _, err := sm.Package(ctx, serviceConfig, serviceContext, progress, &PackageOptions{}); err != nil {
			return nil, err
		}
	}

	// Ensure publish has been performed if no publish artifacts exist
	if len(serviceContext.Publish) == 0 {
		if _, err := sm.Publish(ctx, serviceConfig, serviceContext, progress, &PublishOptions{}); err != nil {
			return nil, err
		}
	}

	serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("getting service target: %w", err)
	}

	targetResource, err := sm.GetTargetResource(ctx, serviceConfig, serviceTarget)
	if err != nil {
		return nil, fmt.Errorf("getting target resource: %w", err)
	}

	deployResult, err := runCommand(
		ctx,
		ServiceEventDeploy,
		serviceConfig,
		serviceContext,
		func() (*ServiceDeployResult, error) {
			return serviceTarget.Deploy(ctx, serviceConfig, serviceContext, targetResource, progress)
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed deploying service '%s': %w", serviceConfig.Name, err)
	}

	// Update service context with deploy artifacts
	if err := serviceContext.Deploy.Add(deployResult.Artifacts...); err != nil {
		return nil, fmt.Errorf("failed to add deploy artifacts: %w", err)
	}

	// Allow users to specify their own endpoints, in cases where they've configured their own front-end load balancers,
	// reverse proxies or DNS host names outside of the service target (and prefer that to be used instead).
	overriddenEndpoints := OverriddenEndpoints(ctx, serviceConfig, sm.env)
	if len(overriddenEndpoints) > 0 {
		for _, endpoint := range overriddenEndpoints {
			if err := deployResult.Artifacts.Add(&Artifact{
				Kind:         ArtifactKindEndpoint,
				LocationKind: LocationKindRemote,
				Location:     endpoint,
				Metadata: map[string]string{
					"overridden": "true",
				},
			}); err != nil {
				return nil, fmt.Errorf("failed to add overridden endpoint artifact: %w", err)
			}
		}
	}

	sm.setOperationResult(serviceConfig, string(ServiceEventDeploy), deployResult)
	return deployResult, nil
}

// GetServiceTarget constructs a ServiceTarget from the underlying service configuration
func (sm *serviceManager) GetServiceTarget(ctx context.Context, serviceConfig *ServiceConfig) (ServiceTarget, error) {
	var target ServiceTarget
	host := string(serviceConfig.Host)

	if alphaFeatureId, isAlphaFeature := alpha.IsFeatureKey(host); isAlphaFeature {
		if !sm.alphaFeatureManager.IsEnabled(alphaFeatureId) {
			return nil, fmt.Errorf(
				"service host '%s' is currently in alpha and needs to be enabled explicitly."+
					" Run `%s` to enable the feature",
				host,
				alpha.GetEnableCommand(alphaFeatureId),
			)
		}
	}

	if err := sm.serviceLocator.ResolveNamed(host, &target); err != nil {
		if errors.Is(err, ioc.ErrResolveInstance) {
			unsupportedErr := &UnsupportedServiceHostError{
				Host:        host,
				ServiceName: serviceConfig.Name,
			}
			return nil, &internal.ErrorWithSuggestion{
				Err: unsupportedErr,
				Suggestion: fmt.Sprintf(
					"Suggestion: install an extension that provides this host or update azure.yaml "+
						"to use one of the supported hosts: %s",
					strings.Join(builtInServiceTargetNames(), ", "),
				),
			}
		}

		return nil, fmt.Errorf(
			"failed to resolve service host '%s' for service '%s', %w",
			serviceConfig.Host,
			serviceConfig.Name,
			err,
		)
	}

	return target, nil
}

// GetFrameworkService constructs a framework service from the underlying service configuration
func (sm *serviceManager) GetFrameworkService(ctx context.Context, serviceConfig *ServiceConfig) (FrameworkService, error) {
	var frameworkService FrameworkService

	// Publishing from an existing image currently follows the same lifecycle as a docker project
	if serviceConfig.Language == ServiceLanguageNone && !serviceConfig.Image.Empty() {
		serviceConfig.Language = ServiceLanguageDocker
	}

	log.Printf("Attempting to resolve language '%s' for service '%s'", serviceConfig.Language, serviceConfig.Name)
	if err := sm.serviceLocator.ResolveNamed(string(serviceConfig.Language), &frameworkService); err != nil {
		log.Printf("Failed to resolve language '%s' from IoC container: %v", serviceConfig.Language, err)
		// Try to resolve as external framework service from extensions
		if errors.Is(err, ioc.ErrResolveInstance) {
			// External framework services are not yet implemented
			return nil, fmt.Errorf(
				"language '%s' is not supported by built-in framework services and no extensions are currently providing it",
				serviceConfig.Language,
			)
		} else {
			return nil, fmt.Errorf(
				"failed to resolve language '%s' for service '%s', %w",
				serviceConfig.Language,
				serviceConfig.Name,
				err,
			)
		}
	} else {
		log.Printf("Successfully resolved language '%s' for service '%s'", serviceConfig.Language, serviceConfig.Name)
	}

	var compositeFramework CompositeFrameworkService
	// For hosts which run in containers, if the source project is not already a container, we need to wrap it in a docker
	// project that handles the containerization.
	requiresLanguage := serviceConfig.Language != ServiceLanguageDocker && serviceConfig.Language != ServiceLanguageNone
	if serviceConfig.Host.RequiresContainer() && requiresLanguage {
		if err := sm.serviceLocator.ResolveNamed(string(ServiceLanguageDocker), &compositeFramework); err != nil {
			return nil, fmt.Errorf(
				"failed resolving composite framework service for '%s', language '%s': %w",
				serviceConfig.Name,
				serviceConfig.Language,
				err,
			)
		}
	} else if serviceConfig.Host == StaticWebAppTarget {
		withSwaConfig, err := swa.ContainsSwaConfig(serviceConfig.Path())
		if err != nil {
			return nil, fmt.Errorf("checking for swa-cli.config.json: %w", err)
		}
		if withSwaConfig {
			if err := sm.serviceLocator.ResolveNamed(string(ServiceLanguageSwa), &compositeFramework); err != nil {
				return nil, fmt.Errorf(
					"failed resolving composite framework service for '%s', language '%s': %w",
					serviceConfig.Name,
					serviceConfig.Language,
					err,
				)
			}
			log.Println("Using swa-cli for build and deploy because swa-cli.config.json was found in the service path")
		}
	}
	if compositeFramework != nil {
		compositeFramework.SetSource(frameworkService)
		frameworkService = compositeFramework
	}

	return frameworkService, nil
}

func OverriddenEndpoints(ctx context.Context, serviceConfig *ServiceConfig, env *environment.Environment) []string {
	overriddenEndpoints := env.GetServiceProperty(serviceConfig.Name, "ENDPOINTS")
	if overriddenEndpoints != "" {
		var endpoints []string
		err := json.Unmarshal([]byte(overriddenEndpoints), &endpoints)
		if err != nil {
			// This can only happen if the environment output was not a valid JSON array, which would be due to an authoring
			// error. For typical infra provider output passthrough, the infra provider would guarantee well-formed syntax
			log.Printf(
				"failed to unmarshal endpoints override for service '%s' as JSON array of strings: %v, skipping override",
				serviceConfig.Name,
				err)
		}

		return endpoints
	}

	return nil
}

// Attempts to retrieve the result of a previous operation from the cache
func (sm *serviceManager) getOperationResult(serviceConfig *ServiceConfig, operationName string) (any, bool) {
	key := fmt.Sprintf("%s:%s:%s", sm.env.Name(), serviceConfig.Name, operationName)
	value, ok := sm.operationCache[key]

	return value, ok
}

// Sets the result of an operation in the cache
func (sm *serviceManager) setOperationResult(serviceConfig *ServiceConfig, operationName string, result any) {
	key := fmt.Sprintf("%s:%s:%s", sm.env.Name(), serviceConfig.Name, operationName)
	sm.operationCache[key] = result
}

// isComponentInitialized Checks if a component has been initialized for a service configuration
func (sm *serviceManager) isComponentInitialized(serviceConfig *ServiceConfig, component any) bool {
	if componentMap, has := sm.initialized[serviceConfig]; has && len(componentMap) > 0 {
		initialized := false
		if ok, has := componentMap[component]; has && ok {
			initialized = ok
		}

		return initialized
	}

	sm.initialized[serviceConfig] = map[any]bool{}

	return false
}

func runCommand[T any](
	ctx context.Context,
	eventName ext.Event,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	fn func() (T, error),
) (T, error) {
	if serviceContext == nil {
		serviceContext = NewServiceContext()
	}

	eventArgs := ServiceLifecycleEventArgs{
		Project:        serviceConfig.Project,
		Service:        serviceConfig,
		ServiceContext: serviceContext,
	}

	var result T

	err := serviceConfig.Invoke(ctx, eventName, eventArgs, func() error {
		res, err := fn()
		result = res
		return err
	})

	return result, err
}

// getTargetResourceForService resolves the target resource for a service configuration.
// For DotNetContainerAppTarget, it handles container app environment resolution.
// For other service types, it delegates to the resource manager.
type targetResourceResolver interface {
	ResolveTargetResource(
		ctx context.Context,
		subscriptionId string,
		serviceConfig *ServiceConfig,
		defaultResolver func() (*environment.TargetResource, error),
	) (*environment.TargetResource, error)
}

// / GetTargetResource finds and resolves the target Azure resource for the specified service configuration and host
func (sm *serviceManager) GetTargetResource(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceTarget ServiceTarget,
) (*environment.TargetResource, error) {
	if serviceTarget != nil {
		if resolver, ok := serviceTarget.(targetResourceResolver); ok {
			// Callback for computing the default target resource
			defaultResolver := func() (*environment.TargetResource, error) {
				return sm.resourceManager.GetTargetResource(ctx, sm.env.GetSubscriptionId(), serviceConfig)
			}

			resource, err := resolver.ResolveTargetResource(
				ctx,
				sm.env.GetSubscriptionId(),
				serviceConfig,
				defaultResolver,
			)
			if err != nil {
				return nil, fmt.Errorf("resolving target resource via external service target: %w", err)
			}

			return resource, nil
		}
	}

	if serviceConfig.Host == DotNetContainerAppTarget {
		containerEnvName := sm.env.GetServiceProperty(serviceConfig.Name, "CONTAINER_ENVIRONMENT_NAME")
		// AZURE_CONTAINER_APPS_ENVIRONMENT_ID is not required for Aspire (serviceConfig.DotNetContainerApp != nil)
		// because it uses a bicep deployment.
		if containerEnvName == "" && serviceConfig.DotNetContainerApp == nil {
			containerEnvName = sm.env.Getenv("AZURE_CONTAINER_APPS_ENVIRONMENT_ID")
			if containerEnvName == "" {
				return nil, fmt.Errorf(
					"could not determine container app environment for service %s, "+
						"have you set AZURE_CONTAINER_ENVIRONMENT_NAME or "+
						"SERVICE_%s_CONTAINER_ENVIRONMENT_NAME as an output of your "+
						"infrastructure?", serviceConfig.Name, strings.ToUpper(serviceConfig.Name))
			}

			parts := strings.Split(containerEnvName, "/")
			containerEnvName = parts[len(parts)-1]
		}

		// Get any explicitly configured resource group name
		// 1. Service level override
		// 2. Project level override
		resourceGroupNameTemplate := serviceConfig.ResourceGroupName
		if resourceGroupNameTemplate.Empty() {
			resourceGroupNameTemplate = serviceConfig.Project.ResourceGroupName
		}

		resourceGroupName, err := sm.resourceManager.GetResourceGroupName(
			ctx,
			sm.env.GetSubscriptionId(),
			resourceGroupNameTemplate,
		)
		if err != nil {
			return nil, fmt.Errorf("getting resource group name: %w", err)
		}

		return environment.NewTargetResource(
			sm.env.GetSubscriptionId(),
			resourceGroupName,
			containerEnvName,
			string(azapi.AzureResourceTypeContainerAppEnvironment),
		), nil
	}

	return sm.resourceManager.GetTargetResource(ctx, sm.env.GetSubscriptionId(), serviceConfig)
}

// Copies a file from the source path to the destination path
// Deletes the source file after the copy is complete
func moveFile(sourcePath string, destinationPath string) error {
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("opening source file: %w", err)
	}
	defer sourceFile.Close()

	// Create or truncate the destination file
	destinationFile, err := os.Create(destinationPath)
	if err != nil {
		return fmt.Errorf("creating destination file: %w", err)
	}
	defer destinationFile.Close()

	// Copy the contents of the source file to the destination file
	_, err = io.Copy(destinationFile, sourceFile)
	if err != nil {
		return fmt.Errorf("copying file: %w", err)
	}

	// Remove the source file (optional)
	defer os.Remove(sourcePath)

	return nil
}
