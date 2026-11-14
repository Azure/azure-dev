// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const azdEnvironmentTag = "azd-env-name"

var errPostgreSQLResourceNotFound = errors.New("PostgreSQL resource not found")

type deploymentSafetyChecker interface {
	EnsureCanGenerate(context.Context, string, map[string]string) error
}

type azureDeploymentSafetyChecker struct {
	client                 *azdext.AzdClient
	verifyOwnership        func(context.Context, string, map[string]string, string) error
	verifyNoOwnedResources func(context.Context, string, map[string]string) error
}

func newDeploymentSafetyChecker(client *azdext.AzdClient) deploymentSafetyChecker {
	checker := &azureDeploymentSafetyChecker{client: client}
	checker.verifyOwnership = checker.verifyPostgreSQLOwnership
	checker.verifyNoOwnedResources = checker.ensureNoOwnedPostgreSQLResources
	return checker
}

func (c *azureDeploymentSafetyChecker) EnsureCanGenerate(
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

	resourceID := strings.TrimSpace(values[postgresServerIDEnv])
	if resourceID == "" {
		resourceGroup := strings.TrimSpace(values[azureResourceGroupEnv])
		resourceName := strings.TrimSpace(values[postgresServerNameEnv])
		if resourceGroup != "" && resourceName != "" {
			resourceID = fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforPostgreSQL/flexibleServers/%s",
				subscriptionID,
				resourceGroup,
				resourceName,
			)
		}
	}

	if resourceID != "" {
		err := c.verifyOwnership(ctx, envName, values, resourceID)
		if err == nil {
			return missingExistingDatabasePasswordError()
		}
		if !errors.Is(err, errPostgreSQLResourceNotFound) {
			return err
		}
	}

	return c.verifyNoOwnedResources(ctx, envName, values)
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
	identity, err := parsePostgreSQLResourceID(resourceID)
	if err != nil {
		return err
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
		if responseErr, ok := errors.AsType[*azcore.ResponseError](err); ok &&
			responseErr.StatusCode == http.StatusNotFound {
			return errPostgreSQLResourceNotFound
		}
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

func parsePostgreSQLResourceID(resourceID string) (*arm.ResourceID, error) {
	identity, err := arm.ParseResourceID(resourceID)
	if err != nil ||
		!strings.EqualFold(identity.ResourceType.String(), "Microsoft.DBforPostgreSQL/flexibleServers") {
		return nil, validationError(
			"invalid_existing_database_identity",
			"the existing deployment returned an invalid PostgreSQL resource ID",
			"restore the original password or repair the deployment before retrying",
		)
	}
	return identity, nil
}

func (c *azureDeploymentSafetyChecker) credential(
	ctx context.Context,
	subscriptionID string,
) (azcore.TokenCredential, error) {
	return credentialForSubscription(ctx, c.client, subscriptionID)
}

func tagEqualsPointers(tags map[string]*string, key, expected string) bool {
	for tag, value := range tags {
		if strings.EqualFold(tag, key) && value != nil && strings.EqualFold(*value, expected) {
			return true
		}
	}
	return false
}
