// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/containerapps"
	"github.com/azure/azure-dev/cli/azd/pkg/cosmosdb"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/sqldb"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/drone/envsubst"
)

type dotnetContainerAppTarget struct {
	env                 *environment.Environment
	containerHelper     *ContainerHelper
	containerAppService containerapps.ContainerAppService
	resourceManager     ResourceManager
	dotNetCli           *dotnet.Cli
	bicepCli            *bicep.Cli
	cosmosDbService     cosmosdb.CosmosDbService
	sqlDbService        sqldb.SqlDbService
	keyvaultService     keyvault.KeyVaultService
	alphaFeatureManager *alpha.FeatureManager
	deploymentService   azapi.DeploymentService
	azureClient         *azapi.AzureClient
}

// NewDotNetContainerAppTarget creates the Service Target for a Container App that is written in .NET. Unlike
// [ContainerAppTarget], this target does not require a Dockerfile to be present in the project. Instead, it uses the built
// in support in .NET 8 for publishing containers using `dotnet publish`. In addition, it uses a different deployment
// strategy built on a yaml manifest file, using the same format `az containerapp create --yaml`, with additional support
// for using text/template to do replacements, similar to tools like Helm.
//
// Note that unlike [ContainerAppTarget] this target does not add SERVICE_<XYZ>_IMAGE_NAME values to the environment,
// instead, the image name is present on the context object used when rendering the template.
func NewDotNetContainerAppTarget(
	env *environment.Environment,
	containerHelper *ContainerHelper,
	containerAppService containerapps.ContainerAppService,
	resourceManager ResourceManager,
	dotNetCli *dotnet.Cli,
	bicepCli *bicep.Cli,
	cosmosDbService cosmosdb.CosmosDbService,
	sqlDbService sqldb.SqlDbService,
	keyvaultService keyvault.KeyVaultService,
	alphaFeatureManager *alpha.FeatureManager,
	deploymentService azapi.DeploymentService,
	azureClient *azapi.AzureClient,
) ServiceTarget {
	return &dotnetContainerAppTarget{
		env:                 env,
		containerHelper:     containerHelper,
		containerAppService: containerAppService,
		resourceManager:     resourceManager,
		dotNetCli:           dotNetCli,
		bicepCli:            bicepCli,
		cosmosDbService:     cosmosDbService,
		sqlDbService:        sqlDbService,
		keyvaultService:     keyvaultService,
		alphaFeatureManager: alphaFeatureManager,
		deploymentService:   deploymentService,
		azureClient:         azureClient,
	}
}

// Gets the required external tools
func (at *dotnetContainerAppTarget) RequiredExternalTools(ctx context.Context, svc *ServiceConfig) []tools.ExternalTool {
	return []tools.ExternalTool{at.dotNetCli}
}

