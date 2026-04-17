// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/containerapps"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
)

// envMu serializes all access to the shared *environment.Environment map
// during concurrent deploys. Environment.DotenvSet, SetServiceProperty
// (which calls DotenvSet internally), Getenv, and envManager.Save all touch
// the same unprotected map[string]string, so every read and write across the
// Publish + Deploy + preprovision paths must go through this mutex.
//
// Threading model for concurrent deploys:
//   - shouldUseDirectRevisionAPI: reads (Getenv) and writes (DotenvSet) the
//     template hash key; then persists via envManager.Save when the hash
//     changes — all under envMu.
//   - expandServiceEnv: reads (Getenv via Expand callback) arbitrary env keys
//     — under envMu.RLock so multiple Expand calls can run concurrently while
//     still blocking against writers.
//   - Publish: writes IMAGE_NAME via SetServiceProperty + envManager.Save —
//     under envMu.
//   - Deploy: writes deployment outputs via UpdateEnvironment (which also
//     ends up calling Save) — under envMu.
//   - preprovision handler: writes RESOURCE_EXISTS + envManager.Save —
//     under envMu.
//
// This is a package-level RWMutex (rather than instance-level) because all
// containerAppTarget instances in a single azd run share the same
// *environment.Environment pointer. A per-instance mutex would not protect
// against cross-instance races on the shared map.
//
// RWMutex lets the expand path run concurrently (Getenv-only) while
// serializing every writer against every other writer AND against readers.
var envMu sync.RWMutex

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

	// expandedEnvMu protects expandedEnvCache from concurrent access.
	expandedEnvMu sync.Mutex
	// expandedEnvCache caches the result of serviceConfig.Environment.Expand()
	// keyed by the service name, to avoid redundant env var resolution within the same azd process.
	expandedEnvCache map[string]map[string]string
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
		expandedEnvCache:    make(map[string]map[string]string),
	}
}

