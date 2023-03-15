package project

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

const (
	defaultServiceTag = "azd-service-name"

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
		ServiceEventPackage,
		ServiceEventDeploy,
	}
)

type ServiceDeploymentChannelResponse struct {
	// The result of a service deploy operation
	Result *ServicePublishResult
	// The error that may have occurred during a deploy operation
	Error error
}
type ServiceLifecycleEventArgs struct {
	Project *ProjectConfig
	Service *ServiceConfig
	Args    map[string]any
}

type ServiceProgress struct {
	Message   string
	Timestamp time.Time
}

func NewServiceProgress(message string) ServiceProgress {
	return ServiceProgress{
		Message:   message,
		Timestamp: time.Now(),
	}
}

type ServiceRestoreResult struct {
	Details interface{} `json:"details"`
}

type ServiceBuildResult struct {
	Restore         *ServiceRestoreResult `json:"restore"`
	BuildOutputPath string                `json:"buildOutputPath"`
	Details         interface{}           `json:"details"`
}

type ServicePackageResult struct {
	Build       *ServiceBuildResult `json:"package"`
	Details     interface{}         `json:"details"`
	PackagePath string              `json:"packagePath"`
}

type ServicePublishResult struct {
	Package *ServicePackageResult
	// Related Azure resource ID
	TargetResourceId string            `json:"targetResourceId"`
	Kind             ServiceTargetKind `json:"kind"`
	Details          interface{}       `json:"details"`
	Endpoints        []string          `json:"endpoints"`
}

type ServiceDeployResult struct {
	*ServicePublishResult
}

type ServiceManager interface {
	GetRequiredTools(ctx context.Context, serviceConfig *ServiceConfig) ([]tools.ExternalTool, error)

	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error
	Restore(
		ctx context.Context,
		serviceConfig *ServiceConfig,
	) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress]
	Build(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		restoreOutput *ServiceRestoreResult,
	) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress]
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		buildOutput *ServiceBuildResult,
	) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress]
	Publish(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		packageOutput *ServicePackageResult,
	) *async.TaskWithProgress[*ServicePublishResult, ServiceProgress]
	Deploy(
		ctx context.Context,
		serviceConfig *ServiceConfig,
	) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress]

	GetFrameworkService(ctx context.Context, serviceConfig *ServiceConfig) (FrameworkService, error)
	GetServiceTarget(ctx context.Context, serviceConfig *ServiceConfig) (ServiceTarget, error)
}

type serviceManager struct {
	env             *environment.Environment
	resourceManager ResourceManager
	serviceLocator  ioc.ServiceLocator
}

func NewServiceManager(
	env *environment.Environment,
	resourceManager ResourceManager,
	serviceLocator ioc.ServiceLocator,
) ServiceManager {
	return &serviceManager{
		env:             env,
		resourceManager: resourceManager,
		serviceLocator:  serviceLocator,
	}
}

// Gets a list of required tools for the current service
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

	return requiredTools, nil
}

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

func (sm *serviceManager) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServiceRestoreResult, ServiceProgress]) {
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
	})
}

func (sm *serviceManager) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {
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
	})
}

func (sm *serviceManager) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
		serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig)
		if err != nil {
			task.SetError(fmt.Errorf("getting service target: %w", err))
			return
		}

		packageResult, err := runCommand(
			ctx,
			task,
			ServiceEventPackage,
			serviceConfig,
			func() *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
				return serviceTarget.Package(ctx, serviceConfig, buildOutput)
			},
		)

		if err != nil {
			task.SetError(fmt.Errorf("failed packaging service '%s': %w", serviceConfig.Name, err))
			return
		}

		task.SetResult(packageResult)
	})
}

