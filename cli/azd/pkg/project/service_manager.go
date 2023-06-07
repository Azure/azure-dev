package project

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

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

	// Restores the code dependencies for the specified service config
	Restore(
		ctx context.Context,
		serviceConfig *ServiceConfig,
	) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress]

	// Builds the code for the specified service config
	// Will call the language compile for compiled languages or
	// may copy build artifacts to a configured output folder
	Build(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		restoreOutput *ServiceRestoreResult,
	) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress]

	// Packages the code for the specified service config
	// Depending on the service configuration this will generate an artifact
	// that can be consumed by the hosting Azure service.
	// Common examples could be a zip archive for app service or
	// Docker images for container apps and AKS
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		buildOutput *ServiceBuildResult,
	) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress]

	// Deploys the generated artifacts to the Azure resource that will
	// host the service application
	// Common examples would be uploading zip archive using ZipDeploy deployment or
	// pushing container images to a container registry.
	Deploy(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		packageOutput *ServicePackageResult,
	) (*ServiceDeployResult, error)

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
}

// NewServiceManager creates a new instance of the ServiceManager component
func NewServiceManager(
	env *environment.Environment,
	resourceManager ResourceManager,
	serviceLocator ioc.ServiceLocator,
	alphaFeatureManager *alpha.FeatureManager,
) ServiceManager {
	return &serviceManager{
		env:                 env,
		resourceManager:     resourceManager,
		serviceLocator:      serviceLocator,
		operationCache:      map[string]any{},
		alphaFeatureManager: alphaFeatureManager,
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

// Restores the code dependencies for the specified service config
func (sm *serviceManager) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServiceRestoreResult, ServiceProgress]) {
		cachedResult, ok := sm.getOperationResult(ctx, serviceConfig, string(ServiceEventRestore))
		if ok && cachedResult != nil {
			task.SetResult(cachedResult.(*ServiceRestoreResult))
			return
		}

		frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
		if err != nil {
			task.SetError(fmt.Errorf("getting framework services: %w", err))
			return
		}

		restoreResult, err := runCommand(
			ctx,
			task,
			ServiceEventRestore,
			serviceConfig,
			func() *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
				return frameworkService.Restore(ctx, serviceConfig)
			},
		)

		if err != nil {
			task.SetError(fmt.Errorf("failed restoring service '%s': %w", serviceConfig.Name, err))
			return
		}

		task.SetResult(restoreResult)
		sm.setOperationResult(ctx, serviceConfig, string(ServiceEventRestore), restoreResult)
	})
}

// Builds the code for the specified service config
// Will call the language compile for compiled languages or may copy build artifacts to a configured output folder
func (sm *serviceManager) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {
		cachedResult, ok := sm.getOperationResult(ctx, serviceConfig, string(ServiceEventBuild))
		if ok && cachedResult != nil {
			task.SetResult(cachedResult.(*ServiceBuildResult))
			return
		}

		if restoreOutput == nil {
			cachedResult, ok := sm.getOperationResult(ctx, serviceConfig, string(ServiceEventRestore))
			if ok && cachedResult != nil {
				restoreOutput = cachedResult.(*ServiceRestoreResult)
			}
		}

		frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
		if err != nil {
			task.SetError(fmt.Errorf("getting framework services: %w", err))
			return
		}

		buildResult, err := runCommand(
			ctx,
			task,
			ServiceEventBuild,
			serviceConfig,
			func() *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
				return frameworkService.Build(ctx, serviceConfig, restoreOutput)
			},
		)

		if err != nil {
			task.SetError(fmt.Errorf("failed building service '%s': %w", serviceConfig.Name, err))
			return
		}

		task.SetResult(buildResult)
		sm.setOperationResult(ctx, serviceConfig, string(ServiceEventBuild), buildResult)
	})
}

