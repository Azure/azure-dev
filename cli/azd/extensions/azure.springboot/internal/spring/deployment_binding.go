// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type deploymentBinding struct {
	id          string
	resources   map[string]bool
	values      map[string]string
	outputs     currentDeploymentOutputs
	original    bool
	automatic   bool
	outboundIPs []string
	app         *armappcontainers.ContainerApp
}

type deploymentBindingKey struct{}

func (b *deploymentBinding) requireResource(resourceID string) error {
	if !b.resources[strings.ToLower(resourceID)] {
		return validationError(
			"resource_deployment_unverified",
			"the selected resource is not a member of the current root deployment",
			"restore the current deployment outputs or reprovision the selected environment",
		)
	}
	return nil
}

func checkResourceOwnership(
	ctx context.Context,
	client *azdext.AzdClient,
	envName string,
	values map[string]string,
	resourceID string,
	tags map[string]*string,
) error {
	for key, value := range tags {
		if strings.EqualFold(key, azdEnvironmentTag) &&
			(value == nil || !strings.EqualFold(*value, envName)) {
			return validationError(
				"resource_environment_conflict",
				"the selected resource belongs to another azd environment",
				"select the original environment; conflicting ownership tags cannot be overridden",
			)
		}
	}
	if binding, ok := ctx.Value(deploymentBindingKey{}).(*deploymentBinding); ok {
		return binding.requireResource(resourceID)
	}
	if client == nil {
		return errors.New("deployment ownership verification is unavailable")
	}
	binding, err := readDeploymentBinding(ctx, client, envName, values)
	if err != nil {
		return err
	}
	return binding.requireResource(resourceID)
}

func readDeploymentBinding(
	ctx context.Context,
	client *azdext.AzdClient,
	envName string,
	values map[string]string,
) (*deploymentBinding, error) {
	response, err := client.Deployment().GetDeployment(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, fmt.Errorf("reading the selected root deployment: %w", nonDisclosureError(err))
	}
	selected := response.GetDeployment()
	if selected == nil || selected.GetId() == "" {
		return nil, errors.New("the selected root deployment is unavailable")
	}
	identity, err := arm.ParseResourceID(selected.GetId())
	if err != nil || !strings.EqualFold(identity.ResourceType.String(), "Microsoft.Resources/deployments") ||
		!strings.EqualFold(identity.SubscriptionID, values[azureSubscriptionIDEnv]) {
		return nil, errors.New("the selected root deployment has an invalid subscription or resource identity")
	}
	project, err := client.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, fmt.Errorf("reading the selected project: %w", err)
	}
	if project.GetProject() == nil || project.GetProject().GetName() == "" || envName == "" {
		return nil, errors.New("the selected project or environment identity is unavailable")
	}
	credential, err := credentialForSubscription(ctx, client, identity.SubscriptionID)
	if err != nil {
		return nil, err
	}
	deployments, err := armresources.NewDeploymentsClient(identity.SubscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("creating deployment reader: %w", err)
	}
	var record armresources.DeploymentExtended
	if identity.ResourceGroupName == "" {
		result, readErr := deployments.GetAtSubscriptionScope(ctx, identity.Name, nil)
		if readErr != nil {
			return nil, fmt.Errorf("reading root subscription deployment: %w", nonDisclosureError(readErr))
		}
		record = result.DeploymentExtended
	} else {
		result, readErr := deployments.Get(ctx, identity.ResourceGroupName, identity.Name, nil)
		if readErr != nil {
			return nil, fmt.Errorf("reading root resource-group deployment: %w", nonDisclosureError(readErr))
		}
		record = result.DeploymentExtended
	}
	if record.ID == nil || !strings.EqualFold(*record.ID, selected.GetId()) ||
		record.Properties == nil || record.Properties.ProvisioningState == nil ||
		*record.Properties.ProvisioningState != armresources.ProvisioningStateSucceeded ||
		!tagEqualsPointers(record.Tags, "azd-env-name", envName) ||
		!tagEqualsPointers(record.Tags, "azd-project-name", project.GetProject().GetName()) {
		return nil, errors.New("the current root deployment is not successful and owned by this project and environment")
	}
	outputs, err := decodeDeploymentOutputs(record.Properties.Outputs)
	if err != nil {
		return nil, err
	}
	normalized, err := boundDeploymentValues(values, outputs)
	if err != nil {
		return nil, err
	}
	binding := &deploymentBinding{
		id: selected.GetId(), values: normalized, outputs: outputs, resources: map[string]bool{},
	}
	_, binding.original, err = findDeploymentOutput(outputs, "DATABASE_SERVER_ID")
	if err != nil {
		return nil, err
	}
	if err := collectDeploymentResources(
		ctx, credential, identity, normalized[azureResourceGroupEnv], binding.resources,
	); err != nil {
		return nil, err
	}
	binding.automatic = true
	if err := bindApplication(ctx, credential, project.GetProject(), binding); err != nil {
		return nil, err
	}
	return binding, nil
}

