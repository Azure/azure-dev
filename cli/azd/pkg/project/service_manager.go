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
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/swa"
)

const (
	defaultServiceTag = "azd-service-name"
)

type ServiceDeploymentChannelResponse struct {
	// The result of a service deploy operation
	Result *ServiceDeploymentResult
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
	OutputPath string
}

type ServicePackageResult struct {
	PackagePath string
}

type ServicePublishResult struct {
}

type ServiceDeployResult struct {
}

type ServiceManager interface {
	GetRequiredTools(ctx context.Context, serviceConfig *ServiceConfig) ([]tools.ExternalTool, error)

	Restore(ctx context.Context, serviceConfig *ServiceConfig) (*ServiceRestoreResult, error)
	Build(ctx context.Context, serviceConfig *ServiceConfig) (*ServiceBuildResult, error)
	Package(ctx context.Context, serviceConfig *ServiceConfig) (*ServicePackageResult, error)
	Publish(ctx context.Context, serviceConfig *ServiceConfig) (*ServicePublishResult, error)
	Deploy(ctx context.Context, serviceConfig *ServiceConfig) (*ServiceDeployResult, error)

	GetFrameworkService(ctx context.Context, serviceConfig *ServiceConfig) (FrameworkService, error)
	GetServiceTarget(ctx context.Context, serviceConfig *ServiceConfig, resource *environment.TargetResource) (ServiceTarget, error)
	GetServiceResources(ctx context.Context, serviceConfig *ServiceConfig, resourceGroupName string) ([]azcli.AzCliResource, error)
	GetServiceResource(ctx context.Context, serviceConfig *ServiceConfig, resourceGroupName string, rerunCommand string) (azcli.AzCliResource, error)
}

type serviceManager struct {
	*ext.EventDispatcher[ServiceLifecycleEventArgs]

	azdContext     *azdcontext.AzdContext
	env            *environment.Environment
	commandRunner  exec.CommandRunner
	azCli          azcli.AzCli
	console        input.Console
	accountManager account.Manager
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
	requiredTools = append(requiredTools, frameworkService.RequiredExternalTools()...)
	requiredTools = append(requiredTools, serviceTarget.RequiredExternalTools()...)

	return requiredTools, nil
}

