// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func deployedSpringInput(
	ctx context.Context,
	app *armappcontainers.ContainerApp,
	loadSecrets func(context.Context) (map[string]string, error),
) (springConfigInput, error) {
	input := springConfigInput{deployed: true, environment: map[string]string{}}
	if app == nil || app.Properties == nil || app.Properties.Template == nil ||
		len(app.Properties.Template.Containers) != 1 || app.Properties.Template.Containers[0] == nil {
		return input, configurationError("automatic discovery requires one verified application container")
	}
	if configuration := app.Properties.Configuration; configuration != nil && configuration.ActiveRevisionsMode != nil &&
		*configuration.ActiveRevisionsMode != armappcontainers.ActiveRevisionsModeSingle {
		return input, configurationError("multiple active Container App revisions require additional runtime evidence")
	}
	container := app.Properties.Template.Containers[0]
	if len(container.VolumeMounts) != 0 {
		return input, configurationError("mounted application files require verified external configuration evidence")
	}
	secretRefs := map[string]string{}
	for _, variable := range container.Env {
		if variable == nil || variable.Name == nil || *variable.Name == "" {
			return input, configurationError("the application container returned an invalid environment entry")
		}
		name := *variable.Name
		if _, present := input.environment[name]; present {
			return input, configurationError("the application container has duplicate environment names")
		}
		input.environment[name] = ""
		if variable.SecretRef != nil {
			if *variable.SecretRef == "" || variable.Value != nil {
				return input, configurationError("an application environment secret reference is empty or ambiguous")
			}
			secretRefs[name] = *variable.SecretRef
		} else if variable.Value != nil {
			input.environment[name] = *variable.Value
		}
	}
	arguments := []*string{}
	if len(container.Command) != 0 {
		arguments = append(arguments, container.Command...)
	}
	arguments = append(arguments, container.Args...)
	var command []string
	for _, argument := range arguments {
		if argument == nil {
			return input, configurationError("the application container returned an invalid command argument")
		}
		command = append(command, *argument)
	}
	if len(container.Command) == 0 {
		input.arguments = command
	} else {
		if len(command) < 3 || command[0] != "java" {
			return input, configurationError(
				"custom application launch scripts cannot establish effective Spring configuration",
			)
		}
		jar := slices.Index(command, "-jar")
		if jar < 1 || jar+1 >= len(command) || command[jar+1] != "/app/application.jar" {
			return input, configurationError(
				"the container launch command does not select the verified packaged application",
			)
		}
		input.jvm = command[1:jar]
		input.arguments = command[jar+2:]
	}
	if len(secretRefs) != 0 {
		if loadSecrets == nil {
			return input, configurationError("the deployed application references secrets that cannot be resolved")
		}
		secrets, err := loadSecrets(ctx)
		if err != nil {
			return input, err
		}
		for name, reference := range secretRefs {
			value, present := secrets[reference]
			if !present {
				return input, configurationError("a deployed application secret reference is unresolved")
			}
			input.environment[name] = value
		}
	}
	return input, nil
}

func (r *Runtime) readApplicationSecrets(
	ctx context.Context, binding *deploymentBinding,
) (map[string]string, error) {
	appID, err := arm.ParseResourceID(binding.values[containerAppIDEnv])
	if err != nil || binding.app == nil {
		return nil, configurationError("the verified application identity is unavailable for secret resolution")
	}
	if err := binding.requireResource(appID.String()); err != nil {
		return nil, err
	}
	credential, err := credentialForSubscription(ctx, r.client, appID.SubscriptionID)
	if err != nil {
		return nil, err
	}
	apps, err := armappcontainers.NewContainerAppsClient(appID.SubscriptionID, credential, nil)
	if err != nil {
		return nil, err
	}
	response, err := apps.ListSecrets(ctx, appID.ResourceGroupName, appID.Name, nil)
	if err != nil {
		return nil, errors.Join(
			configurationError("the deployed application secrets could not be read"), nonDisclosureError(err),
		)
	}
	result := map[string]string{}
	for _, secret := range response.Value {
		if secret == nil || secret.Name == nil || secret.Value == nil {
			continue
		}
		if _, duplicate := result[*secret.Name]; duplicate {
			return nil, configurationError("the deployed application returned duplicate secrets")
		}
		result[*secret.Name] = *secret.Value
	}
	return result, nil
}

func (r *Runtime) bindAutomaticProject(
	ctx context.Context, project *azdext.ProjectConfig, envName string, values map[string]string,
) (context.Context, map[string]string, error) {
	services := springServices(project)
	if len(services) == 0 {
		return ctx, values, nil
	}
	for _, service := range services {
		if err := validateAutomaticServiceConfig(service); err != nil {
			return ctx, values, err
		}
	}
	if r.outputs == nil {
		return ctx, values, errors.New("current deployment verification is unavailable for automatic database discovery")
	}
	values["AZURE_ENV_NAME"] = envName
	binding, err := r.outputs.Read(ctx, values)
	if err != nil {
		return ctx, values, err
	}
	if binding == nil || binding.app == nil || binding.app.ID == nil {
		return ctx, values, configurationError("the current root deployment did not establish the application configuration")
	}
	binding.automatic = true
	return context.WithValue(ctx, deploymentBindingKey{}, binding), binding.values, nil
}

