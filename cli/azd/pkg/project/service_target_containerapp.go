// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/containerapps"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
)

type containerAppTarget struct {
	env                 *environment.Environment
	envManager          environment.Manager
	containerHelper     *ContainerHelper
	containerAppService containerapps.ContainerAppService
	resourceManager     ResourceManager
	armDeployments      *azapi.StandardDeployments
	console             input.Console
	commandRunner       exec.CommandRunner

	bicepCli func() (*bicep.Cli, error)
}

// NewContainerAppTarget creates the container app service target.
//
// The target resource can be partially filled with only ResourceGroupName, since container apps
// can be provisioned during deployment.
func NewContainerAppTarget(
	env *environment.Environment,
	envManager environment.Manager,
	containerHelper *ContainerHelper,
	containerAppService containerapps.ContainerAppService,
	resourceManager ResourceManager,
	deploymentService *azapi.StandardDeployments,
	console input.Console,
	commandRunner exec.CommandRunner,
) ServiceTarget {
	return &containerAppTarget{
		env:                 env,
		envManager:          envManager,
		containerHelper:     containerHelper,
		containerAppService: containerAppService,
		resourceManager:     resourceManager,
		armDeployments:      deploymentService,
		console:             console,
		commandRunner:       commandRunner,
	}
}

// Gets the required external tools
func (at *containerAppTarget) RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
	return at.containerHelper.RequiredExternalTools(ctx, serviceConfig)
}

// Initializes the Container App target
func (at *containerAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	if err := at.addPreProvisionChecks(ctx, serviceConfig); err != nil {
		return fmt.Errorf("initializing container app target: %w", err)
	}

	return nil
}

// Prepares and tags the container image from the build output based on the specified service configuration
func (at *containerAppTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	// Container reference already handled by the underlying framework service
	// No particular additional package requirements for ACA
	return &ServicePackageResult{}, nil
}

// Publish pushes the container image to ACR
func (at *containerAppTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	publishOptions *PublishOptions,
) (*ServicePublishResult, error) {
	if err := at.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	var publishResult *ServicePublishResult
	var err error

	// Extract package path from service context artifacts
	var packagePath string
	// Look for container image first, then directory
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindContainer)); found {
		packagePath = artifact.Location
	} else if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindDirectory)); found {
		packagePath = artifact.Location
	}

	// Skip publishing to the container registry if packagePath is a remote image reference,
	// such as when called through `azd deploy --from-package <image>`
	if parsedImage, err := docker.ParseContainerImage(packagePath); err == nil {
		if parsedImage.Registry != "" {
			publishResult = &ServicePublishResult{
				Artifacts: ArtifactCollection{
					{
						Kind:         ArtifactKindContainer,
						Location:     packagePath,
						LocationKind: LocationKindRemote,
						Metadata: map[string]string{
							"registry": parsedImage.Registry,
							"image":    packagePath,
						},
					},
				},
			}
		}
	}

	if publishResult == nil {
		// Login, tag & push container image to ACR
		publishResult, err = at.containerHelper.Publish(
			ctx, serviceConfig, serviceContext, targetResource, progress, publishOptions)
		if err != nil {
			return nil, err
		}
	}

	// Save the name of the image we pushed into the environment with a well known key.
	log.Printf("writing image name to environment")

	// Extract remote image from artifacts
	if remoteContainer, ok := publishResult.Artifacts.FindFirst(WithKind(ArtifactKindContainer)); ok {
		at.env.SetServiceProperty(serviceConfig.Name, "IMAGE_NAME", remoteContainer.Location)
	}

	if err := at.envManager.Save(ctx, at.env); err != nil {
		return nil, fmt.Errorf("saving image name to environment: %w", err)
	}

	return publishResult, nil
}

