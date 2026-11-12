// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	SpringHost                            = "springbootapp"
	databaseTypeEnv                       = "SPRING_DATABASE_TYPE"
	databaseAuthenticationEnv             = "SPRING_DATABASE_AUTHENTICATION"
	databaseNameEnv                       = "SPRING_DATABASE_NAME"
	databaseUsernameEnv                   = "SPRING_DATABASE_USERNAME"
	databaseAdministratorNameEnv          = "SPRING_DATABASE_ADMINISTRATOR_NAME"
	databaseAdministratorObjectIDEnv      = "SPRING_DATABASE_ADMINISTRATOR_OBJECT_ID"
	databaseUserAccessTenantIDEnv         = "SPRING_DATABASE_USER_ACCESS_TENANT_ID"
	databasePasswordEnv                   = "SPRING_DATABASE_PASSWORD"
	databaseAppPasswordEnv                = "SPRING_DATABASE_APP_PASSWORD"
	provisionedDatabaseTypeEnv            = "SPRING_DATABASE_PROVISIONED_TYPE"
	provisionedDatabaseAccessEnv          = "SPRING_DATABASE_PROVISIONED_ACCESS"
	databaseAccessConfiguredEnv           = "SPRING_DATABASE_ACCESS_CONFIGURED"
	databaseAccessPolicyVersion           = "app-owned-v2"
	azureSubscriptionIDEnv                = "AZURE_SUBSCRIPTION_ID"
	azureResourceGroupEnv                 = "AZURE_RESOURCE_GROUP"
	containerAppIDEnv                     = "AZURE_CONTAINER_APP_ID"
	postgresServerNameEnv                 = "AZURE_POSTGRES_SERVER_NAME"
	postgresServerIDEnv                   = "AZURE_POSTGRES_SERVER_ID"
	postgresHostEnv                       = "AZURE_POSTGRES_HOST"
	postgresDatabaseEnv                   = "AZURE_POSTGRES_DATABASE"
	postgresAdministratorEnv              = "AZURE_POSTGRES_ADMINISTRATOR"
	serviceIdentityIDEnv                  = "SERVICE_PETCLINIC_IDENTITY_ID"
	serviceIdentityClientIDEnv            = "SERVICE_PETCLINIC_IDENTITY_CLIENT_ID"
	serviceIdentityPrincipalIDEnv         = "SERVICE_PETCLINIC_IDENTITY_PRINCIPAL_ID"
	postgresBootstrapClientIPEnv          = "POSTGRES_BOOTSTRAP_CLIENT_IP"
	databaseAppIdentityTypeSystemAssigned = "systemAssigned"
	databaseAppIdentityTypeUserAssigned   = "userAssigned"
)

type databaseType string

const (
	databaseNone       databaseType = "none"
	databaseH2         databaseType = "h2"
	databasePostgreSQL databaseType = "postgresql"
)

type databaseAuthentication string

const (
	databaseAuthenticationLegacy          databaseAuthentication = "legacy"
	databaseAuthenticationPassword        databaseAuthentication = "password"
	databaseAuthenticationManagedIdentity databaseAuthentication = "managedidentity"
)

type databaseAppIdentity struct {
	Type       string
	ResourceID string
	Deferred   bool
}

type resolvedDatabase struct {
	Type              databaseType
	ServerResourceID  string
	ServerDeferred    bool
	selectionPending  bool
	Name              string
	Authentication    databaseAuthentication
	Username          string
	AdministratorName string
	AppIdentity       *databaseAppIdentity
	discovered        bool
	appPassword       string
}

func validateApplicationPackaging(service *azdext.ServiceConfig) error {
	return validateServiceConfiguration(service)
}

func (d resolvedDatabase) managedAccess() bool {
	return d.Authentication == databaseAuthenticationPassword ||
		d.Authentication == databaseAuthenticationManagedIdentity
}

func (d resolvedDatabase) accessFingerprint() string {
	identityType, identity := "", ""
	if d.AppIdentity != nil {
		identityType = d.AppIdentity.Type
		identity = strings.ToLower(d.AppIdentity.ResourceID)
	}
	return strings.Join([]string{
		databaseAccessPolicyVersion,
		string(d.Type),
		string(d.Authentication),
		strings.ToLower(d.ServerResourceID),
		d.Name,
		d.Username,
		identityType,
		identity,
	}, "|")
}

type resolvedService struct {
	service  *azdext.ServiceConfig
	mode     databaseType
	database resolvedDatabase
}

var databaseIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,62}$`)
var databaseLoginPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.@-]{0,62}$`)
var databasePrincipalIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

func validationError(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: suggestion,
	}
}

func springServices(project *azdext.ProjectConfig) []*azdext.ServiceConfig {
	if project == nil {
		return nil
	}

	names := slices.Sorted(maps.Keys(project.Services))
	services := make([]*azdext.ServiceConfig, 0, len(names))
	for _, name := range names {
		service := project.Services[name]
		if service != nil && service.Host == SpringHost {
			services = append(services, service)
		}
	}
	return services
}

func validateAzureResourceID(
	service *azdext.ServiceConfig,
	field string,
	value string,
	expectedType string,
) error {
	resourceID, err := arm.ParseResourceID(value)
	if err != nil || !strings.EqualFold(resourceID.ResourceType.String(), expectedType) ||
		resourceID.SubscriptionID == "" || resourceID.ResourceGroupName == "" || resourceID.Name == "" {
		return validationError(
			"invalid_database_resource_id",
			fmt.Sprintf("service %q %s is not a valid %s resource ID", service.GetName(), field, expectedType),
			fmt.Sprintf("set %s to a complete Azure resource ID", field),
		)
	}
	return nil
}

func environmentLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		if value, ok := values[key]; ok {
			return value, true
		}
		return os.LookupEnv(key)
	}
}
