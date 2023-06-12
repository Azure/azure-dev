package project

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// ShowProgress shows a progress message on the running spinner,
// by updating the title of the spinner.
// For example, "Deploying (<progress message>)".
// The message should be a single line.
type ShowProgress func(s string)

const (
	ServiceEventEnvUpdated ext.Event = "environment updated"
	ServiceEventRestore    ext.Event = "restore"
	ServiceEventBuild      ext.Event = "build"
	ServiceEventPackage    ext.Event = "package"
	ServiceEventDeploy     ext.Event = "deploy"
)

var (
	ServiceEvents []ext.Event = []ext.Event{
		ServiceEventEnvUpdated,
		ServiceEventRestore,
		ServiceEventPackage,
		ServiceEventDeploy,
	}
)

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

	// Sets the progress display function for the service manager
	SetProgressDisplay(progressDisplay ShowProgress)

	// Restores the code dependencies for the specified service config
	Restore(
		ctx context.Context,
		serviceConfig *ServiceConfig,
	) (ServiceRestoreResult, error)

	// Builds the code for the specified service config
	// Will call the language compile for compiled languages or
	// may copy build artifacts to a configured output folder
	Build(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		restoreOutput *ServiceRestoreResult,
	) (ServiceBuildResult, error)

	// Packages the code for the specified service config
	// Depending on the service configuration this will generate an artifact
	// that can be consumed by the hosting Azure service.
	// Common examples could be a zip archive for app service or
	// Docker images for container apps and AKS
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		buildOutput *ServiceBuildResult,
	) (ServicePackageResult, error)

	// Deploys the generated artifacts to the Azure resource that will
	// host the service application
	// Common examples would be uploading zip archive using ZipDeploy deployment or
	// pushing container images to a container registry.
	Deploy(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		packageOutput *ServicePackageResult,
	) (ServiceDeployResult, error)

	// Gets the framework service for the specified service config
	// The framework service performs the restoration and building of the service app code
	GetFrameworkService(ctx context.Context, serviceConfig *ServiceConfig) (FrameworkService, error)

	// Gets the service target service for the specified service config
	// The service target is responsible for packaging & deploying the service app code
	// to the destination Azure resource
	GetServiceTarget(ctx context.Context, serviceConfig *ServiceConfig) (ServiceTarget, error)
}

type serviceManager struct {
	env                 *environment.Environment
	resourceManager     ResourceManager
	serviceLocator      ioc.ServiceLocator
	operationCache      map[string]any
	alphaFeatureManager *alpha.FeatureManager
	console             input.Console
	progressDisplay     ShowProgress
}

// NewServiceManager creates a new instance of the ServiceManager component
func NewServiceManager(
	env *environment.Environment,
	resourceManager ResourceManager,
	serviceLocator ioc.ServiceLocator,
	alphaFeatureManager *alpha.FeatureManager,
	console input.Console,
) ServiceManager {
	return &serviceManager{
		env:                 env,
		resourceManager:     resourceManager,
		serviceLocator:      serviceLocator,
		operationCache:      map[string]any{},
		alphaFeatureManager: alphaFeatureManager,
		console:             console,
	}
}

func (sm *serviceManager) SetProgressDisplay(progressDisplay ShowProgress) {
	sm.progressDisplay = progressDisplay
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
	requiredTools = append(requiredTools, frameworkService.RequiredExternalTools(ctx)...)
	requiredTools = append(requiredTools, serviceTarget.RequiredExternalTools(ctx)...)

	return tools.Unique(requiredTools), nil
}

// Initializes the service configuration and dependent framework & service target
// This allows frameworks & service targets to hook into a services lifecycle events

func (sm *serviceManager) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	if serviceConfig.initialized {
		return nil
	}

	frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
	if err != nil {
		return fmt.Errorf("getting framework service: %w", err)
	}

	serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig)
	if err != nil {
		return fmt.Errorf("getting service target: %w", err)
	}

	if err := frameworkService.Initialize(ctx, serviceConfig); err != nil {
		return err
	}

	if err := serviceTarget.Initialize(ctx, serviceConfig); err != nil {
		return err
	}

	serviceConfig.initialized = true

	return nil
}

// Invokes the pre-service hooks for the event.
func (sm *serviceManager) pre(
	ctx context.Context,
	svc *ServiceConfig,
	event ext.Event) error {
	return svc.PreInvokeHook(ctx, event, ServiceLifecycleEventArgs{
		Project: svc.Project,
		Service: svc,
	})
}

// Invokes the post-service hooks for the event.
func (sm *serviceManager) post(
	ctx context.Context,
	svc *ServiceConfig,
	event ext.Event) error {
	return svc.PostInvokeHook(ctx, event, ServiceLifecycleEventArgs{
		Project: svc.Project,
		Service: svc,
	})
}