func (sm *serviceManager) Restore(ctx context.Context, serviceConfig *ServiceConfig) (*ServiceRestoreResult, error) {
	eventArgs := ServiceLifecycleEventArgs{
		Project: serviceConfig.Project,
		Service: serviceConfig,
	}

	err := sm.Invoke(ctx, ServiceEventRestore, eventArgs, func() error {
		frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
		if err != nil {
			return fmt.Errorf("getting framework services: %w", err)
		}

		if err := frameworkService.Restore(ctx); err != nil {
			return fmt.Errorf("failed installing dependencies, %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return &ServiceRestoreResult{}, nil
}

func (sm *serviceManager) Build(ctx context.Context, serviceConfig *ServiceConfig) (*ServiceBuildResult, error) {
	// frameworkService, err := sm.GetFrameworkService(ctx, serviceConfig)
	// if err != nil {
	// 	return nil, err
	// }

	// _, err := frameworkService.Build(ctx)
	// if err != nil {
	// 	return nil, err
	// }

	return &ServiceBuildResult{}, nil
}

func (sm *serviceManager) Package(ctx context.Context, serviceConfig *ServiceConfig) (*ServicePackageResult, error) {
	targetResource, err := sm.getTargetResource(ctx, serviceConfig)
	if err != nil {
		return nil, err
	}

	serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig, targetResource)
	if err != nil {
		return nil, err
	}

	if err := serviceTarget.Package(ctx); err != nil {
		return nil, err
	}

	return &ServicePackageResult{}, nil
}

func (sm *serviceManager) Publish(ctx context.Context, serviceConfig *ServiceConfig) (*ServicePublishResult, error) {
	// targetResource, err := sm.getTargetResource(ctx, serviceConfig)
	// if err != nil {
	// 	return nil, err
	// }

	// serviceTarget, err := sm.GetServiceTarget(ctx, serviceConfig, targetResource)
	// if err != nil {
	// 	return nil, err
	// }

	//return serviceTarget.Publish(ctx)
	return &ServicePublishResult{}, nil
}

func (sm *serviceManager) Deploy(ctx context.Context, serviceConfig *ServiceConfig) (*ServiceDeployResult, error) {
	return nil, nil
}

func (sm *serviceManager) DeployLegacy(ctx context.Context, serviceConfig *ServiceConfig) (<-chan *ServiceDeploymentChannelResponse, <-chan string) {
	result := make(chan *ServiceDeploymentChannelResponse, 1)
	progress := make(chan string)

	serviceEventArgs := ServiceLifecycleEventArgs{
		Project: serviceConfig.Project,
		Service: serviceConfig,
	}

	go func() {
		defer close(result)
		defer close(progress)

		//var deploymentArtifact string

		err := sm.Invoke(ctx, ServiceEventPackage, serviceEventArgs, func() error {
			log.Printf("packing service %s", serviceConfig.Name)

			progress <- "Preparing packaging"
			_, err := sm.Build(ctx, serviceConfig)
			if err != nil {
				result <- &ServiceDeploymentChannelResponse{
					Error: fmt.Errorf("packaging service %s: %w", serviceConfig.Name, err),
				}
			}

			//deploymentArtifact = buildResult.OutputPath
			return nil
		})

		if err != nil {
			result <- &ServiceDeploymentChannelResponse{
				Error: err,
			}
			return
		}

		var deployResult ServiceDeploymentResult

		err = sm.Invoke(ctx, ServiceEventDeploy, serviceEventArgs, func() error {
			log.Printf("deploying service %s", serviceConfig.Name)

			progress <- "Preparing for deployment"
			//res, err := sm.Publish(ctx, sm.azdContext, deploymentArtifact, progress)
			_, err := sm.Publish(ctx, serviceConfig)
			if err != nil {
				result <- &ServiceDeploymentChannelResponse{
					Error: fmt.Errorf("deploying service %s package: %w", serviceConfig.Name, err),
				}
			}

			// deployResult = res
			return nil
		})

		if err != nil {
			result <- &ServiceDeploymentChannelResponse{
				Error: err,
			}
			return
		}

		// Allow users to specify their own endpoints, in cases where they've configured their own front-end load balancers,
		// reverse proxies or DNS host names outside of the service target (and prefer that to be used instead).
		overriddenEndpoints := sm.getOverriddenEndpoints(ctx, serviceConfig)
		if len(overriddenEndpoints) > 0 {
			deployResult.Endpoints = overriddenEndpoints
		}

		log.Printf("deployed service %s", serviceConfig.Name)
		progress <- "Deployment completed"

		result <- &ServiceDeploymentChannelResponse{
			Result: &deployResult,
		}
	}()

	return result, progress
}

// GetServiceTarget constructs a ServiceTarget from the underlying service configuration
func (sm *serviceManager) GetServiceTarget(ctx context.Context, serviceConfig *ServiceConfig, resource *environment.TargetResource) (ServiceTarget, error) {
	var target ServiceTarget
	var err error

	switch serviceConfig.Host {
	case "", string(AppServiceTarget):
		target, err = NewAppServiceTarget(serviceConfig, sm.env, resource, sm.azCli)
	case string(ContainerAppTarget):
		target, err = NewContainerAppTarget(
			serviceConfig, sm.env, resource, sm.azCli, docker.NewDocker(sm.commandRunner), sm.console, sm.commandRunner, sm.accountManager,
		)
	case string(AzureFunctionTarget):
		target, err = NewFunctionAppTarget(serviceConfig, sm.env, resource, sm.azCli)
	case string(StaticWebAppTarget):
		target, err = NewStaticWebAppTarget(serviceConfig, sm.env, resource, sm.azCli, swa.NewSwaCli(sm.commandRunner))
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
		frameworkService = NewDotNetProject(sm.commandRunner, serviceConfig, sm.env)
	case "py", "python":
		frameworkService = NewPythonProject(sm.commandRunner, serviceConfig, sm.env)
	case "js", "ts":
		frameworkService = NewNpmProject(sm.commandRunner, serviceConfig, sm.env)
	case "java":
		frameworkService = NewMavenProject(sm.commandRunner, serviceConfig, sm.env)
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

func (sm *serviceManager) getTargetResource(ctx context.Context, serviceConfig *ServiceConfig) (*environment.TargetResource, error) {
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
func (sm *serviceManager) GetServiceResources(ctx context.Context, serviceConfig *ServiceConfig, resourceGroupName string) ([]azcli.AzCliResource, error) {
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
func (sm *serviceManager) GetServiceResource(ctx context.Context, serviceConfig *ServiceConfig, resourceGroupName string, rerunCommand string) (azcli.AzCliResource, error) {

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
func (sm *serviceManager) resolveServiceResource(ctx context.Context, serviceConfig *ServiceConfig, resourceGroupName string, rerunCommand string) (azcli.AzCliResource, error) {
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
