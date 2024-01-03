// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/containerapps"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/password"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

type dotnetContainerAppTarget struct {
	env                 *environment.Environment
	containerHelper     *ContainerHelper
	containerAppService containerapps.ContainerAppService
	resourceManager     ResourceManager
	dotNetCli           dotnet.DotNetCli
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
	dotNetCli dotnet.DotNetCli,
) ServiceTarget {
	return &dotnetContainerAppTarget{
		env:                 env,
		containerHelper:     containerHelper,
		containerAppService: containerAppService,
		resourceManager:     resourceManager,
		dotNetCli:           dotNetCli,
	}
}

// Gets the required external tools
func (at *dotnetContainerAppTarget) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
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
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			task.SetResult(packageOutput)
		},
	)
}

// Deploys service container images to ACR and provisions the container app service.
func (at *dotnetContainerAppTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress]) {
			if err := at.validateTargetResource(ctx, serviceConfig, targetResource); err != nil {
				task.SetError(fmt.Errorf("validating target resource: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Logging in to registry"))

			// Login, tag & push container image to ACR
			dockerCreds, err := at.containerHelper.Credentials(ctx, targetResource)
			if err != nil {
				task.SetError(fmt.Errorf("logging in to registry: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Pushing container image"))

			var remoteImageName string

			if serviceConfig.Language == ServiceLanguageDocker {
				containerDeployTask := at.containerHelper.Deploy(ctx, serviceConfig, packageOutput, targetResource, false)
				syncProgress(task, containerDeployTask.Progress())

				res, err := containerDeployTask.Await()
				if err != nil {
					task.SetError(err)
					return
				}

				remoteImageName = res.Details.(*dockerDeployResult).RemoteImageTag
			} else {
				imageName := fmt.Sprintf("azd-deploy-%s-%d", serviceConfig.Name, time.Now().Unix())

				err = at.dotNetCli.PublishContainer(
					ctx,
					serviceConfig.Path(),
					"Debug",
					imageName,
					dockerCreds.LoginServer,
					dockerCreds.Username,
					dockerCreds.Password)
				if err != nil {
					task.SetError(fmt.Errorf("publishing container: %w", err))
					return
				}

				remoteImageName = fmt.Sprintf("%s/%s", dockerCreds.LoginServer, imageName)
			}

			task.SetProgress(NewServiceProgress("Updating container app"))

			var manifest string

			projectRoot := serviceConfig.Path()
			if f, err := os.Stat(projectRoot); err == nil && !f.IsDir() {
				projectRoot = filepath.Dir(projectRoot)
			}

			manifestPath := filepath.Join(projectRoot, "manifests", "containerApp.tmpl.yaml")
			if _, err := os.Stat(manifestPath); err == nil {
				log.Printf("using container app manifest from %s", manifestPath)

				contents, err := os.ReadFile(filepath.Join(projectRoot, "manifests", "containerApp.tmpl.yaml"))
				if err != nil {
					task.SetError(fmt.Errorf("reading container app manifest: %w", err))
					return
				}
				manifest = string(contents)
			} else {
				log.Printf(
					"generating container app manifest from %s for project %s",
					serviceConfig.DotNetContainerApp.ProjectPath,
					serviceConfig.DotNetContainerApp.ProjectName)

				generatedManifest, err := apphost.ContainerAppManifestTemplateForProject(
					serviceConfig.DotNetContainerApp.Manifest,
					serviceConfig.DotNetContainerApp.ProjectName,
				)
				if err != nil {
					task.SetError(fmt.Errorf("generating container app manifest: %w", err))
					return
				}
				manifest = generatedManifest
			}

			fns := &containerAppTemplateManifestFuncs{
				ctx:                 ctx,
				manifest:            serviceConfig.DotNetContainerApp.Manifest,
				targetResource:      targetResource,
				containerAppService: at.containerAppService,
			}

			tmpl, err := template.New("containerApp.tmpl.yaml").
				Option("missingkey=error").
				Funcs(template.FuncMap{
					"urlHost":          fns.UrlHost,
					"connectionString": fns.ConnectionString,
				}).
				Parse(manifest)
			if err != nil {
				task.SetError(fmt.Errorf("failing parsing containerApp.tmpl.yaml: %w", err))
				return
			}

			requiredInputs, err := apphost.Inputs(serviceConfig.DotNetContainerApp.Manifest)
			if err != nil {
				task.SetError(fmt.Errorf("failed to get required inputs: %w", err))
			}

			wroteNewInput := false

			for inputName, inputInfo := range requiredInputs {
				inputConfigKey := fmt.Sprintf("inputs.%s", inputName)

				if _, has := at.env.Config.GetString(inputConfigKey); !has {
					// No value found, so we need to generate one, and store it in the config bag.
					//
					// TODO(ellismg): Today this dereference is safe because when loading a manifest we validate that every
					// input has a generate block with a min length property.  We would like to relax this in Preview 3 to
					// to support cases where this is not the case (and we'd prompt for the value).  When we do that, we'll
					// need to audit these dereferences to check for nil.
					val, err := password.FromAlphabet(password.LettersAndDigits, *inputInfo.Default.Generate.MinLength)
					if err != nil {
						task.SetError(fmt.Errorf("generating value for input %s: %w", inputName, err))
						return

					}

					if err := at.env.Config.Set(inputConfigKey, val); err != nil {
						task.SetError(fmt.Errorf("saving value for input %s: %w", inputName, err))
						return
					}

					wroteNewInput = true
				}
			}

			if wroteNewInput {
				if err := at.containerHelper.envManager.Save(ctx, at.env); err != nil {
					task.SetError(fmt.Errorf("saving environment: %w", err))
					return
				}
			}

			var inputs map[string]any

			if has, err := at.env.Config.GetSection("inputs", &inputs); err != nil {
				task.SetError(fmt.Errorf("failed to get inputs section: %w", err))
				return
			} else if !has {
				inputs = make(map[string]any)
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
				task.SetError(fmt.Errorf("failed executing template file: %w", err))
				return
			}

			err = at.containerAppService.DeployYaml(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				serviceConfig.Name,
				[]byte(builder.String()),
			)
			if err != nil {
				task.SetError(fmt.Errorf("updating container app service: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Fetching endpoints for container app service"))

			containerAppTarget := environment.NewTargetResource(
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				serviceConfig.Name,
				string(infra.AzureResourceTypeContainerApp))

			endpoints, err := at.Endpoints(ctx, serviceConfig, containerAppTarget)
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetResult(&ServiceDeployResult{
				Package: packageOutput,
				TargetResourceId: azure.ContainerAppRID(
					targetResource.SubscriptionId(),
					targetResource.ResourceGroupName(),
					serviceConfig.Name,
				),
				Kind:      ContainerAppTarget,
				Endpoints: endpoints,
			})
		},
	)
}

// Gets endpoint for the container app service
func (at *dotnetContainerAppTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	if ingressConfig, err := at.containerAppService.GetIngressConfiguration(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
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
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) error {
	if targetResource.ResourceGroupName() == "" {
		return fmt.Errorf("missing resource group name: %s", targetResource.ResourceGroupName())
	}

	if targetResource.ResourceType() != "" {
		if err := checkResourceType(targetResource, infra.AzureResourceTypeContainerAppEnvironment); err != nil {
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

// ConnectionString returns the connection string for the given resource name. Presently, we only support resources of
// type `redis.v0` and `postgres.v0`.
//
// It is callable from a template under the name `connectionString`.
func (fns *containerAppTemplateManifestFuncs) ConnectionString(name string) (string, error) {
	resource, has := fns.manifest.Resources[name]
	if !has {
		return "", fmt.Errorf("resource %s not found in manifest", name)
	}

	switch resource.Type {
	case "redis.v0":
		targetContainerName := scaffold.ContainerAppName(name)

		cfg, err := fns.secretValue(targetContainerName, "redis-config")
		if err != nil {
			return "", fmt.Errorf("could not determine redis password: %w", err)
		}

		for _, line := range strings.Split(cfg, "\n") {
			if strings.HasPrefix(line, "requirepass ") {
				password := strings.TrimPrefix(line, "requirepass ")
				return fmt.Sprintf("%s:6379,password=%s", targetContainerName, password), nil
			}
		}

		return "", fmt.Errorf("could not determine redis password: no requirepass line found in redis-config")

	case "postgres.database.v0":
		targetContainerName := scaffold.ContainerAppName(name)

		password, err := fns.secretValue(targetContainerName, "pg-password")
		if err != nil {
			return "", fmt.Errorf("could not determine postgres password: %w", err)
		}

		return fmt.Sprintf("Host=%s;Database=postgres;Username=postgres;Password=%s", targetContainerName, password), nil
	default:
		return "", fmt.Errorf("connectionString: unsupported resource type '%s'", resource.Type)
	}
}

// secretValue returns the value of the secret with the given name, or an error if the secret is not found. A nil value
// is returned as "", without an error.
func (fns *containerAppTemplateManifestFuncs) secretValue(containerAppName string, secretName string) (string, error) {
	secrets, err := fns.containerAppService.ListSecrets(
		fns.ctx, fns.targetResource.SubscriptionId(), fns.targetResource.ResourceGroupName(), containerAppName)
	if err != nil {
		return "", fmt.Errorf("fetching %s secrets: %w", containerAppName, err)
	}

	for _, secret := range secrets {
		if secret.Name != nil && *secret.Name == secretName {
			if secret.Value == nil {
				return "", nil
			}

			return *secret.Value, nil
		}
	}

	return "", fmt.Errorf("secret %s not found", secretName)
}
