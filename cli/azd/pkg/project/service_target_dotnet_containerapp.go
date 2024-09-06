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
	"strconv"
	"strings"
	"text/template"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
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

var bicepForContainerAppFeature = alpha.MustFeatureKey("aspire.useBicepForContainerApps")

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

	useBicepForContainerApps := at.alphaFeatureManager.IsEnabled(bicepForContainerAppFeature)

	var manifest string
	var armTemplate *azure.RawArmTemplate

	appHostRoot := serviceConfig.DotNetContainerApp.AppHostPath
	if f, err := os.Stat(appHostRoot); err == nil && !f.IsDir() {
		appHostRoot = filepath.Dir(appHostRoot)
	}

	if useBicepForContainerApps {
		bicepPath := filepath.Join(
			appHostRoot, "infra", fmt.Sprintf("%s.bicep", serviceConfig.DotNetContainerApp.ProjectName))

		if _, err := os.Stat(bicepPath); err == nil {
			log.Printf("using container app manifest from %s", bicepPath)
			res, err := at.bicepCli.Build(ctx, bicepPath)
			if err != nil {
				return nil, fmt.Errorf("building container app bicep: %w", err)
			}
			armTemplate = to.Ptr(azure.RawArmTemplate(res.Compiled))
		} else {
			log.Printf(
				"generating container app bicep from %s for project %s",
				serviceConfig.DotNetContainerApp.AppHostPath,
				serviceConfig.DotNetContainerApp.ProjectName)

			generatedBicep, err := apphost.BicepModuleForProject(
				serviceConfig.DotNetContainerApp.Manifest,
				serviceConfig.DotNetContainerApp.ProjectName,
				apphost.AppHostOptions{},
			)
			if err != nil {
				return nil, fmt.Errorf("generating container app manifest: %w", err)
			}

			compiled, err := func() (azure.RawArmTemplate, error) {
				f, err := os.CreateTemp(
					"", fmt.Sprintf("azd-bicep-%s-*.bicep", serviceConfig.DotNetContainerApp.ProjectName))
				if err != nil {
					return azure.RawArmTemplate{}, fmt.Errorf("creating temporary file: %w", err)
				}
				defer func() {
					_ = f.Close()
					_ = os.Remove(f.Name())
				}()
				_, err = io.Copy(f, strings.NewReader(generatedBicep))
				if err != nil {
					return azure.RawArmTemplate{}, fmt.Errorf("writing bicep file: %w", err)
				}

				res, err := at.bicepCli.Build(ctx, f.Name())
				if err != nil {
					return azure.RawArmTemplate{}, fmt.Errorf("building container app bicep: %w", err)
				}
				return azure.RawArmTemplate(res.Compiled), nil
			}()
			if err != nil {
				return nil, err
			}
			armTemplate = &compiled
		}
	} else {
		manifestPath := filepath.Join(
			appHostRoot, "infra", fmt.Sprintf("%s.tmpl.yaml", serviceConfig.DotNetContainerApp.ProjectName))

		if _, err := os.Stat(manifestPath); err == nil {
			log.Printf("using container app manifest from %s", manifestPath)

			contents, err := os.ReadFile(manifestPath)
			if err != nil {
				return nil, fmt.Errorf("reading container app manifest: %w", err)
			}
			manifest = string(contents)
		} else {
			log.Printf(
				"generating container app manifest from %s for project %s",
				serviceConfig.DotNetContainerApp.AppHostPath,
				serviceConfig.DotNetContainerApp.ProjectName)

			generatedManifest, err := apphost.ContainerAppManifestTemplateForProject(
				serviceConfig.DotNetContainerApp.Manifest,
				serviceConfig.DotNetContainerApp.ProjectName,
				apphost.AppHostOptions{},
			)
			if err != nil {
				return nil, fmt.Errorf("generating container app manifest: %w", err)
			}
			manifest = generatedManifest
		}
	}

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

	if useBicepForContainerApps {
		var parsed *azure.ArmTemplate
		if err := json.Unmarshal(*armTemplate, &parsed); err != nil {
			return nil, fmt.Errorf("parsing arm template: %w", err)
		}

		// Build the parameters by just evaluating the default value expressions we have and assume that will be good
		// enough.  Perhaps longer term we'll want to support something like the `.params.json` or `.bicepparam` files
		// to allow the user to also assign/override values.
		params := azure.ArmParameters{}
		for name, v := range parsed.Parameters {
			if m, has := v.AzdMetadata(); has && m.DefaultValueExpr != nil {
				tmpl, err := template.New("containerApp.bicep").
					Option("missingkey=error").
					Funcs(funcMap).
					Parse(*m.DefaultValueExpr)
				if err != nil {
					return nil, fmt.Errorf("failing parsing containerApp.tmpl.yaml: %w", err)
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

				// TODO(ellismg): Need to support the other types here - there some logic in bicep_provider.go that we
				// can factor out and share.
				if strings.EqualFold("Int", v.Type) {
					v, err := strconv.Atoi(builder.String())
					if err != nil {
						return nil, fmt.Errorf("converting value to int: %w", err)
					}

					params[name] = azure.ArmParameterValue{
						Value: v,
					}
				} else {
					params[name] = azure.ArmParameterValue{
						Value: builder.String(),
					}
				}
			}
		}

		_, err := at.deploymentService.DeployToResourceGroup(
			ctx,
			at.env.GetSubscriptionId(),
			targetResource.ResourceGroupName(),
			at.deploymentService.GenerateDeploymentName(serviceConfig.Name),
			*armTemplate,
			params,
			nil)
		if err != nil {
			return nil, fmt.Errorf("deploying bicep template: %w", err)
		}
	} else {
		tmpl, err := template.New("containerApp.tmpl.yaml").
			Option("missingkey=error").
			Funcs(funcMap).
			Parse(manifest)
		if err != nil {
			return nil, fmt.Errorf("failing parsing containerApp.tmpl.yaml: %w", err)
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