// Restores the code dependencies for the specified service config
func (sm *serviceManager) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) (ServiceRestoreResult, error) {
	cachedResult, ok := sm.getOperationResult(ctx, serviceConfig, string(ServiceEventRestore))
	if ok && cachedResult != nil {
		return cachedResult.(ServiceRestoreResult), nil
	}

	frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
	if err != nil {
		return ServiceRestoreResult{}, fmt.Errorf("getting framework services: %w", err)
	}

	if err := sm.pre(ctx, serviceConfig, ServiceEventRestore); err != nil {
		return ServiceRestoreResult{}, err
	}
	restoreResult, err := frameworkService.Restore(ctx, serviceConfig, sm.progressDisplay)
	if err != nil {
		return ServiceRestoreResult{}, fmt.Errorf("failed restoring service '%s': %w", serviceConfig.Name, err)
	}
	if err := sm.post(ctx, serviceConfig, ServiceEventRestore); err != nil {
		return ServiceRestoreResult{}, err
	}

	sm.setOperationResult(ctx, serviceConfig, string(ServiceEventRestore), restoreResult)
	return restoreResult, nil
}

// Builds the code for the specified service config
// Will call the language compile for compiled languages or may copy build artifacts to a configured output folder
func (sm *serviceManager) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
) (ServiceBuildResult, error) {
	cachedResult, ok := sm.getOperationResult(ctx, serviceConfig, string(ServiceEventBuild))
	if ok && cachedResult != nil {
		return cachedResult.(ServiceBuildResult), nil
	}

	if restoreOutput == nil {
		cachedResult, ok := sm.getOperationResult(ctx, serviceConfig, string(ServiceEventRestore))
		if ok && cachedResult != nil {
			restoreOutput = cachedResult.(*ServiceRestoreResult)
		}
	}

	frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
	if err != nil {
		return ServiceBuildResult{}, fmt.Errorf("getting framework services: %w", err)
	}

	if err := sm.pre(ctx, serviceConfig, ServiceEventBuild); err != nil {
		return ServiceBuildResult{}, err
	}
	res, err := frameworkService.Build(ctx, serviceConfig, restoreOutput, sm.progressDisplay)
	if err != nil {
		return ServiceBuildResult{}, fmt.Errorf("failed building service '%s': %w", serviceConfig.Name, err)
	}
	if err := sm.post(ctx, serviceConfig, ServiceEventBuild); err != nil {
		return ServiceBuildResult{}, err
	}

	sm.setOperationResult(ctx, serviceConfig, string(ServiceEventBuild), res)
	return res, nil
}

// Packages the code for the specified service config
// Depending on the service configuration this will generate an artifact that can be consumed by the hosting Azure service.
// Common examples could be a zip archive for app service or Docker images for container apps and AKS
func (sm *serviceManager) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) (ServicePackageResult, error) {
	cachedResult, ok := sm.getOperationResult(ctx, serviceConfig, string(ServiceEventPackage))
	if ok && cachedResult != nil {
		return cachedResult.(ServicePackageResult), nil
	}

	if buildOutput == nil {
		cachedResult, ok := sm.getOperationResult(ctx, serviceConfig, string(ServiceEventBuild))
		if ok && cachedResult != nil {
			res := cachedResult.(ServiceBuildResult)
			buildOutput = &res
		}
	}

	frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
	if err != nil {
		return ServicePackageResult{}, fmt.Errorf("getting framework service: %w", err)
	}

	serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig)
	if err != nil {
		return ServicePackageResult{}, fmt.Errorf("getting service target: %w", err)
	}

	hasBuildOutput := buildOutput != nil
	restoreResult := ServiceRestoreResult{}

	// Get the language / framework requirements
	frameworkRequirements := frameworkService.Requirements()

	// When a previous restore result was not provided, and we require it
	// Then we need to restore the dependencies
	if frameworkRequirements.Package.RequireRestore && (!hasBuildOutput || buildOutput.Restore == nil) {
		res, err := sm.Restore(ctx, serviceConfig)
		if err != nil {
			return ServicePackageResult{}, err
		}
		restoreResult = res
	}

	buildResult := ServiceBuildResult{}

	// When a previous build result was not provided, and we require it
	// Then we need to build the project
	if frameworkRequirements.Package.RequireBuild && !hasBuildOutput {
		res, err := sm.Build(ctx, serviceConfig, &restoreResult)
		if err != nil {
			return ServicePackageResult{}, err
		}

		buildResult = res
	}

	if !hasBuildOutput {
		buildOutput = &buildResult
		buildOutput.Restore = &restoreResult
	}

	if err := sm.pre(ctx, serviceConfig, ServiceEventPackage); err != nil {
		return ServicePackageResult{}, err
	}
	pkg, err := frameworkService.Package(ctx, serviceConfig, buildOutput, sm.progressDisplay)
	if err != nil {
		return ServicePackageResult{}, err
	}

	pkg, err = serviceTarget.Package(ctx, serviceConfig, &pkg, sm.progressDisplay)
	sm.setOperationResult(ctx, serviceConfig, string(ServiceEventPackage), pkg)

	if err := sm.post(ctx, serviceConfig, ServiceEventPackage); err != nil {
		return ServicePackageResult{}, err
	}

	if err != nil {
		return ServicePackageResult{}, fmt.Errorf("failed packaging service '%s': %w", serviceConfig.Name, err)
	}

	return pkg, nil
}