func boundDeploymentValues(
	values map[string]string,
	outputs currentDeploymentOutputs,
) (map[string]string, error) {
	result := make(map[string]string, len(values)+8)
	maps.Copy(result, values)
	_, original, err := findDeploymentOutput(outputs, "DATABASE_SERVER_ID")
	if err != nil {
		return nil, err
	}
	if !original {
		return result, nil
	}
	for _, binding := range []struct{ authored, internal string }{
		{"AZURE_RESOURCE_GROUP", azureResourceGroupEnv},
		{"DATABASE_SERVER_ID", postgresServerIDEnv},
		{"DATABASE_HOST", postgresHostEnv},
		{"CONTAINER_APP_ENVIRONMENT_ID", "CONTAINER_APP_ENVIRONMENT_ID"},
		{"AZURE_CONTAINER_REGISTRY_ENDPOINT", "AZURE_CONTAINER_REGISTRY_ENDPOINT"},
	} {
		output, present, err := findDeploymentOutput(outputs, binding.authored)
		if err != nil {
			return nil, err
		}
		value, stringValue := output.Value.(string)
		if !present || !stringValue || !strings.EqualFold(output.Type, "string") ||
			strings.TrimSpace(value) == "" || !strings.EqualFold(value, values[binding.authored]) {
			return nil, fmt.Errorf("%s was not emitted consistently by the current root deployment", binding.authored)
		}
		if existing := values[binding.internal]; existing != "" && !strings.EqualFold(existing, value) {
			return nil, fmt.Errorf("%s conflicts with the selected root deployment", binding.internal)
		}
		result[binding.internal] = value
	}
	for _, binding := range []struct{ authored, internal string }{
		{"APPLICATION_IDENTITY_ID", serviceIdentityIDEnv},
		{"APPLICATION_IDENTITY_CLIENT_ID", serviceIdentityClientIDEnv},
		{"APPLICATION_PRINCIPAL_ID", serviceIdentityPrincipalIDEnv},
	} {
		output, present, err := findDeploymentOutput(outputs, binding.authored)
		if err != nil {
			return nil, err
		}
		if !present {
			delete(result, binding.internal)
			delete(result, binding.authored)
			continue
		}
		value, stringValue := output.Value.(string)
		if !stringValue || !strings.EqualFold(output.Type, "string") ||
			strings.TrimSpace(value) == "" || !strings.EqualFold(value, values[binding.authored]) {
			return nil, fmt.Errorf("%s was not emitted consistently by the current root deployment", binding.authored)
		}
		result[binding.internal] = value
	}
	server, err := arm.ParseResourceID(result[postgresServerIDEnv])
	if err != nil {
		return nil, err
	}
	result[postgresServerNameEnv] = server.Name
	// The legacy output contract has no database-name output; persisted selections are not deployment evidence.
	delete(result, postgresDatabaseEnv)
	return result, nil
}

func collectDeploymentResources(
	ctx context.Context,
	credential azcore.TokenCredential,
	root *arm.ResourceID,
	resourceGroup string,
	resources map[string]bool,
) error {
	client, err := armresources.NewDeploymentOperationsClient(root.SubscriptionID, credential, nil)
	if err != nil {
		return err
	}
	return collectDeploymentResourcesWithClient(ctx, client, root, resourceGroup, resources)
}

