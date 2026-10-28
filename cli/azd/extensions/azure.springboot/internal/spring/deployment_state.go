// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const azdEnvironmentTag = "azd-env-name"

type deploymentSafetyChecker interface {
	EnsureCanGenerate(context.Context, string, map[string]string) error
}

type azureDeploymentSafetyChecker struct {
	client                 *azdext.AzdClient
	getDeployment          func(context.Context) (*azdext.GetDeploymentResponse, error)
	verifyOwnership        func(context.Context, string, map[string]string, string) error
	verifyNoOwnedResources func(context.Context, string, map[string]string) error
}

func newDeploymentSafetyChecker(client *azdext.AzdClient) deploymentSafetyChecker {
	checker := &azureDeploymentSafetyChecker{client: client}
	checker.getDeployment = func(ctx context.Context) (*azdext.GetDeploymentResponse, error) {
		return client.Deployment().GetDeployment(ctx, &azdext.EmptyRequest{})
	}
	checker.verifyOwnership = checker.verifyPostgreSQLOwnership
	checker.verifyNoOwnedResources = checker.ensureNoOwnedPostgreSQLResources
	return checker
}

func (c *azureDeploymentSafetyChecker) EnsureCanGenerate(
	ctx context.Context,
	envName string,
	values map[string]string,
) error {
	response, err := c.getDeployment(ctx)
	if status.Code(err) == codes.NotFound {
		return c.verifyNoOwnedResources(ctx, envName, values)
	}
	if err != nil {
		return validationError(
			"database_deployment_lookup_failed",
			"could not establish whether the selected environment already has infrastructure",
			"restore the original password or fix Azure read access before retrying; password generation is blocked",
		)
	}
	if response == nil {
		return validationError(
			"database_deployment_identity_missing",
			"azd returned an empty deployment response",
			"restore the original password or repair the environment deployment state before retrying",
		)
	}
	deployment := response.GetDeployment()
	if deployment == nil || deployment.GetId() == "" {
		return validationError(
			"database_deployment_identity_missing",
			"azd returned an existing deployment without an authoritative deployment identity",
			"restore the original password or repair the environment deployment state before retrying",
		)
	}
	if !tagEquals(deployment.GetTags(), azdEnvironmentTag, envName) {
		return validationError(
			"database_deployment_ownership_unverified",
			"the existing deployment is not owned by the selected azd environment",
			"restore the original password or use a new azd environment; password generation is blocked",
		)
	}

	postgresIDs := make([]string, 0, 1)
	for _, resourceID := range deployment.GetResources() {
		parsed, parseErr := arm.ParseResourceID(resourceID)
		if parseErr != nil {
			continue
		}
		if strings.EqualFold(parsed.ResourceType.Namespace, "Microsoft.DBforPostgreSQL") &&
			strings.EqualFold(parsed.ResourceType.Type, "flexibleServers") {
			postgresIDs = append(postgresIDs, resourceID)
		}
	}
	if len(postgresIDs) > 1 {
		return validationError(
			"ambiguous_existing_database_identity",
			"the existing deployment contains more than one PostgreSQL Flexible Server",
			"restore the original password and deployment outputs before retrying",
		)
	}
	if len(postgresIDs) == 1 {
		if err := c.verifyOwnership(ctx, envName, values, postgresIDs[0]); err != nil {
			return err
		}
	}

	return missingExistingDatabasePasswordError()
}

func (c *azureDeploymentSafetyChecker) ensureNoOwnedPostgreSQLResources(
	ctx context.Context,
	envName string,
	values map[string]string,
) error {
	subscriptionID := strings.TrimSpace(values[azureSubscriptionIDEnv])
	if subscriptionID == "" {
		return validationError(
			"database_subscription_identity_missing",
			"the selected Azure subscription is unavailable",
			"restore the environment subscription before retrying; password generation is blocked",
		)
	}
	credential, err := c.credential(ctx, subscriptionID)
	if err != nil {
		return err
	}
	resources, err := armresources.NewClient(subscriptionID, credential, nil)
	if err != nil {
		return fmt.Errorf("creating Azure resources client: %w", err)
	}
	resourceType := "Microsoft.DBforPostgreSQL/flexibleServers"
	pager := resources.NewListPager(&armresources.ClientListOptions{
		Filter: new(fmt.Sprintf("resourceType eq '%s'", resourceType)),
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("listing existing PostgreSQL servers: %w", err)
		}
		for _, resource := range page.Value {
			if resource != nil && tagEqualsPointers(resource.Tags, azdEnvironmentTag, envName) {
				return missingExistingDatabasePasswordError()
			}
		}
	}
	return nil
}

func (c *azureDeploymentSafetyChecker) verifyPostgreSQLOwnership(
	ctx context.Context,
	envName string,
	values map[string]string,
	resourceID string,
) error {
	identity, err := arm.ParseResourceID(resourceID)
	if err != nil {
		return validationError(
			"invalid_existing_database_identity",
			"the existing deployment returned an invalid PostgreSQL resource ID",
			"restore the original password or repair the deployment before retrying",
		)
	}
	subscriptionID := strings.TrimSpace(values[azureSubscriptionIDEnv])
	if subscriptionID == "" || !strings.EqualFold(subscriptionID, identity.SubscriptionID) {
		return validationError(
			"mismatched_existing_database_subscription",
			"the existing PostgreSQL resource does not match the selected subscription",
			"restore the original password or select the environment's original subscription",
		)
	}
	credential, err := c.credential(ctx, identity.SubscriptionID)
	if err != nil {
		return err
	}
	servers, err := armpostgresqlflexibleservers.NewServersClient(identity.SubscriptionID, credential, nil)
	if err != nil {
		return fmt.Errorf("creating PostgreSQL server client: %w", err)
	}
	response, err := servers.Get(ctx, identity.ResourceGroupName, identity.Name, nil)
	if err != nil {
		return fmt.Errorf("reading the existing PostgreSQL server: %w", err)
	}
	if response.Server.ID == nil || !strings.EqualFold(*response.Server.ID, resourceID) ||
		!tagEqualsPointers(response.Server.Tags, azdEnvironmentTag, envName) {
		return validationError(
			"database_resource_ownership_unverified",
			"the existing PostgreSQL server is not owned by the selected azd environment",
			"restore the original password or use a new azd environment; password generation is blocked",
		)
	}
	return nil
}

func (c *azureDeploymentSafetyChecker) credential(
	ctx context.Context,
	subscriptionID string,
) (azcore.TokenCredential, error) {
	tenant, err := c.client.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: subscriptionID,
	})
	if err != nil {
		return nil, fmt.Errorf("looking up the user access tenant for the selected subscription: %w", err)
	}
	if tenant.GetTenantId() == "" {
		return nil, errorsNoTenant()
	}
	credential, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   tenant.GetTenantId(),
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("creating the azd Azure credential: %w", err)
	}
	return credential, nil
}

func tagEquals(tags map[string]string, key, expected string) bool {
	for tag, value := range tags {
		if strings.EqualFold(tag, key) && strings.EqualFold(value, expected) {
			return true
		}
	}
	return false
}

func tagEqualsPointers(tags map[string]*string, key, expected string) bool {
	for tag, value := range tags {
		if strings.EqualFold(tag, key) && value != nil && strings.EqualFold(*value, expected) {
			return true
		}
	}
	return false
}
