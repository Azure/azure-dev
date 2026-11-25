// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package deploy

import (
	"context"
	"strings"

	"azurespringboot/internal/exterrors"
	"azurespringboot/internal/packaging"
	"azurespringboot/internal/springmodel"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	// AzdEnvironmentTag is the ARM tag recording the owning azd environment name.
	AzdEnvironmentTag = azdEnvironmentTag
	// AzdServiceNameTag is the ARM tag recording the owning azd service name.
	AzdServiceNameTag = azdServiceNameTag
)

func validationError(code, message, suggestion string) error {
	return exterrors.Validation(code, message, suggestion)
}

func configurationError(message string) error {
	return exterrors.Configuration(message)
}

func nonDisclosureError(err error) error {
	return exterrors.NonDisclosureError(err)
}

// ServiceValidator validates a Spring service configuration during ServiceTarget initialization.
type ServiceValidator interface {
	ValidateService(context.Context, *azdext.ServiceConfig, ...*azdext.ProjectConfig) error
}

// DeveloperIdentity holds the resolved Entra User object ID and user-access tenant ID.
type DeveloperIdentity = developerIdentity

// DeveloperIdentityResolver resolves the signed-in Entra User principal for a subscription.
type DeveloperIdentityResolver = developerIdentityResolver

// NewDeveloperIdentityResolver creates an azd Account-backed DeveloperIdentityResolver.
func NewDeveloperIdentityResolver(client *azdext.AzdClient) DeveloperIdentityResolver {
	return newDeveloperIdentityResolver(client)
}

// ValidAdministratorObjectID reports whether value is a non-zero UUID.
func ValidAdministratorObjectID(value string) bool {
	return validAdministratorObjectID(value)
}

// ValidAdministratorName reports whether value is a printable, bounded PostgreSQL principal name.
func ValidAdministratorName(value string) bool {
	return validAdministratorName(value)
}

// CredentialForSubscription creates an azd credential scoped to the subscription's user-access tenant.
func CredentialForSubscription(
	ctx context.Context, client *azdext.AzdClient, subscriptionID string,
) (azcore.TokenCredential, error) {
	return credentialForSubscription(ctx, client, subscriptionID)
}

// CurrentDeploymentOutput represents one typed root deployment output entry.
type CurrentDeploymentOutput = currentDeploymentOutput

// CurrentDeploymentOutputs maps root deployment output names to values.
type CurrentDeploymentOutputs = currentDeploymentOutputs

// DeploymentBinding captures verified root deployment resources, outputs, and Container Apps.
type DeploymentBinding = deploymentBinding

// DeploymentBindingKey is the context key storing a verified *DeploymentBinding.
type DeploymentBindingKey = deploymentBindingKey

// RequireResource verifies that resourceID belongs to the current root deployment.
func (b *deploymentBinding) RequireResource(resourceID string) error {
	return b.requireResource(resourceID)
}

// DeploymentOutputVerifier reads and verifies root ARM deployment outputs against resolved databases.
type DeploymentOutputVerifier = deploymentOutputVerifier

// AzureDeploymentOutputVerifier is the live AzdClient-backed DeploymentOutputVerifier.
type AzureDeploymentOutputVerifier = azureDeploymentOutputVerifier

// NewDeploymentOutputVerifier creates a live DeploymentOutputVerifier.
func NewDeploymentOutputVerifier(client *azdext.AzdClient) DeploymentOutputVerifier {
	return newDeploymentOutputVerifier(client)
}

// FindDeploymentOutput looks up an output case-insensitively while rejecting ambiguous casing collisions.
func FindDeploymentOutput(
	outputs CurrentDeploymentOutputs, expectedName string,
) (CurrentDeploymentOutput, bool, error) {
	return findDeploymentOutput(outputs, expectedName)
}

// BindServiceContext selects the verified Container App and identity outputs for serviceName in ctx.
func BindServiceContext(ctx context.Context, serviceName string) (context.Context, error) {
	return bindServiceContext(ctx, serviceName)
}

// CheckResourceOwnership verifies both ARM tag compatibility and root deployment membership.
func CheckResourceOwnership(
	ctx context.Context,
	client *azdext.AzdClient,
	envName string,
	values map[string]string,
	resourceID string,
	tags map[string]*string,
) error {
	return checkResourceOwnership(ctx, client, envName, values, resourceID, tags)
}

