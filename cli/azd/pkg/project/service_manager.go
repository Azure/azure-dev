package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
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
}

type ServiceBuildResult struct {
	BuildOutputPath string
}

type ServicePackageResult struct {
	PackagePath string
}

type ServicePublishResult struct {
	// Related Azure resource ID
	TargetResourceId string            `json:"targetResourceId"`
	Kind             ServiceTargetKind `json:"kind"`
	Details          interface{}       `json:"details"`
	Endpoints        []string          `json:"endpoints"`
}

type ServiceDeployResult struct {
	*ServiceRestoreResult
	*ServiceBuildResult
	*ServicePackageResult
	*ServicePublishResult
}

type ServiceManager interface {
	GetRequiredTools(ctx context.Context, serviceConfig *ServiceConfig) ([]tools.ExternalTool, error)

	Restore(
		ctx context.Context,
		serviceConfig *ServiceConfig,
	) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress]
	Build(ctx context.Context, serviceConfig *ServiceConfig) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress]
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
	) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress]
	Publish(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		servicePackage ServicePackageResult,
	) *async.TaskWithProgress[*ServicePublishResult, ServiceProgress]
	Deploy(ctx context.Context, serviceConfig *ServiceConfig) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress]

	GetFrameworkService(ctx context.Context, serviceConfig *ServiceConfig) (FrameworkService, error)
	GetServiceTarget(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		resource *environment.TargetResource,
	) (ServiceTarget, error)
	GetServiceResources(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		resourceGroupName string,
	) ([]azcli.AzCliResource, error)
	GetServiceResource(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		resourceGroupName string,
		rerunCommand string,
	) (azcli.AzCliResource, error)
}

type serviceManager struct {
	azdContext     *azdcontext.AzdContext
	env            *environment.Environment
	commandRunner  exec.CommandRunner
	azCli          azcli.AzCli
	console        input.Console
	accountManager account.Manager
}

func NewServiceManager(
	azdContext *azdcontext.AzdContext,
	env *environment.Environment,
	commandRunner exec.CommandRunner,
	azCli azcli.AzCli,
	console input.Console,
	accountManager account.Manager,
) ServiceManager {
	return &serviceManager{
		azdContext:     azdContext,
		env:            env,
		commandRunner:  commandRunner,
		azCli:          azCli,
		console:        console,
		accountManager: accountManager,
	}
}

