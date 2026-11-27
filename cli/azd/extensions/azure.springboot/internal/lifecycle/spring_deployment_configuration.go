// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"azurespringboot/internal/deploy"
	"azurespringboot/internal/packaging"
	"azurespringboot/internal/springmodel"
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
) (springmodel.ConfigInput, error) {
	input := springmodel.ConfigInput{Deployed: true, Environment: map[string]string{}}
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
		if _, present := input.Environment[name]; present {
			return input, configurationError("the application container has duplicate environment names")
		}
		input.Environment[name] = ""
		if variable.SecretRef != nil {
			if *variable.SecretRef == "" || variable.Value != nil {
				return input, configurationError("an application environment secret reference is empty or ambiguous")
			}
			secretRefs[name] = *variable.SecretRef
		} else if variable.Value != nil {
			input.Environment[name] = *variable.Value
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
		input.Arguments = command
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
		input.JVM = command[1:jar]
		input.Arguments = command[jar+2:]
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
			input.Environment[name] = value
		}
	}
	return input, nil
}

func (r *Runtime) readApplicationSecrets(
	ctx context.Context, binding *deploy.DeploymentBinding,
) (map[string]string, error) {
	appID, err := arm.ParseResourceID(binding.Values[springmodel.ContainerAppIDEnv])
	if err != nil || binding.App == nil {
		return nil, configurationError("the verified application identity is unavailable for secret resolution")
	}
	if err := binding.RequireResource(appID.String()); err != nil {
		return nil, err
	}
	credential, err := deploy.CredentialForSubscription(ctx, r.client, appID.SubscriptionID)
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
	if r.outputs == nil {
		return ctx, values, errors.New("current deployment verification is unavailable for automatic database discovery")
	}
	values["AZURE_ENV_NAME"] = envName
	binding, err := r.outputs.Read(ctx, values)
	if err != nil {
		return ctx, values, err
	}
	if binding == nil || (binding.App == nil && len(binding.Apps) == 0) ||
		(binding.App != nil && binding.App.ID == nil) {
		return ctx, values, configurationError("the current root deployment did not establish the application configuration")
	}
	binding.Automatic = true
	return context.WithValue(ctx, deploy.DeploymentBindingKey{}, binding), binding.Values, nil
}

func (r *Runtime) discoverServiceDatabase(
	ctx context.Context,
	project *azdext.ProjectConfig,
	service *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	values map[string]string,
	requireFinal bool,
) (springmodel.ResolvedDatabase, error) {
	ctx, err := deploy.BindServiceContext(ctx, service.GetName())
	if err != nil {
		return springmodel.ResolvedDatabase{}, err
	}
	intent, err := r.readServiceDatasource(ctx, project, service, serviceContext, requireFinal)
	if err != nil {
		return springmodel.ResolvedDatabase{}, err
	}
	binding, _ := ctx.Value(deploy.DeploymentBindingKey{}).(*deploy.DeploymentBinding)
	return bindDiscoveredDatasource(service, intent, binding, requireFinal)
}

func (r *Runtime) readServiceDatasource(
	ctx context.Context, project *azdext.ProjectConfig, service *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext, requireFinal bool,
) (*springmodel.DiscoveredDatasource, error) {
	if err := packaging.ValidateServiceConfiguration(service); err != nil {
		return nil, err
	}
	root, err := packaging.ServicePath(project, service)
	if err != nil {
		return nil, err
	}
	if err := springmodel.ValidateStandardBootstrap(ctx, root); err != nil {
		return nil, err
	}
	input := springmodel.ConfigInput{
		Root: root, Deployed: true, Environment: map[string]string{}, AllowPending: !requireFinal,
	}
	binding, bound := ctx.Value(deploy.DeploymentBindingKey{}).(*deploy.DeploymentBinding)
	if requireFinal {
		if !bound || binding.App == nil {
			return nil, configurationError(
				"automatic database discovery requires verified current application settings",
			)
		}
		input, err = deployedSpringInput(ctx, binding.App, func(ctx context.Context) (map[string]string, error) {
			return r.readApplicationSecrets(ctx, binding)
		})
		if err != nil {
			return nil, err
		}
		input.Root = root
	}
	if serviceContext != nil {
		input.JarPath, err = packaging.FindSpringJar(serviceContext, root)
		if err != nil {
			return nil, err
		}
	}
	config, err := springmodel.LoadConfiguration(ctx, input)
	if err != nil {
		return nil, err
	}
	var dependencies springmodel.Dependencies
	if packaged := config.PackagedDependencies(); packaged != nil {
		dependencies = *packaged
	} else {
		projectRoot := root
		if project != nil && strings.TrimSpace(project.GetPath()) != "" {
			projectRoot = project.GetPath()
		}
		dependencies, err = springmodel.ReadDependenciesInProject(ctx, projectRoot, root)
		if err != nil {
			return nil, err
		}
	}
	return springmodel.InferDatasource(config, dependencies, requireFinal)
}