// Deploys the container app service using the published image.
func (at *containerAppTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if err := at.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	// Extract image name from publish artifacts in service context
	var imageName string
	if artifact, found := serviceContext.Publish.FindFirst(WithKind(ArtifactKindContainer)); found {
		imageName = artifact.Location
	}
	if imageName == "" {
		return nil, fmt.Errorf("no container image found in service context for service: %s", serviceConfig.Name)
	}

	// Default resource name and type
	resourceName := targetResource.ResourceName()
	resourceTypeContainer := azapi.AzureResourceTypeContainerApp

	// Check for the presence of a deployment module infra/<module_name> that is used to deploy the revisions.
	// If present, build and deploy it.
	controlledRevision := false

	moduleName := serviceConfig.Module
	if moduleName == "" {
		moduleName = serviceConfig.Name
	}

	modulePath := filepath.Join(serviceConfig.Project.Infra.Path, moduleName)
	bicepPath := modulePath + ".bicep"
	bicepParametersPath := modulePath + ".parameters.json"
	bicepParamPath := modulePath + ".bicepparam"
	mainPath := bicepPath

	if _, err := os.Stat(bicepParamPath); err == nil {
		controlledRevision = true
		mainPath = bicepParamPath
	} else if _, err := os.Stat(bicepPath); err == nil {
		if _, err := os.Stat(bicepParametersPath); err == nil {
			controlledRevision = true
		}
	}

	if controlledRevision {
		fetchBicepCli := at.bicepCli
		if fetchBicepCli == nil {
			fetchBicepCli = func() (*bicep.Cli, error) {
				return bicep.NewCli(ctx, at.console, at.commandRunner)
			}
		}

		bicepCli, err := fetchBicepCli()
		if err != nil {
			return nil, fmt.Errorf("acquiring bicep cli: %w", err)
		}

		progress.SetProgress(NewServiceProgress("Building bicep"))
		deployment, err := compileBicep(bicepCli, ctx, mainPath, at.env)
		if err != nil {
			return nil, fmt.Errorf("building bicep: %w", err)
		}

		var template azure.ArmTemplate
		if err := json.Unmarshal(deployment.Template, &template); err != nil {
			log.Printf("failed unmarshalling arm template to JSON: %s: contents:\n%s", err, deployment.Template)
			return nil, fmt.Errorf("failed unmarshalling arm template from json: %w", err)
		}

		progress.SetProgress(NewServiceProgress("Deploying revision"))
		deploymentResult, err := at.armDeployments.DeployToResourceGroup(
			ctx,
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			at.armDeployments.GenerateDeploymentName(serviceConfig.Name),
			deployment.Template,
			deployment.Parameters,
			nil, nil,
		)
		if err != nil {
			return nil, fmt.Errorf("deploying bicep template: %w", err)
		}

		deploymentHostDetails, err := deploymentHost(deploymentResult)
		if err != nil {
			return nil, fmt.Errorf("getting deployment host type: %w", err)
		}
		resourceName = deploymentHostDetails.name
		outputs := azapi.CreateDeploymentOutput(deploymentResult.Outputs)

		if len(outputs) > 0 {
			outputParams := provisioning.OutputParametersFromArmOutputs(template.Outputs, outputs)
			err := provisioning.UpdateEnvironment(ctx, outputParams, at.env, at.envManager)
			if err != nil {
				return nil, fmt.Errorf("updating environment: %w", err)
			}
		}
	} else {
		if resourceName == "" {
			// Fetch the target resource explicitly
			res, err := at.resourceManager.GetTargetResource(ctx, at.env.GetSubscriptionId(), serviceConfig)
			if err != nil {
				return nil, fmt.Errorf("fetching target resource: %w", err)
			}

			targetResource = res
		}

		// Fall back to only updating container image when no bicep infra is present
		containerAppOptions := containerapps.ContainerAppOptions{
			ApiVersion: serviceConfig.ApiVersion,
		}

		progress.SetProgress(NewServiceProgress("Updating container app revision"))
		err := at.containerAppService.AddRevision(
			ctx,
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			resourceName,
			imageName,
			&containerAppOptions,
		)
		if err != nil {
			return nil, fmt.Errorf("updating container app service: %w", err)
		}
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for container app service"))

	// Create deployment deployArtifacts
	deployArtifacts := ArtifactCollection{}

	target := environment.NewTargetResource(
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		resourceName,
		string(resourceTypeContainer))

	var resourceArtifact *Artifact
	if err := mapper.Convert(target, &resourceArtifact); err == nil {
		if err := deployArtifacts.Add(resourceArtifact); err != nil {
			return nil, fmt.Errorf("failed to add resource artifact: %w", err)
		}
	}

	endpoints, err := at.Endpoints(ctx, serviceConfig, target)
	if err != nil {
		return nil, err
	}

	// Add endpoint artifacts
	for _, endpoint := range endpoints {
		deployArtifacts = append(deployArtifacts, &Artifact{
			Kind:         ArtifactKindEndpoint,
			Location:     endpoint,
			LocationKind: LocationKindRemote,
			Metadata: map[string]string{
				"service": serviceConfig.Name,
			},
		})
	}

	return &ServiceDeployResult{
		Artifacts: deployArtifacts,
	}, nil
}

// Gets endpoint for the container app service
func (at *containerAppTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	containerAppOptions := containerapps.ContainerAppOptions{
		ApiVersion: serviceConfig.ApiVersion,
	}

	if ingressConfig, err := at.containerAppService.GetIngressConfiguration(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		&containerAppOptions,
	); err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	} else {
		endpoints := make([]string, len(ingressConfig.HostNames))
		for idx, hostName := range ingressConfig.HostNames {
			endpoints[idx] = fmt.Sprintf("https://%s/", hostName)
		}

		return endpoints, nil
	}
}

func (at *containerAppTarget) validateTargetResource(
	targetResource *environment.TargetResource,
) error {
	if targetResource.ResourceGroupName() == "" {
		return fmt.Errorf("missing resource group name: %s", targetResource.ResourceGroupName())
	}

	if targetResource.ResourceType() != "" {
		if err := checkResourceType(targetResource, azapi.AzureResourceTypeContainerApp); err != nil {
			return err
		}
	}

	return nil
}

func (at *containerAppTarget) addPreProvisionChecks(ctx context.Context, serviceConfig *ServiceConfig) error {
	// Attempt to retrieve the target resource for the current service
	// This allows the resource deployment to detect whether or not to pull existing container image during
	// provision operation to avoid resetting the container app back to a default image
	return serviceConfig.Project.AddHandler(
		ctx,
		"preprovision",
		func(ctx context.Context, args ProjectLifecycleEventArgs) error {
			exists := false

			// Check if the target resource already exists
			targetResource, err := at.resourceManager.GetTargetResource(ctx, at.env.GetSubscriptionId(), serviceConfig)
			if err == nil && targetResource != nil && targetResource.ResourceName() != "" {
				exists = true
			}

			at.env.SetServiceProperty(serviceConfig.Name, "RESOURCE_EXISTS", strconv.FormatBool(exists))
			return at.envManager.Save(ctx, at.env)
		},
	)
}