// expandServiceEnv expands environment variables from the service config, caching results
// to avoid redundant resolution within the same azd process.
func (at *containerAppTarget) expandServiceEnv(serviceConfig *ServiceConfig) (map[string]string, error) {
	at.expandedEnvMu.Lock()
	defer at.expandedEnvMu.Unlock()

	if cached, ok := at.expandedEnvCache[serviceConfig.Name]; ok {
		return cached, nil
	}

	// Hold envMu (read lock) while reading from the shared env map via Expand.
	// The underlying Environment map is not goroutine-safe, and concurrent
	// writers (DotenvSet / SetServiceProperty / envManager.Save) take the
	// write lock. RLock allows concurrent expand calls across services.
	envMu.RLock()
	envVars, err := serviceConfig.Environment.Expand(at.env.Getenv)
	envMu.RUnlock()
	if err != nil {
		return nil, err
	}

	at.expandedEnvCache[serviceConfig.Name] = envVars
	return envVars, nil
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
			ctx, serviceConfig, serviceContext, targetResource, at.env, progress, publishOptions)
		if err != nil {
			return nil, err
		}
	}

	// Save the name of the image we pushed into the environment with a well known key.
	log.Printf("writing image name to environment")

	// Serialize env mutation + save to prevent lost updates and map races when
	// parallel publishes write to the same shared environment (see envMu docs).
	envMu.Lock()

	// Extract remote image from artifacts
	if remoteContainer, ok := publishResult.Artifacts.FindFirst(WithKind(ArtifactKindContainer)); ok {
		at.env.SetServiceProperty(serviceConfig.Name, "IMAGE_NAME", remoteContainer.Location)
	}

	if err := at.envManager.Save(ctx, at.env); err != nil {
		envMu.Unlock()
		return nil, fmt.Errorf("saving image name to environment: %w", err)
	}

	envMu.Unlock()

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

	infraOptions, err := serviceConfig.Project.Infra.GetWithDefaults()
	if err != nil {
		return nil, fmt.Errorf("getting infra options: %w", err)
	}

	infraRoot := infraOptions.Path
	if !filepath.IsAbs(infraRoot) {
		infraRoot = filepath.Join(serviceConfig.Project.Path, infraRoot)
	}

	modulePath := filepath.Join(infraRoot, moduleName)
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

	// Smart deploy API: prefer direct revision API for code-only changes when template is unchanged.
	// This avoids the overhead of full ARM template revalidation when only the container image tag changed.
	if controlledRevision && at.shouldUseDirectRevisionAPI(ctx, serviceConfig, mainPath) {
		controlledRevision = false
	}

	if controlledRevision {
		tracing.AppendUsageAttributeUnique(fields.FeaturesKey.String(fields.FeatRevisionDeployment))

		fetchBicepCli := at.bicepCli
		if fetchBicepCli == nil {
			fetchBicepCli = func() (*bicep.Cli, error) {
				return bicep.NewCli(at.console, at.commandRunner), nil
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
		resourceTypeContainer = deploymentHostDetails.hostType
		outputs := azapi.CreateDeploymentOutput(deploymentResult.Outputs)

		if len(outputs) > 0 {
			outputParams := provisioning.OutputParametersFromArmOutputs(template.Outputs, outputs)
			envMu.Lock()
			err := provisioning.UpdateEnvironment(ctx, outputParams, at.env, at.envManager)
			envMu.Unlock()
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
			resourceName = targetResource.ResourceName()
		}

		// Fall back to only updating container image when no bicep infra is present
		containerAppOptions := containerapps.ContainerAppOptions{
			ApiVersion: serviceConfig.ApiVersion,
		}

		isJob := isJobResource(targetResource)

		if isJob {
			tracing.AppendUsageAttributeUnique(fields.FeaturesKey.String(fields.FeatJobDeployment))
			resourceTypeContainer = azapi.AzureResourceTypeContainerAppJob

			// Expand environment variables from service config
			envVars, err := at.expandServiceEnv(serviceConfig)
			if err != nil {
				return nil, fmt.Errorf("expanding environment variables: %w", err)
			}

			progress.SetProgress(NewServiceProgress("Updating container app job image"))
			err = at.containerAppService.UpdateContainerAppJobImage(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				resourceName,
				imageName,
				envVars,
				&containerAppOptions,
			)
			if err != nil {
				return nil, fmt.Errorf("updating container app job: %w", err)
			}
		} else {
			// Expand environment variables from service config
			envVars, err := at.expandServiceEnv(serviceConfig)
			if err != nil {
				return nil, fmt.Errorf("expanding environment variables: %w", err)
			}

			progress.SetProgress(NewServiceProgress("Updating container app revision"))
			err = at.containerAppService.AddRevision(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				resourceName,
				imageName,
				envVars,
				&containerAppOptions,
			)
			if err != nil {
				return nil, fmt.Errorf("updating container app service: %w", err)
			}
		}
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for service"))

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
	// Container App Jobs don't have ingress/endpoints
	if isJobResource(targetResource) {
		return []string{}, nil
	}

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

// shouldUseDirectRevisionAPI checks whether the service's infrastructure template is unchanged
// since the last deployment, indicating that only the container image tag changed and the
// cheaper direct revision API can be used instead of a full ARM template deployment.
// The envMu mutex serializes access to the shared environment map for concurrent deploys.
// The hash is persisted to disk whenever it changes so the optimization survives process
// exit on revision paths that have no deployment outputs (e.g., no-output template).
func (at *containerAppTarget) shouldUseDirectRevisionAPI(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	mainPath string,
) bool {
	templateContent, readErr := os.ReadFile(mainPath)
	if readErr != nil {
		return false
	}

	currentHash := sha256.Sum256(templateContent)
	currentHashStr := hex.EncodeToString(currentHash[:])
	envHashKey := fmt.Sprintf("SERVICE_%s_TEMPLATE_HASH", environment.Key(serviceConfig.Name))

	// Serialize access to the shared Environment map. DotenvSet and Getenv operate
	// on an unprotected map[string]string, so concurrent calls during parallel
	// deploy would be a data race. Hold the write lock across the read-modify-write
	// and the subsequent Save so another writer can't observe a torn state.
	envMu.Lock()
	previousHash := at.env.Getenv(envHashKey)
	hashChanged := currentHashStr != previousHash
	if hashChanged {
		at.env.DotenvSet(envHashKey, currentHashStr)
	}

	// Persist the updated hash so the next run sees it. The downstream env-save
	// inside Deploy() only runs when the deployment produced ARM outputs, so a
	// no-output template (or the direct-revision branch, which skips ARM
	// deploy entirely) would otherwise lose the hash at process exit and fall
	// back to a full ARM deployment on the next `azd deploy`.
	var saveErr error
	if hashChanged {
		saveErr = at.envManager.Save(ctx, at.env)
	}
	envMu.Unlock()

	if hashChanged && saveErr != nil {
		log.Printf("persisting template hash for %s failed: %v", serviceConfig.Name, saveErr)
		// Fall through: the in-memory hash is still set for this run, but
		// the next run may redo a full deploy. Do not fail the build over
		// a persistence hiccup.
	}

	if currentHashStr == previousHash {
		log.Printf(
			"template unchanged for %s, using direct revision API",
			serviceConfig.Name,
		)
		return true
	}

	return false
}

func (at *containerAppTarget) validateTargetResource(
	targetResource *environment.TargetResource,
) error {
	if targetResource.ResourceGroupName() == "" {
		return fmt.Errorf("missing resource group name: %s", targetResource.ResourceGroupName())
	}

	if targetResource.ResourceType() != "" {
		errApp := checkResourceType(targetResource, azapi.AzureResourceTypeContainerApp)
		errJob := checkResourceType(targetResource, azapi.AzureResourceTypeContainerAppJob)
		if errApp != nil && errJob != nil {
			return fmt.Errorf(
				"resource '%s' with type '%s' does not match expected types: %w",
				targetResource.ResourceName(),
				targetResource.ResourceType(),
				errors.Join(errApp, errJob),
			)
		}
	}

	return nil
}

// isJobResource returns true when the target resource is a Container App Job.
func isJobResource(targetResource *environment.TargetResource) bool {
	return strings.EqualFold(
		targetResource.ResourceType(),
		string(azapi.AzureResourceTypeContainerAppJob),
	)
}

// ResolveTargetResource implements TargetResourceResolver for containerAppTarget.
// It attempts to find an existing container app resource. If not found, it returns a partial
// target resource (with resource group but no resource name) to support bicep-based deployments
// that provision the container app on-demand.
func (at *containerAppTarget) ResolveTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *ServiceConfig,
	defaultResolver func() (*environment.TargetResource, error),
) (*environment.TargetResource, error) {
	targetResource, err := defaultResolver()
	if err == nil {
		return targetResource, nil
	}

	if _, ok := errors.AsType[*azureutil.ResourceNotFoundError](err); ok {
		resourceGroupTemplate := serviceConfig.ResourceGroupName
		if resourceGroupTemplate.Empty() {
			resourceGroupTemplate = serviceConfig.Project.ResourceGroupName
		}

		resourceGroupName, rgErr := at.resourceManager.GetResourceGroupName(ctx, subscriptionId, resourceGroupTemplate)
		if rgErr != nil {
			return nil, err
		}

		// Return partial target resource to enable bicep-based deployments.
		// Use empty resource type since the actual type (containerApp vs job)
		// is determined by the Bicep template at deploy time.
		return environment.NewTargetResource(
			subscriptionId,
			resourceGroupName,
			"",
			"",
		), nil
	}

	return nil, err
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

			envMu.Lock()
			defer envMu.Unlock()
			at.env.SetServiceProperty(serviceConfig.Name, "RESOURCE_EXISTS", strconv.FormatBool(exists))
			return at.envManager.Save(ctx, at.env)
		},
	)
}