func bindDiscoveredDatasource(
	service *azdext.ServiceConfig,
	intent *springmodel.DiscoveredDatasource,
	binding *deploy.DeploymentBinding,
	requireFinal bool,
) (springmodel.ResolvedDatabase, error) {
	result := springmodel.ResolvedDatabase{
		Type: intent.Engine, Authentication: intent.Authentication, Name: intent.Name, Username: intent.Username,
		Discovered:       true,
		SelectionPending: intent.SelectionPending,
	}
	if intent.Engine == springmodel.DatabaseH2 || intent.Engine == springmodel.DatabaseNone {
		return result, nil
	}

	result.ServerDeferred = !requireFinal
	if !requireFinal {
		return result, nil
	}
	if intent.Port != 5432 || !strings.EqualFold(intent.Host, binding.Values[springmodel.PostgresHostEnv]) {
		return springmodel.ResolvedDatabase{}, configurationError(
			"the application datasource does not match the current PostgreSQL server",
		)
	}
	result.ServerResourceID = binding.Values[springmodel.PostgresServerIDEnv]
	if err := springmodel.ValidateAzureResourceID(
		service, "discovered PostgreSQL server", result.ServerResourceID, "Microsoft.DBforPostgreSQL/flexibleServers",
	); err != nil {
		return springmodel.ResolvedDatabase{}, err
	}
	if err := binding.RequireResource(result.ServerResourceID); err != nil {
		return springmodel.ResolvedDatabase{}, err
	}
	if err := binding.RequireResource(result.ServerResourceID + "/databases/" + result.Name); err != nil {
		return springmodel.ResolvedDatabase{}, err
	}
	if output := binding.Values[springmodel.PostgresDatabaseEnv]; output != "" && output != result.Name {
		return springmodel.ResolvedDatabase{}, configurationError(
			"the application database differs from the verified deployment binding",
		)
	}
	binding.Values[springmodel.PostgresDatabaseEnv] = result.Name
	if intent.Authentication == springmodel.DatabaseAuthenticationManagedIdentity {
		if !intent.ClientIDConfigured {
			if binding.App.ID == nil || !deploy.HasSystemAssignedIdentity(binding.App.Identity) ||
				binding.App.Identity.PrincipalID == nil {
				return springmodel.ResolvedDatabase{}, configurationError(
					"the application does not expose the selected system-assigned identity",
				)
			}
			principalID := strings.TrimSpace(*binding.App.Identity.PrincipalID)
			if principalID == "" ||
				!strings.EqualFold(principalID, binding.Values[springmodel.ServiceIdentityPrincipalIDEnv]) {
				return springmodel.ResolvedDatabase{}, configurationError(
					"the application system-assigned identity does not match the current deployment output",
				)
			}
			result.AppIdentity = &springmodel.DatabaseAppIdentity{
				Type:       springmodel.DatabaseAppIdentityTypeSystemAssigned,
				ResourceID: *binding.App.ID,
			}
		} else {
			identityID := binding.Values[springmodel.ServiceIdentityIDEnv]
			if identityID == "" ||
				!strings.EqualFold(intent.ClientID, binding.Values[springmodel.ServiceIdentityClientIDEnv]) ||
				binding.App.Identity == nil {
				return springmodel.ResolvedDatabase{}, configurationError(
					"the application does not select the verified deployed user-assigned identity",
				)
			}
			matched := false
			for resourceID, identity := range binding.App.Identity.UserAssignedIdentities {
				if strings.EqualFold(resourceID, identityID) && identity != nil &&
					identity.ClientID != nil && strings.EqualFold(*identity.ClientID, intent.ClientID) {
					matched = true
				}
			}
			if !matched {
				return springmodel.ResolvedDatabase{}, configurationError(
					"the selected managed identity is not attached to the current application",
				)
			}
			if err := binding.RequireResource(identityID); err != nil {
				return springmodel.ResolvedDatabase{}, err
			}
			result.AppIdentity = &springmodel.DatabaseAppIdentity{
				Type:       springmodel.DatabaseAppIdentityTypeUserAssigned,
				ResourceID: identityID,
			}
		}
	}
	if err := deploy.ValidateProvisionedBindings(result, binding.Values); err != nil {
		return springmodel.ResolvedDatabase{}, err
	}
	return result, nil
}