// ReadDeploymentBinding reads and verifies the current root deployment and bound Container Apps.
func ReadDeploymentBinding(
	ctx context.Context, client *azdext.AzdClient, envName string, values map[string]string,
) (*DeploymentBinding, error) {
	return readDeploymentBinding(ctx, client, envName, values)
}

// ResolveProjectServiceModules validates and resolves all Spring service Bicep modules in project.
func ResolveProjectServiceModules(
	ctx context.Context, client *azdext.AzdClient, project *azdext.ProjectConfig,
) (map[string]*resolvedServiceModule, error) {
	return resolveProjectServiceModules(ctx, client, project)
}

// TagEqualsPointers checks case-insensitive tag key/value equality on an ARM tag map.
func TagEqualsPointers(tags map[string]*string, expectedKey, expectedValue string) bool {
	return tagEqualsPointers(tags, expectedKey, expectedValue)
}

// HasSystemAssignedIdentity reports whether identity enables system-assigned managed identity.
func HasSystemAssignedIdentity(identity *armappcontainers.ManagedServiceIdentity) bool {
	return hasSystemAssignedIdentity(identity)
}

func hasSystemAssignedIdentity(identity *armappcontainers.ManagedServiceIdentity) bool {
	if identity == nil || identity.Type == nil {
		return false
	}
	return *identity.Type == armappcontainers.ManagedServiceIdentityTypeSystemAssigned ||
		*identity.Type == armappcontainers.ManagedServiceIdentityTypeSystemAssignedUserAssigned
}

// ValidateProvisionedBindings verifies persisted/bound server, database, and identity outputs.
func ValidateProvisionedBindings(database springmodel.ResolvedDatabase, values map[string]string) error {
	return validateProvisionedBindings(database, values)
}

func validateProvisionedBindings(database springmodel.ResolvedDatabase, values map[string]string) error {
	serverID := strings.TrimSpace(values[springmodel.PostgresServerIDEnv])
	if serverID == "" || !strings.EqualFold(serverID, database.ServerResourceID) {
		return validationError(
			"mismatched_postgres_binding",
			"database.serverResourceId does not match the current PostgreSQL root deployment output",
			"restore the current root server output and use the selected serverResourceId expression",
		)
	}
	if output := strings.TrimSpace(values[springmodel.PostgresDatabaseEnv]); output != "" &&
		output != database.Name {
		return validationError(
			"mismatched_database_name",
			"database.databaseName does not match the current PostgreSQL root deployment output",
			"restore the authored database selection from the current deployment",
		)
	}
	if database.Authentication == springmodel.DatabaseAuthenticationManagedIdentity {
		if database.AppIdentity == nil {
			return validationError(
				"mismatched_database_app_identity",
				"the discovered Spring application identity is unavailable",
				"restore the current application identity and deployment outputs",
			)
		}
		switch database.AppIdentity.Type {
		case springmodel.DatabaseAppIdentityTypeSystemAssigned:
			if principalID := strings.TrimSpace(values[springmodel.ServiceIdentityPrincipalIDEnv]); principalID == "" ||
				!strings.EqualFold(database.AppIdentity.ResourceID, values[springmodel.ContainerAppIDEnv]) {
				return validationError(
					"mismatched_database_app_identity",
					"the discovered system-assigned identity does not match the selected service application",
					"restore the current Container App and application principal outputs",
				)
			}
		case springmodel.DatabaseAppIdentityTypeUserAssigned:
			identityID := strings.TrimSpace(values[springmodel.ServiceIdentityIDEnv])
			if identityID == "" || !strings.EqualFold(identityID, database.AppIdentity.ResourceID) {
				return validationError(
					"mismatched_database_app_identity",
					"the discovered Spring application identity does not match the selected service identity output",
					"restore the current service identity output and use the selected UAMI resourceId expression",
				)
			}
		default:
			return validationError(
				"unsupported_database_app_identity",
				"the discovered Spring application identity type is unsupported",
				"select the Container App system identity or one attached user-assigned identity",
			)
		}
	}
	return nil
}

// ReadOfflineServiceDatasource evaluates source/JAR Spring configuration and dependencies without Azure calls.
func ReadOfflineServiceDatasource(
	ctx context.Context,
	project *azdext.ProjectConfig,
	service *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
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
		Root: root, Deployed: true, Environment: map[string]string{}, AllowPending: true,
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
	return springmodel.InferDatasource(config, dependencies, false)
}