// Packages the code for the specified service config
// Depending on the service configuration this will generate an artifact that can be consumed by the hosting Azure service.
// Common examples could be a zip archive for app service or Docker images for container apps and AKS
func (sm *serviceManager) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
		cachedResult, ok := sm.getOperationResult(ctx, serviceConfig, string(ServiceEventPackage))
		if ok && cachedResult != nil {
			task.SetResult(cachedResult.(*ServicePackageResult))
			return
		}

		if buildOutput == nil {
			cachedResult, ok := sm.getOperationResult(ctx, serviceConfig, string(ServiceEventBuild))
			if ok && cachedResult != nil {
				buildOutput = cachedResult.(*ServiceBuildResult)
			}
		}

		frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
		if err != nil {
			task.SetError(fmt.Errorf("getting framework service: %w", err))
			return
		}

		serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig)
		if err != nil {
			task.SetError(fmt.Errorf("getting service target: %w", err))
			return
		}

		eventArgs := ServiceLifecycleEventArgs{
			Project: serviceConfig.Project,
			Service: serviceConfig,
		}

		hasBuildOutput := buildOutput != nil
		restoreResult := &ServiceRestoreResult{}

		// Get the language / framework requirements
		frameworkRequirements := frameworkService.Requirements()

		// When a previous restore result was not provided, and we require it
		// Then we need to restore the dependencies
		if frameworkRequirements.Package.RequireRestore && (!hasBuildOutput || buildOutput.Restore == nil) {
			restoreTask := sm.Restore(ctx, serviceConfig)
			syncProgress(task, restoreTask.Progress())

			restoreTaskResult, err := restoreTask.Await()
			if err != nil {
				task.SetError(err)
				return
			}

			restoreResult = restoreTaskResult
		}

		buildResult := &ServiceBuildResult{}

		// When a previous build result was not provided, and we require it
		// Then we need to build the project
		if frameworkRequirements.Package.RequireBuild && !hasBuildOutput {
			buildTask := sm.Build(ctx, serviceConfig, restoreResult)
			syncProgress(task, buildTask.Progress())

			buildTaskResult, err := buildTask.Await()
			if err != nil {
				task.SetError(err)
				return
			}

			buildResult = buildTaskResult
		}

		if !hasBuildOutput {
			buildOutput = buildResult
			buildOutput.Restore = restoreResult
		}

		var packageResult *ServicePackageResult

		err = serviceConfig.Invoke(ctx, ServiceEventPackage, eventArgs, func() error {
			frameworkPackageTask := frameworkService.Package(ctx, serviceConfig, buildOutput)
			syncProgress(task, frameworkPackageTask.Progress())

			frameworkPackageResult, err := frameworkPackageTask.Await()
			if err != nil {
				return err
			}

			serviceTargetPackageTask := serviceTarget.Package(ctx, serviceConfig, frameworkPackageResult)
			syncProgress(task, serviceTargetPackageTask.Progress())

			serviceTargetPackageResult, err := serviceTargetPackageTask.Await()
			if err != nil {
				return err
			}

			packageResult = serviceTargetPackageResult
			sm.setOperationResult(ctx, serviceConfig, string(ServiceEventPackage), packageResult)

			return nil
		})

		if err != nil {
			task.SetError(fmt.Errorf("failed packaging service '%s': %w", serviceConfig.Name, err))
			return
		}

		task.SetResult(packageResult)
	})
}

// Deploys the generated artifacts to the Azure resource that will host the service application
// Common examples would be uploading zip archive using ZipDeploy deployment or
// pushing container images to a container registry.
func (sm *serviceManager) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageResult *ServicePackageResult,
) (*ServiceDeployResult, error) {
	cachedResult, ok := sm.getOperationResult(ctx, serviceConfig, string(ServiceEventDeploy))
	if ok && cachedResult != nil {
		return cachedResult.(*ServiceDeployResult), nil
	}

	if packageResult == nil {
		cachedResult, ok := sm.getOperationResult(ctx, serviceConfig, string(ServiceEventPackage))
		if ok && cachedResult != nil {
			packageResult = cachedResult.(*ServicePackageResult)
		}
	}

	serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("getting service target: %w", err)
	}

	targetResource, err := sm.resourceManager.GetTargetResource(ctx, sm.env.GetSubscriptionId(), serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("getting target resource: %w", err)
	}

	deployResult, err := runCommandWithEvent(
		ctx,
		ServiceEventDeploy,
		serviceConfig,
		func() *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress] {
			return serviceTarget.Deploy(ctx, serviceConfig, packageResult, targetResource)
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed deploying service '%s': %w", serviceConfig.Name, err)
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

func runCommandWithEvent[T comparable, P comparable](
	ctx context.Context,
	eventName ext.Event,
	serviceConfig *ServiceConfig,
	taskFunc func() *async.TaskWithProgress[T, P],
) (T, error) {
	eventArgs := ServiceLifecycleEventArgs{
		Project: serviceConfig.Project,
		Service: serviceConfig,
	}

	var result T

	err := serviceConfig.Invoke(ctx, eventName, eventArgs, func() error {
		serviceTask := taskFunc()
		go flushProgress(serviceTask.Progress())

		taskResult, err := serviceTask.Await()
		if err != nil {
			return err
		}

		result = taskResult
		return nil
	})

	if err != nil {
		return result, err
	}

	return result, nil
}

func runCommand[T comparable, P comparable](
	ctx context.Context,
	task *async.TaskContextWithProgress[T, P],
	eventName ext.Event,
	serviceConfig *ServiceConfig,
	taskFunc func() *async.TaskWithProgress[T, P],
) (T, error) {
	eventArgs := ServiceLifecycleEventArgs{
		Project: serviceConfig.Project,
		Service: serviceConfig,
	}

	var result T

	err := serviceConfig.Invoke(ctx, eventName, eventArgs, func() error {
		serviceTask := taskFunc()
		go syncProgress(task, serviceTask.Progress())

		taskResult, err := serviceTask.Await()
		if err != nil {
			return err
		}

		result = taskResult
		return nil
	})

	if err != nil {
		return result, err
	}

	return result, nil
}

func syncProgress[T comparable, P comparable](task *async.TaskContextWithProgress[T, P], progressChannel <-chan P) {
	for progress := range progressChannel {
		task.SetProgress(progress)
	}
}

func flushProgress[P comparable](progressChannel <-chan P) {
	for progress := range progressChannel {
		log.Println(progress)
	}
}