// Deploys the generated artifacts to the Azure resource that will host the service application
// Common examples would be uploading zip archive using ZipDeploy deployment or
// pushing container images to a container registry.
func (sm *serviceManager) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageResult *ServicePackageResult,
) (ServiceDeployResult, error) {
	cachedResult, ok := sm.getOperationResult(ctx, serviceConfig, string(ServiceEventDeploy))
	if ok && cachedResult != nil {
		return cachedResult.(ServiceDeployResult), nil
	}

	if packageResult == nil {
		cachedResult, ok := sm.getOperationResult(ctx, serviceConfig, string(ServiceEventPackage))
		if ok && cachedResult != nil {
			packageResult = cachedResult.(*ServicePackageResult)
		}
	}

	serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig)
	if err != nil {
		return ServiceDeployResult{}, fmt.Errorf("getting service target: %w", err)
	}

	targetResource, err := sm.resourceManager.GetTargetResource(ctx, sm.env.GetSubscriptionId(), serviceConfig)
	if err != nil {
		return ServiceDeployResult{}, fmt.Errorf("getting target resource: %w", err)
	}

	if err := sm.pre(ctx, serviceConfig, ServiceEventDeploy); err != nil {
		return ServiceDeployResult{}, err
	}
	deployResult, err := serviceTarget.Deploy(ctx, serviceConfig, packageResult, targetResource, sm.progressDisplay)
	if err != nil {
		return ServiceDeployResult{}, fmt.Errorf("failed deploying service '%s': %w", serviceConfig.Name, err)
	}
	if err := sm.post(ctx, serviceConfig, ServiceEventDeploy); err != nil {
		return ServiceDeployResult{}, err
	}

	// Allow users to specify their own endpoints, in cases where they've configured their own front-end load balancers,
	// reverse proxies or DNS host names outside of the service target (and prefer that to be used instead).
	overriddenEndpoints := sm.getOverriddenEndpoints(ctx, serviceConfig)
	if len(overriddenEndpoints) > 0 {
		deployResult.Endpoints = overriddenEndpoints
	}

	sm.setOperationResult(ctx, serviceConfig, string(ServiceEventDeploy), deployResult)
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
					" Run `%s` to enable the feature.",
				host,
				alpha.GetEnableCommand(alphaFeatureId),
			)
		}
	}

	if err := sm.serviceLocator.ResolveNamed(host, &target); err != nil {
		panic(fmt.Errorf(
			"failed to resolve service host '%s' for service '%s', %w",
			serviceConfig.Host,
			serviceConfig.Name,
			err,
		))
	}

	return target, nil
}

// GetFrameworkService constructs a framework service from the underlying service configuration
func (sm *serviceManager) GetFrameworkService(ctx context.Context, serviceConfig *ServiceConfig) (FrameworkService, error) {
	var frameworkService FrameworkService

	if err := sm.serviceLocator.ResolveNamed(string(serviceConfig.Language), &frameworkService); err != nil {
		panic(fmt.Errorf(
			"failed to resolve language '%s' for service '%s', %w",
			serviceConfig.Language,
			serviceConfig.Name,
			err,
		))
	}

	// For containerized applications we use a composite framework service
	if serviceConfig.Host == ContainerAppTarget || serviceConfig.Host == AksTarget {
		var compositeFramework CompositeFrameworkService
		if err := sm.serviceLocator.ResolveNamed(string(ServiceLanguageDocker), &compositeFramework); err != nil {
			panic(fmt.Errorf(
				"failed resolving composite framework service for '%s', language '%s': %w",
				serviceConfig.Name,
				serviceConfig.Language,
				err,
			))
		}

		compositeFramework.SetSource(frameworkService)

		frameworkService = compositeFramework
	}

	return frameworkService, nil
}

func (sm *serviceManager) getOverriddenEndpoints(ctx context.Context, serviceConfig *ServiceConfig) []string {
	overriddenEndpoints := sm.env.GetServiceProperty(serviceConfig.Name, "ENDPOINTS")
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
func (sm *serviceManager) getOperationResult(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	operationName string,
) (any, bool) {
	key := fmt.Sprintf("%s:%s", serviceConfig.Name, operationName)
	value, ok := sm.operationCache[key]

	return value, ok
}

// Sets the result of an operation in the cache
func (sm *serviceManager) setOperationResult(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	operationName string,
	result any,
) {
	key := fmt.Sprintf("%s:%s", serviceConfig.Name, operationName)
	sm.operationCache[key] = result
}