func (sm *serviceManager) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageResult *ServicePackageResult,
) *async.TaskWithProgress[*ServicePublishResult, ServiceProgress] {
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePublishResult, ServiceProgress]) {
		serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig)
		if err != nil {
			task.SetError(fmt.Errorf("getting service target: %w", err))
			return
		}

		targetResource, err := sm.resourceManager.GetTargetResource(ctx, serviceConfig)
		if err != nil {
			task.SetError(fmt.Errorf("getting target resource: %w", err))
			return
		}

		publishResult, err := runCommand(
			ctx,
			task,
			ServiceEventPublish,
			serviceConfig,
			func() *async.TaskWithProgress[*ServicePublishResult, ServiceProgress] {
				return serviceTarget.Publish(ctx, serviceConfig, packageResult, targetResource)
			},
		)

		if err != nil {
			task.SetError(fmt.Errorf("failed publishing service '%s': %w", serviceConfig.Name, err))
			return
		}

		// Allow users to specify their own endpoints, in cases where they've configured their own front-end load balancers,
		// reverse proxies or DNS host names outside of the service target (and prefer that to be used instead).
		overriddenEndpoints := sm.getOverriddenEndpoints(ctx, serviceConfig)
		if len(overriddenEndpoints) > 0 {
			publishResult.Endpoints = overriddenEndpoints
		}

		task.SetResult(publishResult)
	})
}

func (sm *serviceManager) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress] {
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress]) {
		var result *ServiceDeployResult

		serviceEventArgs := ServiceLifecycleEventArgs{
			Project: serviceConfig.Project,
			Service: serviceConfig,
		}

		err := serviceConfig.Invoke(ctx, ServiceEventDeploy, serviceEventArgs, func() error {
			restoreTask := sm.Restore(ctx, serviceConfig)
			go syncProgress(task, restoreTask.Progress())
			restoreResult, err := restoreTask.Await()
			if err != nil {
				return err
			}

			buildTask := sm.Build(ctx, serviceConfig, restoreResult)
			go syncProgress(task, buildTask.Progress())
			buildResult, err := buildTask.Await()
			if err != nil {
				return err
			}

			packageTask := sm.Package(ctx, serviceConfig, buildResult)
			go syncProgress(task, packageTask.Progress())
			packageResult, err := packageTask.Await()
			if err != nil {
				return err
			}

			publishTask := sm.Publish(ctx, serviceConfig, packageResult)
			go syncProgress(task, publishTask.Progress())
			publishResult, err := publishTask.Await()
			if err != nil {
				return err
			}

			result = &ServiceDeployResult{
				ServicePublishResult: publishResult,
			}

			return nil
		})

		if err != nil {
			task.SetError(fmt.Errorf("failed deploying service '%s': %w", serviceConfig.Name, err))
			return
		}

		task.SetProgress(NewServiceProgress("Deployment completed"))
		task.SetResult(result)
	})
}

// GetServiceTarget constructs a ServiceTarget from the underlying service configuration
func (sm *serviceManager) GetServiceTarget(ctx context.Context, serviceConfig *ServiceConfig) (ServiceTarget, error) {
	var target ServiceTarget

	// If we fail resolving the service target here this is most likely due the user
	// specifying a host value outside the bounds of our supported values.
	if err := sm.serviceLocator.ResolveNamed(serviceConfig.Host, &target); err != nil {
		return nil, fmt.Errorf(
			"unsupported service host '%s' for service '%s', %w",
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

	// If we fail resolving the service target here this is most likely due the user
	// specifying a language value outside the bounds of our supported values.
	if err := sm.serviceLocator.ResolveNamed(serviceConfig.Language, &frameworkService); err != nil {
		return nil, fmt.Errorf(
			"unsupported language '%s' for service '%s', %w",
			serviceConfig.Language,
			serviceConfig.Name,
			err,
		)
	}

	// For containerized applications we use a composite framework service
	if serviceConfig.Host == string(ContainerAppTarget) || serviceConfig.Host == string(AksTarget) {
		var compositeFramework CompositeFrameworkService
		if err := sm.serviceLocator.ResolveNamed(string(ServiceLanguageDocker), &compositeFramework); err != nil {
			panic(fmt.Errorf(
				"failed resolving composite framework service for '%s', language '%s': %w",
				serviceConfig.Name,
				serviceConfig.Language,
				err,
			))
		}

		if err := compositeFramework.SetSource(ctx, frameworkService); err != nil {
			return nil, fmt.Errorf("failed setting source framework service")
		}

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
