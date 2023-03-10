package project

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/kubectl"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/swa"
	"github.com/benbjohnson/clock"
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
	azdContext      *azdcontext.AzdContext
	env             *environment.Environment
	commandRunner   exec.CommandRunner
	azCli           azcli.AzCli
	console         input.Console
	accountManager  account.Manager
	resourceManager ResourceManager
}

func NewServiceManager(
	azdContext *azdcontext.AzdContext,
	env *environment.Environment,
	commandRunner exec.CommandRunner,
	azCli azcli.AzCli,
	console input.Console,
	accountManager account.Manager,
	resourceManager ResourceManager,
) ServiceManager {
	return &serviceManager{
		azdContext:      azdContext,
		env:             env,
		commandRunner:   commandRunner,
		azCli:           azCli,
		console:         console,
		accountManager:  accountManager,
		resourceManager: resourceManager,
	}
}

// Gets a list of required tools for the current service
func (sm *serviceManager) GetRequiredTools(ctx context.Context, serviceConfig *ServiceConfig) ([]tools.ExternalTool, error) {
	frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("getting framework services: %w", err)
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
	var err error

	switch serviceConfig.Host {
	case "", string(AppServiceTarget):
		target = NewAppServiceTarget(sm.env, sm.azCli)
	case string(ContainerAppTarget):
		// TODO: (azure/azure-dev#1657)
		// Using IoC container directly here is a work around till we can expose a
		// dynamic service location to resolve these configuration based dependencies
		var containerRegistryService azcli.ContainerRegistryService
		if err := ioc.Global.Resolve(&containerRegistryService); err != nil {
			return nil, err
		}

		target, err = NewContainerAppTarget(
			sm.env,
			containerRegistryService,
			sm.azCli,
			docker.NewDocker(sm.commandRunner),
			sm.console,
			sm.commandRunner,
			sm.accountManager,
			sm,
			sm.resourceManager,
		)
	case string(AksTarget):
		// TODO: (azure/azure-dev#1657)
		// Using IoC container directly here is a work around till we can expose a
		// dynamic service location to resolve these configuration based dependencies
		var managedClustersService azcli.ManagedClustersService
		if err := ioc.Global.Resolve(&managedClustersService); err != nil {
			return nil, err
		}

		var containerRegistryService azcli.ContainerRegistryService
		if err := ioc.Global.Resolve(&containerRegistryService); err != nil {
			return nil, err
		}

		target, err = NewAksTarget(
			sm.env,
			managedClustersService,
			containerRegistryService,
			kubectl.NewKubectl(sm.commandRunner),
			docker.NewDocker(sm.commandRunner),
			clock.New(),
		)
	case string(AzureFunctionTarget):
		target = NewFunctionAppTarget(sm.env, sm.azCli)
	case string(StaticWebAppTarget):
		target = NewStaticWebAppTarget(sm.env, sm.azCli, swa.NewSwaCli(sm.commandRunner))
	default:
		return nil, fmt.Errorf("unsupported host '%s' for service '%s'", serviceConfig.Host, serviceConfig.Name)
	}

	if err != nil {
		return nil, fmt.Errorf("failed validation for host '%s': %w", serviceConfig.Host, err)
	}

	return target, nil
}

// GetFrameworkService constructs a framework service from the underlying service configuration
func (sm *serviceManager) GetFrameworkService(ctx context.Context, serviceConfig *ServiceConfig) (FrameworkService, error) {
	var frameworkService FrameworkService

	switch serviceConfig.Language {
	case "", "dotnet", "csharp", "fsharp":
		frameworkService = NewDotNetProject(sm.commandRunner, sm.env)
	case "py", "python":
		frameworkService = NewPythonProject(sm.commandRunner, sm.env)
	case "js", "ts":
		frameworkService = NewNpmProject(sm.commandRunner, sm.env)
	case "java":
		frameworkService = NewMavenProject(sm.commandRunner, sm.env)
	default:
		return nil, fmt.Errorf("unsupported language '%s' for service '%s'", serviceConfig.Language, serviceConfig.Name)
	}

	// For containerized applications we use a nested framework service
	if serviceConfig.Host == string(ContainerAppTarget) || serviceConfig.Host == string(AksTarget) {
		sourceFramework := frameworkService
		frameworkService = NewDockerProject(serviceConfig, sm.env, docker.NewDocker(sm.commandRunner), sourceFramework)
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