func (r *Runtime) discoverServiceDatabase(
	ctx context.Context,
	project *azdext.ProjectConfig,
	service *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	values map[string]string,
	requireFinal bool,
) (resolvedDatabase, error) {
	intent, err := r.readServiceDatasource(ctx, project, service, serviceContext, requireFinal)
	if err != nil {
		return resolvedDatabase{}, err
	}
	binding, _ := ctx.Value(deploymentBindingKey{}).(*deploymentBinding)
	return bindDiscoveredDatasource(service, intent, binding, requireFinal)
}

func (r *Runtime) readServiceDatasource(
	ctx context.Context, project *azdext.ProjectConfig, service *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext, requireFinal bool,
) (*discoveredDatasource, error) {
	if err := validateAutomaticServiceConfig(service); err != nil {
		return nil, err
	}
	root, err := servicePath(project, service)
	if err != nil {
		return nil, err
	}
	if err := validateStandardSpringBootstrap(ctx, root); err != nil {
		return nil, err
	}
	input := springConfigInput{
		root: root, deployed: true, environment: map[string]string{}, allowPending: !requireFinal,
	}
	binding, bound := ctx.Value(deploymentBindingKey{}).(*deploymentBinding)
	if requireFinal {
		if !bound || binding.app == nil {
			return nil, configurationError(
				"automatic database discovery requires verified current application settings",
			)
		}
		input, err = deployedSpringInput(ctx, binding.app, func(ctx context.Context) (map[string]string, error) {
			return r.readApplicationSecrets(ctx, binding)
		})
		if err != nil {
			return nil, err
		}
		input.root = root
	}
	if serviceContext != nil {
		input.jarPath, err = findMavenJar(serviceContext)
		if err != nil {
			return nil, err
		}
	}
	config, err := loadSpringConfiguration(ctx, input)
	if err != nil {
		return nil, err
	}
	var dependencies springDependencies
	if config.packaged != nil {
		dependencies = *config.packaged
	} else {
		dependencies, err = readSpringDependencies(ctx, root)
		if err != nil {
			return nil, err
		}
	}
	return inferSpringDatasource(config, dependencies, requireFinal)
}

func bindDiscoveredDatasource(
	service *azdext.ServiceConfig, intent *discoveredDatasource, binding *deploymentBinding, requireFinal bool,
) (resolvedDatabase, error) {
	result := resolvedDatabase{
		Type: intent.engine, Authentication: intent.authentication, Name: intent.name, Username: intent.username,
		discovered: true, appPassword: intent.password,
		selectionPending: intent.selectionPending,
	}
	if intent.engine == databaseH2 {
		return result, nil
	}

	result.ServerDeferred = !requireFinal
	if !requireFinal {
		return result, nil
	}
	if intent.port != 5432 || !strings.EqualFold(intent.host, binding.values[postgresHostEnv]) {
		return resolvedDatabase{}, configurationError(
			"the application datasource does not match the current PostgreSQL server",
		)
	}
	result.ServerResourceID = binding.values[postgresServerIDEnv]
	if err := validateAzureResourceID(
		service, "discovered PostgreSQL server", result.ServerResourceID, "Microsoft.DBforPostgreSQL/flexibleServers",
	); err != nil {
		return resolvedDatabase{}, err
	}
	if err := binding.requireResource(result.ServerResourceID); err != nil {
		return resolvedDatabase{}, err
	}
	if err := binding.requireResource(result.ServerResourceID + "/databases/" + result.Name); err != nil {
		return resolvedDatabase{}, err
	}
	if output := binding.values[postgresDatabaseEnv]; output != "" && output != result.Name {
		return resolvedDatabase{}, configurationError(
			"the application database differs from the verified deployment binding",
		)
	}
	binding.values[postgresDatabaseEnv] = result.Name
	if intent.authentication == databaseAuthenticationManagedIdentity {
		identityID := binding.values[serviceIdentityIDEnv]
		if identityID == "" || !strings.EqualFold(intent.clientID, binding.values[serviceIdentityClientIDEnv]) ||
			binding.app.Identity == nil {
			return resolvedDatabase{}, configurationError(
				"the application does not select the verified deployed user-assigned identity",
			)
		}
		matched := false
		for resourceID, identity := range binding.app.Identity.UserAssignedIdentities {
			if strings.EqualFold(resourceID, identityID) && identity != nil &&
				identity.ClientID != nil && strings.EqualFold(*identity.ClientID, intent.clientID) {
				matched = true
			}
		}
		if !matched {
			return resolvedDatabase{}, configurationError(
				"the selected managed identity is not attached to the current application",
			)
		}
		if err := binding.requireResource(identityID); err != nil {
			return resolvedDatabase{}, err
		}
		result.AppIdentity = &databaseAppIdentity{Type: databaseAppIdentityTypeUserAssigned, ResourceID: identityID}
	}
	if err := validateProvisionedBindings(result, binding.values); err != nil {
		return resolvedDatabase{}, err
	}
	return result, nil
}
