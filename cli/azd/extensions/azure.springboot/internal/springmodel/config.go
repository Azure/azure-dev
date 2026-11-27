// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package springmodel

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"azurespringboot/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

const (
	SpringHost                            = "springbootapp"
	DatabaseTypeEnv                       = "SPRING_DATABASE_TYPE"
	DatabaseAuthenticationEnv             = "SPRING_DATABASE_AUTHENTICATION"
	DatabaseNameEnv                       = "SPRING_DATABASE_NAME"
	DatabaseUsernameEnv                   = "SPRING_DATABASE_USERNAME"
	DatabaseAdministratorNameEnv          = "SPRING_DATABASE_ADMINISTRATOR_NAME"
	DatabaseAdministratorObjectIDEnv      = "SPRING_DATABASE_ADMINISTRATOR_OBJECT_ID"
	DatabaseUserAccessTenantIDEnv         = "SPRING_DATABASE_USER_ACCESS_TENANT_ID"
	DatabasePasswordEnv                   = "SPRING_DATABASE_PASSWORD"
	DatabaseAppPasswordEnv                = "SPRING_DATABASE_APP_PASSWORD"
	ProvisionedDatabaseTypeEnv            = "SPRING_DATABASE_PROVISIONED_TYPE"
	ProvisionedDatabaseAccessEnv          = "SPRING_DATABASE_PROVISIONED_ACCESS"
	DatabaseAccessConfiguredEnv           = "SPRING_DATABASE_ACCESS_CONFIGURED"
	DatabaseAccessPolicyVersion           = "app-owned-v2"
	AutomaticMIProvisioningVersion        = "app-owned-v2-local-provision-v1"
	AzureSubscriptionIDEnv                = "AZURE_SUBSCRIPTION_ID"
	AzureResourceGroupEnv                 = "AZURE_RESOURCE_GROUP"
	ContainerAppIDEnv                     = "AZURE_CONTAINER_APP_ID"
	PostgresServerNameEnv                 = "AZURE_POSTGRES_SERVER_NAME"
	PostgresServerIDEnv                   = "AZURE_POSTGRES_SERVER_ID"
	PostgresHostEnv                       = "AZURE_POSTGRES_HOST"
	PostgresDatabaseEnv                   = "AZURE_POSTGRES_DATABASE"
	PostgresAdministratorEnv              = "AZURE_POSTGRES_ADMINISTRATOR"
	ServiceIdentityIDEnv                  = "SERVICE_APPLICATION_IDENTITY_ID"
	ServiceIdentityClientIDEnv            = "SERVICE_APPLICATION_IDENTITY_CLIENT_ID"
	ServiceIdentityPrincipalIDEnv         = "SERVICE_APPLICATION_IDENTITY_PRINCIPAL_ID"
	DatabaseAppIdentityTypeSystemAssigned = "systemAssigned"
	DatabaseAppIdentityTypeUserAssigned   = "userAssigned"
	// #nosec G101 -- public OAuth scope, not a credential.
	PostgresTokenScope = "https://ossrdbms-aad.database.windows.net/.default"
)

type DatabaseType string

const (
	DatabaseNone       DatabaseType = "none"
	DatabaseH2         DatabaseType = "h2"
	DatabasePostgreSQL DatabaseType = "postgresql"
)

type DatabaseAuthentication string

const (
	DatabaseAuthenticationManagedIdentity DatabaseAuthentication = "managedidentity"
)

type DatabaseAppIdentity struct {
	Type       string
	ResourceID string
	Deferred   bool
}

type ResolvedDatabase struct {
	Type              DatabaseType
	ServerResourceID  string
	ServerDeferred    bool
	SelectionPending  bool
	Name              string
	Authentication    DatabaseAuthentication
	Username          string
	AdministratorName string
	AppIdentity       *DatabaseAppIdentity
	Discovered        bool
}

func (d ResolvedDatabase) ManagedAccess() bool {
	return d.Authentication == DatabaseAuthenticationManagedIdentity
}