func collectDeploymentResourcesWithClient(
	ctx context.Context,
	client *armresources.DeploymentOperationsClient,
	root *arm.ResourceID,
	resourceGroup string,
	resources map[string]bool,
) error {
	queue := []*arm.ResourceID{root}
	visited := map[string]bool{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if visited[strings.ToLower(current.String())] {
			continue
		}
		if len(visited) >= 128 {
			return errors.New("deployment resource traversal exceeded its bounded nested-deployment limit")
		}
		visited[strings.ToLower(current.String())] = true
		consume := func(operations []*armresources.DeploymentOperation) error {
			for _, operation := range operations {
				if operation == nil || operation.Properties == nil || operation.Properties.TargetResource == nil ||
					operation.Properties.TargetResource.ID == nil {
					continue
				}
				target, parseErr := arm.ParseResourceID(*operation.Properties.TargetResource.ID)
				if parseErr != nil || !strings.EqualFold(target.SubscriptionID, root.SubscriptionID) ||
					!strings.EqualFold(target.ResourceGroupName, resourceGroup) {
					return errors.New("a nested deployment resource falls outside the verified resource scope")
				}
				if strings.EqualFold(target.ResourceType.String(), "Microsoft.Resources/deployments") {
					queue = append(queue, target)
				} else if operation.Properties.ProvisioningOperation != nil &&
					*operation.Properties.ProvisioningOperation == armresources.ProvisioningOperationCreate {
					key := strings.ToLower(target.String())
					if !resources[key] && len(resources) >= 10000 {
						return errors.New("deployment resource traversal exceeded its resource limit")
					}
					resources[key] = true
				}
			}
			return nil
		}
		if current.ResourceGroupName == "" {
			pager := client.NewListAtSubscriptionScopePager(current.Name, nil)
			for pager.More() {
				page, pageErr := pager.NextPage(ctx)
				if pageErr != nil {
					return fmt.Errorf("reading root deployment resources: %w", nonDisclosureError(pageErr))
				}
				if err := consume(page.Value); err != nil {
					return err
				}
			}
		} else {
			pager := client.NewListPager(current.ResourceGroupName, current.Name, nil)
			for pager.More() {
				page, pageErr := pager.NextPage(ctx)
				if pageErr != nil {
					return fmt.Errorf("reading nested deployment resources: %w", nonDisclosureError(pageErr))
				}
				if err := consume(page.Value); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func bindApplication(
	ctx context.Context,
	credential azcore.TokenCredential,
	project *azdext.ProjectConfig,
	binding *deploymentBinding,
) error {
	apps, err := armappcontainers.NewContainerAppsClient(binding.values[azureSubscriptionIDEnv], credential, nil)
	if err != nil {
		return err
	}
	candidates := []string{}
	for resourceID := range binding.resources {
		identity, err := arm.ParseResourceID(resourceID)
		if err != nil || !strings.EqualFold(identity.ResourceType.String(), containerAppType) {
			continue
		}
		app, err := apps.Get(ctx, identity.ResourceGroupName, identity.Name, nil)
		if err != nil {
			return fmt.Errorf("reading deployment application: %w", nonDisclosureError(err))
		}
		for _, service := range springServices(project) {
			if tagEqualsPointers(app.Tags, azdServiceNameTag, service.GetName()) {
				if expected := binding.values["CONTAINER_APP_ENVIRONMENT_ID"]; expected != "" &&
					!matchesContainerAppEnvironment(app.Properties, expected) {
					return errors.New("the service application belongs to a different managed environment")
				}
				if app.ID == nil || !strings.EqualFold(*app.ID, resourceID) || app.Properties == nil {
					return errors.New("the current service application returned an inconsistent resource identity")
				}
				if err := checkResourceOwnership(
					context.WithValue(ctx, deploymentBindingKey{}, binding), nil,
					binding.values["AZURE_ENV_NAME"], binding.values, resourceID, app.Tags,
				); err != nil {
					return err
				}
				candidates = append(candidates, resourceID)
				binding.app = &app.ContainerApp
			}
		}
	}
	if len(candidates) != 1 {
		return errors.New("the current deployment must identify exactly one matching Spring application")
	}
	if persisted := binding.values[containerAppIDEnv]; persisted != "" &&
		!strings.EqualFold(persisted, candidates[0]) {
		return errors.New("the saved Container App identity conflicts with the current deployment")
	}
	binding.values[containerAppIDEnv] = candidates[0]
	return nil
}

func matchesContainerAppEnvironment(properties *armappcontainers.ContainerAppProperties, expected string) bool {
	if properties == nil || expected == "" {
		return false
	}
	actual := properties.EnvironmentID
	if actual == nil {
		actual = properties.ManagedEnvironmentID
	}
	if actual == nil || !strings.EqualFold(*actual, expected) {
		return false
	}
	return properties.ManagedEnvironmentID == nil || strings.EqualFold(*properties.ManagedEnvironmentID, expected)
}
