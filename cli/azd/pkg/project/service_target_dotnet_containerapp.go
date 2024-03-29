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
	"github.com/azure/azure-dev/cli/azd/pkg/cosmosdb"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/sqldb"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

type dotnetContainerAppTarget struct {
	env                 *environment.Environment
	containerHelper     *ContainerHelper
	containerAppService containerapps.ContainerAppService
	resourceManager     ResourceManager
	dotNetCli           dotnet.DotNetCli
	cosmosDbService     cosmosdb.CosmosDbService
	sqlDbService        sqldb.SqlDbService
	keyvaultService     keyvault.KeyVaultService
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
	cosmosDbService cosmosdb.CosmosDbService,
	sqlDbService sqldb.SqlDbService,
	keyvaultService keyvault.KeyVaultService,
) ServiceTarget {
	return &dotnetContainerAppTarget{
		env:                 env,
		containerHelper:     containerHelper,
		containerAppService: containerAppService,
		resourceManager:     resourceManager,
		dotNetCli:           dotNetCli,
		cosmosDbService:     cosmosDbService,
		sqlDbService:        sqlDbService,
		keyvaultService:     keyvaultService,
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
			dockerCreds, err := at.containerHelper.Credentials(ctx, serviceConfig, targetResource)
			if err != nil {
				task.SetError(fmt.Errorf("logging in to registry: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Pushing container image"))

			var remoteImageName string
			var portNumber int

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

				portNumber, err = at.dotNetCli.PublishContainer(
					ctx,
					serviceConfig.Path(),
					"Release",
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
				cosmosDbService:     at.cosmosDbService,
				sqlDbService:        at.sqlDbService,
				env:                 at.env,
				keyvaultService:     at.keyvaultService,
			}

			tmpl, err := template.New("containerApp.tmpl.yaml").
				Option("missingkey=error").
				Funcs(template.FuncMap{
					"urlHost":          fns.UrlHost,
					"connectionString": fns.ConnectionString,
					"parameter":        fns.Parameter,
					// securedParameter gets a parameter the same way as parameter, but supporting the securedParameter
					// allows to update the logic of pulling secret parameters in the future, if azd changes the way it
					// stores the parameter value.
					"securedParameter": fns.Parameter,
					"secretOutput":     fns.kvSecret,
				}).
				Parse(manifest)
			if err != nil {
				task.SetError(fmt.Errorf("failing parsing containerApp.tmpl.yaml: %w", err))
				return
			}

			var inputs map[string]any
			// inputs are auto-gen during provision and saved to env-config
			if has, err := at.env.Config.GetSection("inputs", &inputs); err != nil {
				task.SetError(fmt.Errorf("failed to get inputs section: %w", err))
				return
			} else if !has {
				inputs = make(map[string]any)
			}

			builder := strings.Builder{}
			err = tmpl.Execute(&builder, struct {
				Env        map[string]string
				Image      string
				Inputs     map[string]any
				TargetPort int
			}{
				Env:        at.env.Dotenv(),
				Image:      remoteImageName,
				Inputs:     inputs,
				TargetPort: portNumber,
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

func (fns *containerAppTemplateManifestFuncs) Parameter(name string) (string, error) {
	val, found := fns.env.Config.Get("infra.parameters." + name)
	if !found {
		return "", fmt.Errorf("parameter %s not found", name)
	}
	valString, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("parameter %s is not a string", name)
	}
	return valString, nil
}

// ConnectionString returns the connection string for the given resource name. Presently, we only support resources of
// type `redis.v0`, `postgres.v0`, `cosmosdb.database.v0`, `azure.sql.database.v0` and `sqlserver.database.v0`.
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
		dbConnString := dbConnectionString{
			Host:     scaffold.ContainerAppName(name),
			Database: "postgres",
			Username: "postgres",
		}

		parentResource := resource.Parent
		if parentResource == nil || *parentResource == "" {
			return "", fmt.Errorf("parent resource not found for db: %s", name)
		}

		parent := fns.manifest.Resources[*parentResource]

		if parent.Type == "postgres.server.v0" {
			dbConnString.Host = scaffold.ContainerAppName(*parentResource)
			dbConnString.Database = name
			password, err := fns.secretValue(dbConnString.Host, "pg-password")
			if err != nil {
				return "", fmt.Errorf("could not determine postgres password: %w", err)
			}
			dbConnString.Password = password
			return dbConnString.String(), nil
		}

		if parent.Type == "container.v0" {
			var ensureDelimiter string
			if !strings.HasSuffix(*parent.ConnectionString, ";") {
				ensureDelimiter = ";"
			}
			rawConnectionString := *parent.ConnectionString + fmt.Sprintf("%sDatabase=%s;", ensureDelimiter, name)
			resolvedConnectionString, err := apphost.EvalString(rawConnectionString, func(expr string) (string, error) {
				return evalBindingRefWithParent(expr, parent, fns.env)
			})
			if err != nil {
				return "", fmt.Errorf("evaluating connection string for %s: %w", name, err)
			}
			return resolvedConnectionString, nil
		}

		return "", fmt.Errorf("connectionString: unsupported parent resource type '%s'", parent.Type)

	case "azure.cosmosdb.account.v0":
		return fns.cosmosConnectionString(name)
	case "azure.cosmosdb.database.v0":
		// get the parent resource name, which is the cosmos account name
		return fns.cosmosConnectionString(*resource.Parent)
	case "azure.sql.v0", "sqlserver.server.v0":
		return fns.sqlConnectionString(name, "")
	case "azure.sql.database.v0", "sqlserver.database.v0":
		parentResource := resource.Parent
		if parentResource == nil || *parentResource == "" {
			return "", fmt.Errorf("parent resource not found for db: %s", name)
		}

		parent := fns.manifest.Resources[*parentResource]
		if parent.Type == "container.v0" {
			var ensureDelimiter string
			if !strings.HasSuffix(*parent.ConnectionString, ";") {
				ensureDelimiter = ";"
			}
			rawConnectionString := *parent.ConnectionString + fmt.Sprintf("%sDatabase=%s;", ensureDelimiter, name)
			resolvedConnectionString, err := apphost.EvalString(rawConnectionString, func(expr string) (string, error) {
				return evalBindingRefWithParent(expr, parent, fns.env)
			})
			if err != nil {
				return "", fmt.Errorf("evaluating connection string for %s: %w", name, err)
			}
			return resolvedConnectionString, nil
		}

		return fns.sqlConnectionString(*resource.Parent, name)
	default:
		return "", fmt.Errorf("connectionString: unsupported resource type '%s'", resource.Type)
	}
}

type dbConnectionString struct {
	Host     string
	Port     string
	Username string
	Password string
	Database string
}

func (db *dbConnectionString) String() string {
	var port string
	if db.Port != "" {
		port = fmt.Sprintf("Port=%s;", db.Port)
	}
	return fmt.Sprintf(
		"Host=%s;%sUsername=%s;Password=%s;Database=%s;",
		db.Host,
		port,
		db.Username,
		db.Password,
		db.Database)
}

// evalBindingRefWithParent evaluates a binding reference expression with the given parent resource. The expression is
// expected to be of the form <resourceName>.<propertyPath> where <resourceName> is the name of a resource in the manifest
// and <propertyPath> is a property path within that resource. The function returns the value of the property, or an error
// if the property is not found or the expression is malformed.
func evalBindingRefWithParent(v string, parent *apphost.Resource, env *environment.Environment) (string, error) {
	expParts := strings.SplitN(v, ".", 2)
	if len(expParts) != 2 {
		return "", fmt.Errorf("malformed binding expression, expected <resourceName>.<propertyPath> but was: %s", v)
	}

	resource, prop := expParts[0], expParts[1]

	// resolve inputs
	if strings.HasPrefix(prop, "inputs.") {
		inputParts := strings.Split(prop[len("inputs."):], ".")

		if len(inputParts) != 1 {
			return "", fmt.Errorf("malformed binding expression, expected inputs.<input-name> but was: %s", v)
		}
		val, found := env.Config.Get(fmt.Sprintf("inputs.%s.%s", resource, inputParts[0]))
		if !found {
			return "", fmt.Errorf("input %s not found", inputParts[0])
		}
		valString, ok := val.(string)
		if !ok {
			return "", fmt.Errorf("input %s is not a string", inputParts[0])
		}
		return valString, nil
	}

	if strings.HasPrefix(prop, "bindings.") {
		bindParts := strings.Split(prop[len("bindings."):], ".")

		if len(bindParts) != 2 {
			return "", fmt.Errorf("malformed binding expression, expected "+
				"bindings.<binding-name>.<property> but was: %s", v)
		}

		binding := *parent.Bindings[bindParts[0]]
		switch bindParts[1] {
		case "host":
			// The host name matches the containerapp name, so we can just return the resource name.
			return resource, nil
		case "port":
			return fmt.Sprintf(`%d`, *binding.ContainerPort), nil
		case "url":
			var urlFormatString string

			if binding.External {
				urlFormatString = "%s://%s.{{ .Env.AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN }}"
			} else {
				urlFormatString = "%s://%s.internal.{{ .Env.AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN }}"
			}

			return fmt.Sprintf(urlFormatString, binding.Scheme, resource), nil
		default:
			return "",
				fmt.Errorf("malformed binding expression, expected bindings.<binding-name>.[host|port|url] but was: %s", v)
		}
	}
	return "", fmt.Errorf(
		"malformed binding expression, expected inputs.<input-name> or bindings.<binding-name>.<property> but was: %s", v)
}

func (fns *containerAppTemplateManifestFuncs) cosmosConnectionString(accountName string) (string, error) {
	// cosmos account name can be defined with a resourceToken during provisioning
	// the final name is expected to be output as SERVICE_BINDING_{accountName}_NAME
	accountNameKey := fmt.Sprintf("SERVICE_BINDING_%s_NAME", scaffold.AlphaSnakeUpper(accountName))
	resourceName := fns.env.Getenv(accountNameKey)
	if resourceName == "" {
		return "", fmt.Errorf("The value for SERVICE_BINDING_%s_NAME was not found or is empty.", accountName)
	}

	return fns.cosmosDbService.ConnectionString(
		fns.ctx,
		fns.targetResource.SubscriptionId(),
		fns.targetResource.ResourceGroupName(),
		resourceName)
}

func (fns *containerAppTemplateManifestFuncs) sqlConnectionString(serverName, sqlDbName string) (string, error) {
	serverNameKey := fmt.Sprintf("SERVICE_BINDING_%s_NAME", scaffold.AlphaSnakeUpper(serverName))
	resourceName := fns.env.Getenv(serverNameKey)
	if resourceName == "" {
		return "", fmt.Errorf("the value for SERVICE_BINDING_%s_NAME was not found or is empty", serverName)
	}

	return fns.sqlDbService.ConnectionString(
		fns.ctx,
		fns.targetResource.SubscriptionId(),
		fns.targetResource.ResourceGroupName(),
		resourceName,
		sqlDbName)
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