func (d ResolvedDatabase) AccessFingerprint() string {
	identityType, identity := "", ""
	if d.AppIdentity != nil {
		identityType = d.AppIdentity.Type
		identity = strings.ToLower(d.AppIdentity.ResourceID)
	}
	return strings.Join([]string{
		DatabaseAccessPolicyVersion,
		string(d.Type),
		string(d.Authentication),
		strings.ToLower(d.ServerResourceID),
		d.Name,
		d.Username,
		identityType,
		identity,
	}, "|")
}

func ServiceProvisionedAccessEnvKey(serviceName string) string {
	return "SERVICE_" + environment.Key(serviceName) + "_" + ProvisionedDatabaseAccessEnv
}

func NormalizeAccessFingerprint(raw string) string {
	trimmed := strings.TrimSpace(raw)
	parts := strings.Split(trimmed, "|")
	if len(parts) != 8 {
		return trimmed
	}
	parts[3] = strings.ToLower(parts[3])
	parts[7] = strings.ToLower(parts[7])
	return strings.Join(parts, "|")
}

func ExpectedProvisionedAccess(values map[string]string, serviceName string) string {
	if perService := strings.TrimSpace(values[ServiceProvisionedAccessEnvKey(serviceName)]); perService != "" {
		return NormalizeAccessFingerprint(perService)
	}
	return NormalizeAccessFingerprint(values[ProvisionedDatabaseAccessEnv])
}

var DatabaseIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,62}$`)
var DatabaseLoginPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.@-]{0,62}$`)
var DatabasePrincipalIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

func validationError(code, message, suggestion string) error {
	return exterrors.Validation(code, message, suggestion)
}

func SpringServices(project *azdext.ProjectConfig) []*azdext.ServiceConfig {
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

func ValidateAzureResourceID(
	service *azdext.ServiceConfig,
	field string,
	value string,
	expectedType string,
) error {
	resourceID, err := arm.ParseResourceID(value)
	if err != nil || !strings.EqualFold(resourceID.ResourceType.String(), expectedType) ||
		resourceID.SubscriptionID == "" || resourceID.ResourceGroupName == "" || resourceID.Name == "" {
		return exterrors.Validation(
			"invalid_database_resource_id",
			fmt.Sprintf("service %q %s is not a valid %s resource ID", service.GetName(), field, expectedType),
			fmt.Sprintf("set %s to a complete Azure resource ID", field),
		)
	}
	return nil
}

func EnvironmentLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		if value, ok := values[key]; ok {
			return value, true
		}
		return os.LookupEnv(key)
	}
}

func RegularContainedFile(projectRoot, candidate string) bool {
	info, err := os.Lstat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(projectRoot, resolved)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// EnvironmentStore reads and updates values in the selected azd environment.
type EnvironmentStore interface {
	Values(context.Context) (string, map[string]string, error)
	Set(context.Context, string, string, string) error
}

type grpcEnvironmentStore struct {
	client *azdext.AzdClient
}

// NewGrpcEnvironmentStore creates an EnvironmentStore backed by an AzdClient.
func NewGrpcEnvironmentStore(client *azdext.AzdClient) EnvironmentStore {
	return &grpcEnvironmentStore{client: client}
}

func (s *grpcEnvironmentStore) Values(ctx context.Context) (string, map[string]string, error) {
	current, err := s.client.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", nil, fmt.Errorf("getting current azd environment: %w", err)
	}
	if current.GetEnvironment().GetName() == "" {
		return "", nil, errors.New("no current azd environment is selected")
	}
	name := current.GetEnvironment().GetName()
	response, err := s.client.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: name})
	if err != nil {
		return "", nil, fmt.Errorf("getting values for azd environment %q: %w", name, err)
	}
	values := make(map[string]string, len(response.GetKeyValues()))
	for _, pair := range response.GetKeyValues() {
		if pair != nil {
			values[pair.GetKey()] = pair.GetValue()
		}
		values["AZURE_ENV_NAME"] = name
	}
	return name, values, nil
}

func (s *grpcEnvironmentStore) Set(ctx context.Context, envName, key, value string) error {
	_, err := s.client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return fmt.Errorf("setting %s in azd environment %q: %w", key, envName, err)
	}
	return nil
}