// Initializes the Container App target
func (at *dotnetContainerAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Prepares and tags the container image from the build output based on the specified service configuration
func (at *dotnetContainerAppTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	return &ServicePackageResult{}, nil
}

// TODO: move publish logic from Deploy() into Publish()
func (at *dotnetContainerAppTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	publishOptions *PublishOptions,
) (*ServicePublishResult, error) {
	return &ServicePublishResult{}, nil
}

// Deploys service container images to ACR and provisions the container app service.
func (at *dotnetContainerAppTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if err := at.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	// Track deployment results for different deployment paths
	var bicepDeploymentResult *azapi.ResourceDeployment
	var yamlDeploymentError error

	progress.SetProgress(NewServiceProgress("Pushing container image"))

	var remoteImageName string
	var portNumber int

	// This service target is shared across four different aspire resource types: "dockerfile.v0" (a reference to
	// an project backed by a dockerfile), "container.v0" (a reference to a project backed by an existing container
	// image), "project.v0" (a reference to a project backed by a .NET project), and "container.v1" (a reference
	// to a project which might have an existing container image, or can provide a dockerfile).
	// Depending on the type, we have different steps for pushing the container image.
	//
	// For the dockerfile.v0 and container.v1+dockerfile type, [DotNetImporter] arranges things such that we can
	// leverage the existing support in `azd` for services backed by a Dockerfile.
	// This causes the image to be built and pushed to ACR.
	//
	// For the container.v0 or container.v1+image type, we assume the container image specified by the manifest is
	// public and just use it directly.
	//
	// For the project.v0 type, we use the .NET CLI to publish the container image to ACR.
	//
	// The name of the image that should be referenced in the manifest is stored in `remoteImageName` and presented
	// to the deployment template as a parameter named `Image`.
	if serviceConfig.Language == ServiceLanguageDocker {
		res, err := at.containerHelper.Publish(ctx, serviceConfig, serviceContext, targetResource, progress, nil)
		if err != nil {
			return nil, err
		}

		// Extract remote image from artifacts
		var remoteImage string
		if artifact, found := res.Artifacts.FindFirst(WithKind(ArtifactKindContainer)); found {
			remoteImage = artifact.Location
		}
		remoteImageName = remoteImage
	} else if serviceConfig.DotNetContainerApp.ContainerImage != "" {
		remoteImageName = serviceConfig.DotNetContainerApp.ContainerImage
	} else {
		progress.SetProgress(NewServiceProgress("Logging in to registry"))

		// Login, tag & push container image to ACR
		dockerCreds, err := at.containerHelper.Credentials(ctx, serviceConfig, targetResource)
		if err != nil {
			return nil, fmt.Errorf("logging in to registry: %w", err)
		}

		imageName := fmt.Sprintf("%s:%s",
			at.containerHelper.DefaultImageName(serviceConfig),
			at.containerHelper.DefaultImageTag())

		portNumber, err = at.dotNetCli.PublishContainer(
			ctx,
			serviceConfig.Path(),
			"Release",
			imageName,
			dockerCreds.LoginServer,
			dockerCreds.Username,
			dockerCreds.Password)
		if err != nil {
			return nil, fmt.Errorf("publishing container: %w", err)
		}

		remoteImageName = fmt.Sprintf("%s/%s", dockerCreds.LoginServer, imageName)
	}

	progress.SetProgress(NewServiceProgress("Updating application"))

	var manifestTemplate string

	appHostRoot := serviceConfig.DotNetContainerApp.AppHostPath
	if f, err := os.Stat(appHostRoot); err == nil && !f.IsDir() {
		appHostRoot = filepath.Dir(appHostRoot)
	}

	deploymentConfig := serviceConfig.DotNetContainerApp.Manifest.Resources[serviceConfig.Name].Deployment
	useBicepForContainerApps := deploymentConfig != nil
	projectName := serviceConfig.DotNetContainerApp.ProjectName

	if useBicepForContainerApps {
		bicepParamPath := filepath.Join(
			appHostRoot, "infra", projectName, fmt.Sprintf("%s.tmpl.bicepparam", projectName))
		if _, err := os.Stat(bicepParamPath); err == nil {
			// read the file into manifestContents
			contents, err := os.ReadFile(bicepParamPath)
			if err != nil {
				return nil, fmt.Errorf("reading container app manifest: %w", err)
			}
			manifestTemplate = string(contents)
		} else {
			// missing bicepparam template file, generate it
			contents, _, err := apphost.ContainerAppManifestTemplateForProject(
				serviceConfig.DotNetContainerApp.Manifest,
				projectName,
				apphost.AppHostOptions{},
			)
			if err != nil {
				return nil, fmt.Errorf("generating container app manifest: %w", err)
			}
			manifestTemplate = contents
		}
	} else {
		manifestPath := filepath.Join(
			appHostRoot, "infra", fmt.Sprintf("%s.tmpl.yaml", projectName))

		if _, err := os.Stat(manifestPath); err == nil {
			log.Printf("using container app manifest from %s", manifestPath)

			contents, err := os.ReadFile(manifestPath)
			if err != nil {
				return nil, fmt.Errorf("reading container app manifest: %w", err)
			}
			manifestTemplate = string(contents)
		} else {
			log.Printf(
				"generating container app manifest from %s for project %s",
				serviceConfig.DotNetContainerApp.AppHostPath,
				projectName)

			generatedManifest, _, err := apphost.ContainerAppManifestTemplateForProject(
				serviceConfig.DotNetContainerApp.Manifest,
				projectName,
				apphost.AppHostOptions{},
			)
			if err != nil {
				return nil, fmt.Errorf("generating container app manifest: %w", err)
			}
			manifestTemplate = generatedManifest
		}
	}

	log.Printf("Resolve the manifest template for project %s", projectName)

	fns := &containerAppTemplateManifestFuncs{
		ctx:                 ctx,
		manifest:            serviceConfig.DotNetContainerApp.Manifest,
		targetResource:      targetResource,
		containerAppService: at.containerAppService,
		cosmosDbService:     at.cosmosDbService,
		sqlDbService:        at.sqlDbService,
		env:                 at.env,
		keyvaultService:     at.keyvaultService,
	}

	funcMap := template.FuncMap{
		"urlHost":              fns.UrlHost,
		"parameter":            fns.Parameter,
		"parameterWithDefault": fns.ParameterWithDefault,
		// securedParameter gets a parameter the same way as parameter, but supporting the securedParameter
		// allows to update the logic of pulling secret parameters in the future, if azd changes the way it
		// stores the parameter value.
		"securedParameter": fns.Parameter,
		"uriEncode":        url.QueryEscape,
		"secretOutput":     fns.kvSecret,
		"targetPortOrDefault": func(targetPortFromManifest int) int {
			// portNumber is 0 for dockerfile.v0, so we use the targetPort from the manifest
			if portNumber == 0 {
				return targetPortFromManifest
			}
			return portNumber
		},
	}

	var inputs map[string]any
	// inputs are auto-gen during provision and saved to env-config
	if has, err := at.env.Config.GetSection("inputs", &inputs); err != nil {
		return nil, fmt.Errorf("failed to get inputs section: %w", err)
	} else if !has {
		inputs = make(map[string]any)
	}

	tmpl, err := template.New("manifest template").
		Option("missingkey=error").
		Funcs(funcMap).
		Parse(manifestTemplate)
	if err != nil {
		return nil, fmt.Errorf("failing parsing manifest template: %w", err)
	}

	// Both bicepparam and yaml go-template can reference all variables from AZD environment and those variables
	// from system environment variables that are prefixed either AZURE_ or AZD_
	// Variables from AZD environment override those from system environment variables.
	env := make(map[string]string)
	for _, envVar := range os.Environ() {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.HasPrefix(parts[0], "AZURE_") || strings.HasPrefix(parts[0], "AZD_") {
			env[parts[0]] = parts[1]
		}
	}
	// Add the environment variables from the azd environment
	for key, value := range at.env.Dotenv() {
		env[key] = value
	}

	builder := strings.Builder{}
	err = tmpl.Execute(&builder, struct {
		Env    map[string]string
		Image  string
		Inputs map[string]any
	}{
		Env:    env,
		Image:  remoteImageName,
		Inputs: inputs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed executing template file: %w", err)
	}

	aspireDeploymentType := azapi.AzureResourceTypeContainerApp
	resourceName := serviceConfig.Name
	if useBicepForContainerApps {
		// Compile the bicep template
		deployment, err := func() (armDeployment, error) {
			tempFolder, err := os.MkdirTemp("", fmt.Sprintf("%s-build*", projectName))
			if err != nil {
				return armDeployment{}, fmt.Errorf("creating temporary build folder: %w", err)
			}
			defer func() {
				_ = os.RemoveAll(tempFolder)
			}()
			// write bicepparam content to a new file in the temp folder
			f, err := os.Create(filepath.Join(tempFolder, "main.bicepparam"))
			if err != nil {
				return armDeployment{}, fmt.Errorf("creating bicepparam file: %w", err)
			}
			_, err = io.Copy(f, strings.NewReader(builder.String()))
			if err != nil {
				return armDeployment{}, fmt.Errorf("writing bicepparam file: %w", err)
			}
			err = f.Close()
			if err != nil {
				return armDeployment{}, fmt.Errorf("closing bicepparam file: %w", err)
			}

			// copy module to same path as bicepparam so it can be compiled from the temp folder
			bicepSourceFileName := filepath.Base(*deploymentConfig.Path)
			bicepContent, err := os.ReadFile(filepath.Join(appHostRoot, "infra", projectName, bicepSourceFileName))
			if err != nil {
				// when source bicep is not found, we generate it from the manifest
				generatedBicep, err := apphost.ContainerSourceBicepContent(
					serviceConfig.DotNetContainerApp.Manifest,
					projectName,
					apphost.AppHostOptions{},
				)
				if err != nil {
					return armDeployment{}, fmt.Errorf("generating bicep file: %w", err)
				}
				bicepContent = []byte(generatedBicep)
			}
			sourceFile, err := os.Create(filepath.Join(tempFolder, bicepSourceFileName))
			if err != nil {
				return armDeployment{}, fmt.Errorf("creating bicep file: %w", err)
			}
			_, err = io.Copy(sourceFile, strings.NewReader(string(bicepContent)))
			if err != nil {
				return armDeployment{}, fmt.Errorf("writing bicep file: %w", err)
			}
			err = sourceFile.Close()
			if err != nil {
				return armDeployment{}, fmt.Errorf("closing bicep file: %w", err)
			}

			deployment, err := compileBicep(at.bicepCli, ctx, f.Name(), at.env)
			if err != nil {
				return armDeployment{}, fmt.Errorf("building container app bicep: %w", err)
			}
			return deployment, nil
		}()
		if err != nil {
			return nil, err
		}

		bicepDeploymentResult, err = at.deploymentService.DeployToResourceGroup(
			ctx,
			at.env.GetSubscriptionId(),
			targetResource.ResourceGroupName(),
			at.deploymentService.GenerateDeploymentName(serviceConfig.Name),
			deployment.Template,
			deployment.Parameters,
			nil, nil)
		if err != nil {
			return nil, fmt.Errorf("deploying bicep template: %w", err)
		}
		deploymentHostDetails, err := deploymentHost(bicepDeploymentResult)
		if err != nil {
			return nil, fmt.Errorf("getting deployment host type: %w", err)
		}
		resourceName = deploymentHostDetails.name
		aspireDeploymentType = deploymentHostDetails.hostType

	} else {
		containerAppOptions := containerapps.ContainerAppOptions{
			ApiVersion: serviceConfig.ApiVersion,
		}

		yamlDeploymentError = at.containerAppService.DeployYaml(
			ctx,
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			serviceConfig.Name,
			[]byte(builder.String()),
			&containerAppOptions,
		)
		if yamlDeploymentError != nil {
			return nil, fmt.Errorf("updating container app service: %w", yamlDeploymentError)
		}
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for service"))

	target := environment.NewTargetResource(
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		resourceName,
		string(aspireDeploymentType))

	endpoints, err := at.Endpoints(ctx, serviceConfig, target)
	if err != nil {
		return nil, err
	}

	artifacts := ArtifactCollection{}

	// Add deployment result as artifact if Bicep deployment was used
	if bicepDeploymentResult != nil {
		if err := artifacts.Add(&Artifact{
			Kind:         ArtifactKindDeployment,
			Location:     bicepDeploymentResult.Name,
			LocationKind: LocationKindRemote,
			Metadata: map[string]string{
				"deploymentType":   "bicep",
				"deploymentStatus": string(bicepDeploymentResult.ProvisioningState),
				"serviceName":      serviceConfig.Name,
				"resourceName":     targetResource.ResourceName(),
				"resourceType":     targetResource.ResourceType(),
				"subscription":     targetResource.SubscriptionId(),
				"resourceGroup":    targetResource.ResourceGroupName(),
				"deploymentName":   bicepDeploymentResult.Name,
				"deploymentId":     bicepDeploymentResult.DeploymentId,
			},
		}); err != nil {
			return nil, fmt.Errorf("failed to add bicep deployment artifact: %w", err)
		}
	} else if yamlDeploymentError == nil {
		// Add YAML deployment artifact if YAML deployment was successful
		if err := artifacts.Add(&Artifact{
			Kind:         ArtifactKindDeployment,
			Location:     "yaml-deployment-completed",
			LocationKind: LocationKindRemote,
			Metadata: map[string]string{
				"deploymentType": "yaml",
				"serviceName":    serviceConfig.Name,
				"resourceName":   targetResource.ResourceName(),
				"resourceType":   targetResource.ResourceType(),
				"subscription":   targetResource.SubscriptionId(),
				"resourceGroup":  targetResource.ResourceGroupName(),
			},
		}); err != nil {
			return nil, fmt.Errorf("failed to add yaml deployment artifact: %w", err)
		}
	}

	// Add endpoints as artifacts
	for _, endpoint := range endpoints {
		if err := artifacts.Add(&Artifact{
			Kind:         ArtifactKindEndpoint,
			Location:     endpoint,
			LocationKind: LocationKindRemote,
		}); err != nil {
			return nil, fmt.Errorf("failed to add endpoint artifact: %w", err)
		}
	}

	// Add resource artifact
	var resourceArtifact *Artifact
	if err := mapper.Convert(targetResource, &resourceArtifact); err == nil {
		if err := artifacts.Add(resourceArtifact); err != nil {
			return nil, fmt.Errorf("failed to add resource artifact: %w", err)
		}
	}

	return &ServiceDeployResult{
		Artifacts: artifacts,
	}, nil
}

type appDeploymentHost struct {
	name     string
	hostType azapi.AzureResourceType
}

// deploymentHost inspect the deployment result and returns the type of the
// host when it is a known host like Container App or WebApp.
// Returns error if the type is not know.
func deploymentHost(deploymentResult *azapi.ResourceDeployment) (appDeploymentHost, error) {
	if deploymentResult == nil {
		return appDeploymentHost{}, fmt.Errorf("deployment result is empty")
	}

	for _, resource := range deploymentResult.Resources {
		rType, err := arm.ParseResourceType(*resource.ID)
		if err != nil {
			return appDeploymentHost{}, err
		}
		r, err := arm.ParseResourceID(*resource.ID)
		if err != nil {
			return appDeploymentHost{}, err
		}
		if rType.String() == string(azapi.AzureResourceTypeWebSite) {
			return appDeploymentHost{
				name:     r.Name,
				hostType: azapi.AzureResourceTypeWebSite,
			}, nil
		}
		if rType.String() == string(azapi.AzureResourceTypeContainerApp) {
			return appDeploymentHost{
				name:     r.Name,
				hostType: azapi.AzureResourceTypeContainerApp,
			}, nil
		}
		if rType.String() == string(azapi.AzureResourceTypeContainerAppJob) {
			return appDeploymentHost{
				name:     r.Name,
				hostType: azapi.AzureResourceTypeContainerAppJob,
			}, nil
		}
	}
	return appDeploymentHost{}, fmt.Errorf("didn't find any known application host from the deployment")
}

// armDeployment represents the compiled ARM template and parameters
// that is ready to be deployed.
type armDeployment struct {
	Template   azure.RawArmTemplate
	Parameters azure.ArmParameters
}

// compileBicep compiles the specified Bicep module to an ARM template and parameters.
//
// The Bicep module can be either a main Bicep file (with .bicep extension) or a Bicep parameters
// file (with .bicepparam extension).
//
// If a Bicep file is provided, the corresponding parameters file must exist in the same directory with the same base name.
// The environment variables in the parameters file will be substituted using the provided
// environment before parsing.
//
// Returns the compiled ARM template and parameters, or an error if the compilation fails.
func compileBicep(
	cli *bicep.Cli,
	ctx context.Context,
	bicepModulePath string,
	env *environment.Environment,
) (armDeployment, error) {
	var result armDeployment

	ext := filepath.Ext(bicepModulePath)
	if ext != ".bicep" && ext != ".bicepparam" {
		return result, fmt.Errorf("bicep module path must have .bicep or .bicepparam extension")
	}

	if ext == ".bicep" {
		paramFilePath := strings.TrimSuffix(bicepModulePath, ext) + ".parameters.json"
		parametersBytes, err := os.ReadFile(paramFilePath)
		if err != nil {
			return result, fmt.Errorf("reading parameters file: %w", err)
		}

		substituted, err := envsubst.Eval(string(parametersBytes), env.Getenv)
		if err != nil {
			return result, fmt.Errorf("performing env substitution: %w", err)
		}

		var paramFile azure.ArmParameterFile
		if err := json.Unmarshal([]byte(substituted), &paramFile); err != nil {
			return result, fmt.Errorf("unmarshaling file: %w", err)
		}

		res, err := cli.Build(ctx, bicepModulePath)
		if err != nil {
			return result, fmt.Errorf("building bicep: %w", err)
		}

		result.Template = azure.RawArmTemplate(res.Compiled)
		result.Parameters = paramFile.Parameters
	} else {
		res, err := cli.BuildBicepParam(ctx, bicepModulePath, env.Environ())
		if err != nil {
			return result, fmt.Errorf("building bicepparam: %w", err)
		}

		type compiledBicepParamResult struct {
			TemplateJson   string `json:"templateJson"`
			ParametersJson string `json:"parametersJson"`
		}
		var bicepParamOutput compiledBicepParamResult
		if err := json.Unmarshal([]byte(res.Compiled), &bicepParamOutput); err != nil {
			return result, fmt.Errorf("failed unmarshalling arm template from json: %w", err)
		}
		var params azure.ArmParameterFile
		if err := json.Unmarshal([]byte(bicepParamOutput.ParametersJson), &params); err != nil {
			return result, fmt.Errorf("failed unmarshalling arm parameters template from json: %w", err)
		}

		result.Template = azure.RawArmTemplate(bicepParamOutput.TemplateJson)
		result.Parameters = params.Parameters
	}

	return result, nil
}

// Gets endpoint for the container app service
func (at *dotnetContainerAppTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	resourceType := azapi.AzureResourceType(targetResource.ResourceType())
	// Currently supports ACA, ACA Jobs, and WebApp for Aspire (on reading Endpoints)
	if resourceType != azapi.AzureResourceTypeWebSite &&
		resourceType != azapi.AzureResourceTypeContainerApp &&
		resourceType != azapi.AzureResourceTypeContainerAppJob {
		return nil, fmt.Errorf("unsupported resource type: %s", resourceType)
	}

	var hostNames []string
	switch resourceType {
	case azapi.AzureResourceTypeContainerApp, azapi.AzureResourceTypeContainerAppJob:

		containerAppOptions := containerapps.ContainerAppOptions{
			ApiVersion: serviceConfig.ApiVersion,
		}
		ingressConfig, err := at.containerAppService.GetIngressConfiguration(
			ctx,
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			targetResource.ResourceName(),
			&containerAppOptions,
		)
		if err != nil {
			// Container App Jobs might not have ingress configuration
			if resourceType == azapi.AzureResourceTypeContainerAppJob {
				// Return empty endpoints for jobs without ingress
				return []string{}, nil
			}
			return nil, fmt.Errorf("fetching service properties: %w", err)
		}
		hostNames = ingressConfig.HostNames

	case azapi.AzureResourceTypeWebSite:
		appServiceProperties, err := at.azureClient.GetAppServiceProperties(
			ctx,
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			targetResource.ResourceName(),
		)
		if err != nil {
			return nil, fmt.Errorf("fetching service properties: %w", err)
		}

		hostNames = appServiceProperties.HostNames
	default:
		hostNames = []string{}
	}

	endpoints := make([]string, len(hostNames))
	for idx, hostName := range hostNames {
		endpoints[idx] = fmt.Sprintf("https://%s/", hostName)
	}
	return endpoints, nil
}

func (at *dotnetContainerAppTarget) validateTargetResource(
	targetResource *environment.TargetResource,
) error {
	if targetResource.ResourceGroupName() == "" {
		return fmt.Errorf("missing resource group name: %s", targetResource.ResourceGroupName())
	}

	if targetResource.ResourceType() != "" {
		if err := checkResourceType(targetResource, azapi.AzureResourceTypeContainerAppEnvironment); err != nil {
			return err
		}
	}

	return nil
}

// containerAppTemplateManifestFuncs contains all the functions that are callable while evaluating the manifest template.
type containerAppTemplateManifestFuncs struct {
	ctx                 context.Context
	manifest            *apphost.Manifest
	targetResource      *environment.TargetResource
	containerAppService containerapps.ContainerAppService
	cosmosDbService     cosmosdb.CosmosDbService
	sqlDbService        sqldb.SqlDbService
	env                 *environment.Environment
	keyvaultService     keyvault.KeyVaultService
}

// UrlHost returns the Hostname (without the port) of the given string, or an error if the string is not a valid URL.
//
// It is callable from a template under the name `urlHost`
func (_ *containerAppTemplateManifestFuncs) UrlHost(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", err
	}
	return u.Hostname(), nil
}

const infraParametersKey = "infra.parameters."

// Parameter resolves the name of a parameter defined in the ACA yaml definition. The parameter can be mapped to a system
// environment variable ONLY.
func (fns *containerAppTemplateManifestFuncs) Parameter(name string) (string, error) {
	envVarMapping := scaffold.AzureSnakeCase(name)
	// map only to system environment variables. Not adding support for mapping to azd environment by design (b/c
	// parameters could be secured)
	if val, found := os.LookupEnv(envVarMapping); found {
		return val, nil
	}

	key := infraParametersKey + name
	val, found := fns.env.Config.Get(key)
	if !found {
		return "", fmt.Errorf("parameter %s not found", name)
	}
	valString, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("parameter %s is not a string", name)
	}
	return valString, nil
}

// ParameterWithDefault resolves the name of a parameter defined in the ACA yaml definition.
// The parameter can be mapped to a system environment variable or be default to a value directly.
func (fns *containerAppTemplateManifestFuncs) ParameterWithDefault(name string, defaultValue string) (string, error) {
	envVarMapping := scaffold.AzureSnakeCase(name)
	if val, found := fns.env.LookupEnv(envVarMapping); found {
		return val, nil
	}
	return defaultValue, nil
}

// kvSecret gets the value of the secret with the given name from the KeyVault with the given host name. If the secret is
// not found, an error is returned.
func (fns *containerAppTemplateManifestFuncs) kvSecret(kvHost, secretName string) (string, error) {
	hostName := fns.env.Getenv(kvHost)
	if hostName == "" {
		return "", fmt.Errorf("the value for %s was not found or is empty", kvHost)
	}

	secret, err := fns.keyvaultService.GetKeyVaultSecret(fns.ctx, fns.targetResource.SubscriptionId(), hostName, secretName)
	if err != nil {
		return "", fmt.Errorf("fetching secret %s from %s: %w", secretName, hostName, err)
	}
	return secret.Value, nil
}