// Gets a list of required tools for the current service
func (sm *serviceManager) GetRequiredTools(ctx context.Context, serviceConfig *ServiceConfig) ([]tools.ExternalTool, error) {
	frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("getting framework services: %w", err)
	}

	targetResource, err := sm.getTargetResource(ctx, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("getting target resource: %w", err)
	}

	serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig, targetResource)
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
				return frameworkService.Build(ctx, serviceConfig)
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
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
		targetResource, err := sm.getTargetResource(ctx, serviceConfig)
		if err != nil {
			task.SetError(fmt.Errorf("getting target resource: %w", err))
			return
		}

		serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig, targetResource)
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
				return serviceTarget.Package(ctx, serviceConfig)
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
	servicePackage ServicePackageResult,
) *async.TaskWithProgress[*ServicePublishResult, ServiceProgress] {
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePublishResult, ServiceProgress]) {
		targetResource, err := sm.getTargetResource(ctx, serviceConfig)
		if err != nil {
			task.SetError(fmt.Errorf("getting target resource: %w", err))
			return
		}

		serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig, targetResource)
		if err != nil {
			task.SetError(fmt.Errorf("getting service target: %w", err))
			return
		}

		publishResult, err := runCommand(
			ctx,
			task,
			ServiceEventPublish,
			serviceConfig,
			func() *async.TaskWithProgress[*ServicePublishResult, ServiceProgress] {
				return serviceTarget.Publish(ctx, serviceConfig, servicePackage, targetResource)
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

			buildTask := sm.Build(ctx, serviceConfig)
			go syncProgress(task, buildTask.Progress())
			buildResult, err := buildTask.Await()
			if err != nil {
				return err
			}

			packageTask := sm.Package(ctx, serviceConfig)
			go syncProgress(task, packageTask.Progress())
			packageResult, err := packageTask.Await()
			if err != nil {
				return err
			}

			publishTask := sm.Publish(ctx, serviceConfig, *packageResult)
			go syncProgress(task, publishTask.Progress())
			publishResult, err := publishTask.Await()
			if err != nil {
				return err
			}

			result = &ServiceDeployResult{
				ServiceRestoreResult: restoreResult,
				ServiceBuildResult:   buildResult,
				ServicePackageResult: packageResult,
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
func (sm *serviceManager) GetServiceTarget(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	resource *environment.TargetResource,
) (ServiceTarget, error) {
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
	if serviceConfig.Host == string(ContainerAppTarget) {
		sourceFramework := frameworkService
		frameworkService = NewDockerProject(serviceConfig, sm.env, docker.NewDocker(sm.commandRunner), sourceFramework)
	}

	return frameworkService, nil
}

func (sm *serviceManager) getTargetResource(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) (*environment.TargetResource, error) {
	resourceGroupName, err := serviceConfig.Project.ResourceGroupName.Envsubst(sm.env.Getenv)
	if err != nil {
		return nil, err
	}

	azureResource, err := sm.resolveServiceResource(ctx, serviceConfig, resourceGroupName, "provision")
	if err != nil {
		return nil, err
	}

	return environment.NewTargetResource(
		sm.env.GetSubscriptionId(),
		resourceGroupName,
		azureResource.Name,
		azureResource.Type,
	), nil
}

// GetServiceResources finds azure service resources targeted by the service.
//
// If an explicit `ResourceName` is specified in `azure.yaml`, a resource with that name is searched for.
// Otherwise, searches for resources with 'azd-service-name' tag set to the service key.
func (sm *serviceManager) GetServiceResources(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	resourceGroupName string,
) ([]azcli.AzCliResource, error) {
	filter := fmt.Sprintf("tagName eq '%s' and tagValue eq '%s'", defaultServiceTag, serviceConfig.Name)

	subst, err := serviceConfig.ResourceName.Envsubst(sm.env.Getenv)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(subst) != "" {
		filter = fmt.Sprintf("name eq '%s'", subst)
	}

	return sm.azCli.ListResourceGroupResources(
		ctx,
		sm.env.GetSubscriptionId(),
		resourceGroupName,
		&azcli.ListResourceGroupResourcesOptions{
			Filter: &filter,
		},
	)
}

// GetServiceResources gets the specific azure service resource targeted by the service.
//
// rerunCommand specifies the command that users should rerun in case of misconfiguration.
// This is included in the error message if applicable
func (sm *serviceManager) GetServiceResource(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	resourceGroupName string,
	rerunCommand string,
) (azcli.AzCliResource, error) {
	expandedResourceName, err := serviceConfig.ResourceName.Envsubst(sm.env.Getenv)
	if err != nil {
		return azcli.AzCliResource{}, fmt.Errorf("expanding name: %w", err)
	}

	resources, err := sm.GetServiceResources(ctx, serviceConfig, resourceGroupName)
	if err != nil {
		return azcli.AzCliResource{}, fmt.Errorf("getting service resource: %w", err)
	}

	if expandedResourceName == "" { // A tag search was performed
		if len(resources) == 0 {
			err := fmt.Errorf(
				//nolint:lll
				"unable to find a resource tagged with '%s: %s'. Ensure the service resource is correctly tagged in your bicep files, and rerun %s",
				defaultServiceTag,
				serviceConfig.Name,
				rerunCommand,
			)
			return azcli.AzCliResource{}, azureutil.ResourceNotFound(err)
		}

		if len(resources) != 1 {
			return azcli.AzCliResource{}, fmt.Errorf(
				//nolint:lll
				"expecting only '1' resource tagged with '%s: %s', but found '%d'. Ensure a unique service resource is correctly tagged in your bicep files, and rerun %s",
				defaultServiceTag,
				serviceConfig.Name,
				len(resources),
				rerunCommand,
			)
		}
	} else { // Name based search
		if len(resources) == 0 {
			err := fmt.Errorf(
				"unable to find a resource with name '%s'. Ensure that resourceName in azure.yaml is valid, and rerun %s",
				expandedResourceName,
				rerunCommand)
			return azcli.AzCliResource{}, azureutil.ResourceNotFound(err)
		}

		// This can happen if multiple resources with different resource types are given the same name.
		if len(resources) != 1 {
			return azcli.AzCliResource{},
				fmt.Errorf(
					//nolint:lll
					"expecting only '1' resource named '%s', but found '%d'. Use a unique name for the service resource in the resource group '%s'",
					expandedResourceName,
					len(resources),
					resourceGroupName)
		}
	}

	return resources[0], nil
}

// resolveServiceResource resolves the service resource during service construction
func (sm *serviceManager) resolveServiceResource(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	resourceGroupName string,
	rerunCommand string,
) (azcli.AzCliResource, error) {
	azureResource, err := sm.GetServiceResource(ctx, serviceConfig, resourceGroupName, rerunCommand)

	// If the service target supports delayed provisioning, the resource isn't expected to be found yet.
	// Return the empty resource
	var resourceNotFoundError *azureutil.ResourceNotFoundError
	if err != nil &&
		errors.As(err, &resourceNotFoundError) &&
		ServiceTargetKind(serviceConfig.Host).SupportsDelayedProvisioning() {
		return azureResource, nil
	}

	if err != nil {
		return azcli.AzCliResource{}, err
	}

	return azureResource, nil
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
