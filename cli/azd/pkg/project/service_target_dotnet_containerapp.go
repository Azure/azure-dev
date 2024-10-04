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
	packageOutput *ServicePackageResult,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	return packageOutput, nil
}

// Deploys service container images to ACR and provisions the container app service.
func (at *dotnetContainerAppTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if err := at.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	progress.SetProgress(NewServiceProgress("Logging in to registry"))

	// Login, tag & push container image to ACR
	dockerCreds, err := at.containerHelper.Credentials(ctx, serviceConfig, targetResource)
	if err != nil {
		return nil, fmt.Errorf("logging in to registry: %w", err)
	}

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
		res, err := at.containerHelper.Deploy(ctx, serviceConfig, packageOutput, targetResource, false, progress)
		if err != nil {
			return nil, err
		}

		remoteImageName = res.Details.(*dockerDeployResult).RemoteImageTag
	} else if serviceConfig.DotNetContainerApp.ContainerImage != "" {
		remoteImageName = serviceConfig.DotNetContainerApp.ContainerImage
	} else {
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

	progress.SetProgress(NewServiceProgress("Updating container app"))

	var manifestTemplate string
	var armTemplate *azure.RawArmTemplate
	var armParams azure.ArmParameters

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
		"urlHost":   fns.UrlHost,
		"parameter": fns.Parameter,
		// securedParameter gets a parameter the same way as parameter, but supporting the securedParameter
		// allows to update the logic of pulling secret parameters in the future, if azd changes the way it
		// stores the parameter value.
		"securedParameter": fns.Parameter,
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

	builder := strings.Builder{}
	err = tmpl.Execute(&builder, struct {
		Env    map[string]string
		Image  string
		Inputs map[string]any
	}{
		Env:    at.env.Dotenv(),
		Image:  remoteImageName,
		Inputs: inputs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed executing template file: %w", err)
	}

	if useBicepForContainerApps {
		// Compile the bicep template
		compiled, params, err := func() (azure.RawArmTemplate, azure.ArmParameters, error) {
			tempFolder, err := os.MkdirTemp("", fmt.Sprintf("%s-build*", projectName))
			if err != nil {
				return azure.RawArmTemplate{}, nil, fmt.Errorf("creating temporary build folder: %w", err)
			}
			defer func() {
				_ = os.RemoveAll(tempFolder)
			}()
			// write bicepparam content to a new file in the temp folder
			f, err := os.Create(filepath.Join(tempFolder, "main.bicepparam"))
			if err != nil {
				return azure.RawArmTemplate{}, nil, fmt.Errorf("creating bicepparam file: %w", err)
			}
			_, err = io.Copy(f, strings.NewReader(builder.String()))
			if err != nil {
				return azure.RawArmTemplate{}, nil, fmt.Errorf("writing bicepparam file: %w", err)
			}
			err = f.Close()
			if err != nil {
				return azure.RawArmTemplate{}, nil, fmt.Errorf("closing bicepparam file: %w", err)
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
					return azure.RawArmTemplate{}, nil, fmt.Errorf("generating bicep file: %w", err)
				}
				bicepContent = []byte(generatedBicep)
			}
			sourceFile, err := os.Create(filepath.Join(tempFolder, bicepSourceFileName))
			if err != nil {
				return azure.RawArmTemplate{}, nil, fmt.Errorf("creating bicep file: %w", err)
			}
			_, err = io.Copy(sourceFile, strings.NewReader(string(bicepContent)))
			if err != nil {
				return azure.RawArmTemplate{}, nil, fmt.Errorf("writing bicep file: %w", err)
			}
			err = sourceFile.Close()
			if err != nil {
				return azure.RawArmTemplate{}, nil, fmt.Errorf("closing bicep file: %w", err)
			}

			res, err := at.bicepCli.BuildBicepParam(ctx, f.Name(), at.env.Environ())
			if err != nil {
				return azure.RawArmTemplate{}, nil, fmt.Errorf("building container app bicep: %w", err)
			}
			type compiledBicepParamResult struct {
				TemplateJson   string `json:"templateJson"`
				ParametersJson string `json:"parametersJson"`
			}
			var bicepParamOutput compiledBicepParamResult
			if err := json.Unmarshal([]byte(res.Compiled), &bicepParamOutput); err != nil {
				log.Printf(
					"failed unmarshalling compiled bicepparam (err: %v), template contents:\n%s", err, res.Compiled)
				return nil, nil, fmt.Errorf("failed unmarshalling arm template from json: %w", err)
			}
			var params azure.ArmParameterFile
			if err := json.Unmarshal([]byte(bicepParamOutput.ParametersJson), &params); err != nil {
				log.Printf(
					"failed unmarshalling compiled bicepparam parameters(err: %v), template contents:\n%s",
					err,
					res.Compiled)
				return nil, nil, fmt.Errorf("failed unmarshalling arm parameters template from json: %w", err)
			}
			return azure.RawArmTemplate(bicepParamOutput.TemplateJson), params.Parameters, nil
		}()
		if err != nil {
			return nil, err
		}
		armTemplate = &compiled
		armParams = params

		_, err = at.deploymentService.DeployToResourceGroup(
			ctx,
			at.env.GetSubscriptionId(),
			targetResource.ResourceGroupName(),
			at.deploymentService.GenerateDeploymentName(serviceConfig.Name),
			*armTemplate,
			armParams,
			nil, nil)
		if err != nil {
			return nil, fmt.Errorf("deploying bicep template: %w", err)
		}
	} else {
		containerAppOptions := containerapps.ContainerAppOptions{
			ApiVersion: serviceConfig.ApiVersion,
		}

		err = at.containerAppService.DeployYaml(
			ctx,
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			serviceConfig.Name,
			[]byte(builder.String()),
			&containerAppOptions,
		)
		if err != nil {
			return nil, fmt.Errorf("updating container app service: %w", err)
		}
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for container app service"))

	containerAppTarget := environment.NewTargetResource(
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		serviceConfig.Name,
		string(azapi.AzureResourceTypeContainerApp))

	endpoints, err := at.Endpoints(ctx, serviceConfig, containerAppTarget)
	if err != nil {
		return nil, err
	}

	return &ServiceDeployResult{
		Package: packageOutput,
		TargetResourceId: azure.ContainerAppRID(
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			serviceConfig.Name,
		),
		Kind:      ContainerAppTarget,
		Endpoints: endpoints,
	}, nil
}

// Gets endpoint for the container app service
func (at *dotnetContainerAppTarget) Endpoints(
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

func (fns *containerAppTemplateManifestFuncs) Parameter(name string) (string, error) {
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
