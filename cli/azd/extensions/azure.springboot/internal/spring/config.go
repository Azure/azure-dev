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
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
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
	databaseAppIdentityTypeUserAssigned   = "userAssigned"
	deferredConfigurationValuePlaceholder = "azd-deferred-value"
)

type databaseType string

const (
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

func discoversDatabase(service *azdext.ServiceConfig) bool {
	_, present := service.GetAdditionalProperties().AsMap()["database"]
	return !present
}

func validateApplicationPackaging(service *azdext.ServiceConfig) error {
	document, err := decodeDatabaseConfig(service)
	if err != nil {
		return err
	}
	if usesAuthoredDockerfile(service) && (discoversDatabase(service) || document.Auth != nil) {
		return configurationError(
			"automatic discovery and application-auth verification require native Maven packaging, not an authored image",
		)
	}
	return nil
}

func (d resolvedDatabase) managedAccess() bool {
	return d.Authentication == databaseAuthenticationPassword ||
		d.Authentication == databaseAuthenticationManagedIdentity
}

func (d resolvedDatabase) accessFingerprint() string {
	identity := ""
	if d.AppIdentity != nil {
		identity = strings.ToLower(d.AppIdentity.ResourceID)
	}
	return strings.Join([]string{
		databaseAccessPolicyVersion,
		string(d.Type),
		string(d.Authentication),
		strings.ToLower(d.ServerResourceID),
		d.Name,
		d.Username,
		identity,
	}, "|")
}

type resolvedService struct {
	service  *azdext.ServiceConfig
	mode     databaseType
	database resolvedDatabase
}

type databaseConfigDocument struct {
	Type             string
	ServerResourceID string
	ServerPresent    bool
	DatabaseName     string
	DatabasePresent  bool
	Auth             *databaseAuthConfigDocument
}

type databaseAuthConfigDocument struct {
	Type                     string
	Username                 string
	AdministratorName        string
	AdministratorNamePresent bool
	AppIdentity              *databaseAppIdentityConfigDocument
}

type databaseAppIdentityConfigDocument struct {
	Type       string
	ResourceID string
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

func resolveDatabase(
	service *azdext.ServiceConfig,
	values map[string]string,
	lookupProcess func(string) (string, bool),
	requireLateValues bool,
) (resolvedDatabase, error) {
	document, err := decodeDatabaseConfig(service)
	if err != nil {
		return resolvedDatabase{}, err
	}
	modeValue, _, err := resolveConfigString(
		service,
		"database.type",
		document.Type,
		values,
		lookupProcess,
		false,
	)
	if err != nil {
		return resolvedDatabase{}, err
	}
	mode := databaseType(strings.ToLower(modeValue))
	switch mode {
	case databaseH2, databasePostgreSQL:
	default:
		return resolvedDatabase{}, validationError(
			"unsupported_database_type",
			fmt.Sprintf("service %q database.type resolved to unsupported value %q", service.GetName(), modeValue),
			"set database.type or SPRING_DATABASE_TYPE to h2 or postgresql",
		)
	}
	if err := validateBoundValue(service, databaseTypeEnv, string(mode), values, lookupProcess, false); err != nil {
		return resolvedDatabase{}, err
	}

	if mode == databaseH2 {
		if document.ServerPresent || document.DatabasePresent || document.Auth != nil {
			return resolvedDatabase{}, validationError(
				"h2_database_fields_not_supported",
				fmt.Sprintf("service %q selects h2 but also supplies PostgreSQL target or auth fields", service.GetName()),
				"remove serverResourceId, databaseName, and auth when database.type is h2",
			)
		}
		return resolvedDatabase{Type: mode, Authentication: databaseAuthenticationLegacy}, nil
	}

	if document.Auth == nil {
		if document.ServerPresent || document.DatabasePresent {
			return resolvedDatabase{}, validationError(
				"postgres_target_requires_auth",
				fmt.Sprintf("service %q supplies PostgreSQL target fields without database.auth", service.GetName()),
				"add the complete auth object or remove the new target fields to retain legacy password behavior",
			)
		}
		return resolvedDatabase{Type: mode, Authentication: databaseAuthenticationLegacy}, nil
	}

	serverID, serverDeferred, err := resolveConfigString(
		service,
		"database.serverResourceId",
		document.ServerResourceID,
		values,
		lookupProcess,
		!requireLateValues,
	)
	if err != nil {
		return resolvedDatabase{}, err
	}
	if requireLateValues && serverDeferred {
		return resolvedDatabase{}, missingLateValueError(service, "database.serverResourceId")
	}
	if !serverDeferred {
		if err := validateAzureResourceID(
			service,
			"database.serverResourceId",
			serverID,
			"Microsoft.DBforPostgreSQL/flexibleServers",
		); err != nil {
			return resolvedDatabase{}, err
		}
	}
	databaseName, _, err := resolveConfigString(
		service,
		"database.databaseName",
		document.DatabaseName,
		values,
		lookupProcess,
		false,
	)
	if err != nil {
		return resolvedDatabase{}, err
	}
	if !databaseIdentifierPattern.MatchString(databaseName) {
		return resolvedDatabase{}, validationError(
			"invalid_database_name",
			fmt.Sprintf(
				"service %q database.databaseName is not a supported PostgreSQL identifier",
				service.GetName(),
			),
			"use a name that starts with a letter or underscore and contains only letters, numbers, underscores, or hyphens",
		)
	}

	authTypeValue, _, err := resolveConfigString(
		service,
		"database.auth.type",
		document.Auth.Type,
		values,
		lookupProcess,
		false,
	)
	if err != nil {
		return resolvedDatabase{}, err
	}
	authentication := databaseAuthentication(strings.ToLower(authTypeValue))
	if authentication != databaseAuthenticationPassword &&
		authentication != databaseAuthenticationManagedIdentity {
		return resolvedDatabase{}, validationError(
			"unsupported_database_authentication",
			fmt.Sprintf(
				"service %q database.auth.type resolved to unsupported value %q",
				service.GetName(),
				authTypeValue,
			),
			"set auth.type to password or managedidentity",
		)
	}
	username, _, err := resolveConfigString(
		service,
		"database.auth.username",
		document.Auth.Username,
		values,
		lookupProcess,
		false,
	)
	if err != nil {
		return resolvedDatabase{}, err
	}
	if !databaseLoginPattern.MatchString(username) {
		return resolvedDatabase{}, validationError(
			"invalid_database_username",
			fmt.Sprintf("service %q database.auth.username contains unsupported characters", service.GetName()),
			"use a PostgreSQL login name containing letters, numbers, underscores, periods, at signs, or hyphens",
		)
	}
	resolved := resolvedDatabase{
		Type:             mode,
		ServerResourceID: serverID,
		ServerDeferred:   serverDeferred,
		Name:             databaseName,
		Authentication:   authentication,
		Username:         username,
	}
	if document.Auth.AdministratorNamePresent {
		administratorName, _, resolveErr := resolveConfigString(
			service,
			"database.auth.administratorName",
			document.Auth.AdministratorName,
			values,
			lookupProcess,
			false,
		)
		if resolveErr != nil {
			return resolvedDatabase{}, resolveErr
		}
		if !validAdministratorName(administratorName) {
			return resolvedDatabase{}, validationError(
				"invalid_database_administrator_name",
				fmt.Sprintf(
					"service %q database.auth.administratorName contains unsupported characters",
					service.GetName(),
				),
				"use the current developer PostgreSQL administrator login name",
			)
		}
		resolved.AdministratorName = administratorName
	}

	switch authentication {
	case databaseAuthenticationPassword:
		if document.Auth.AppIdentity != nil || document.Auth.AdministratorNamePresent {
			return resolvedDatabase{}, validationError(
				"password_auth_contains_managed_identity_fields",
				fmt.Sprintf("service %q password auth cannot contain appIdentity or administratorName", service.GetName()),
				"remove managed-identity-only fields from the complete password auth block",
			)
		}
	case databaseAuthenticationManagedIdentity:
		if document.Auth.AppIdentity == nil {
			return resolvedDatabase{}, validationError(
				"managed_identity_binding_missing",
				fmt.Sprintf("service %q managedidentity auth requires appIdentity", service.GetName()),
				"set auth.appIdentity.type to userAssigned and provide its resourceId",
			)
		}
		identityType, _, resolveErr := resolveConfigString(
			service,
			"database.auth.appIdentity.type",
			document.Auth.AppIdentity.Type,
			values,
			lookupProcess,
			false,
		)
		if resolveErr != nil {
			return resolvedDatabase{}, resolveErr
		}
		if identityType != databaseAppIdentityTypeUserAssigned {
			return resolvedDatabase{}, validationError(
				"unsupported_database_app_identity",
				fmt.Sprintf(
					"service %q auth.appIdentity.type resolved to unsupported value %q",
					service.GetName(),
					identityType,
				),
				"set auth.appIdentity.type to userAssigned",
			)
		}
		resourceID, deferred, resolveErr := resolveConfigString(
			service,
			"database.auth.appIdentity.resourceId",
			document.Auth.AppIdentity.ResourceID,
			values,
			lookupProcess,
			!requireLateValues,
		)
		if resolveErr != nil {
			return resolvedDatabase{}, resolveErr
		}
		if requireLateValues && deferred {
			return resolvedDatabase{}, missingLateValueError(service, "database.auth.appIdentity.resourceId")
		}
		if !deferred {
			if err := validateAzureResourceID(
				service,
				"database.auth.appIdentity.resourceId",
				resourceID,
				"Microsoft.ManagedIdentity/userAssignedIdentities",
			); err != nil {
				return resolvedDatabase{}, err
			}
		}
		resolved.AppIdentity = &databaseAppIdentity{
			Type:       identityType,
			ResourceID: resourceID,
			Deferred:   deferred,
		}
	}

	for _, binding := range []struct {
		name          string
		value         string
		caseSensitive bool
	}{
		{databaseAuthenticationEnv, string(authentication), false},
		{databaseNameEnv, databaseName, true},
		{databaseUsernameEnv, username, true},
	} {
		if err := validateBoundValue(
			service,
			binding.name,
			binding.value,
			values,
			lookupProcess,
			binding.caseSensitive,
		); err != nil {
			return resolvedDatabase{}, err
		}
	}
	if resolved.AdministratorName != "" {
		if err := validateBoundValue(
			service,
			databaseAdministratorNameEnv,
			resolved.AdministratorName,
			values,
			lookupProcess,
			true,
		); err != nil {
			return resolvedDatabase{}, err
		}
	}
	return resolved, nil
}

func resolveDatabaseType(
	service *azdext.ServiceConfig,
	values map[string]string,
	lookupProcess func(string) (string, bool),
) (databaseType, error) {
	resolved, err := resolveDatabase(service, values, lookupProcess, false)
	return resolved.Type, err
}

func resolveConfigString(
	service *azdext.ServiceConfig,
	field string,
	raw string,
	values map[string]string,
	lookupProcess func(string) (string, bool),
	allowDeferred bool,
) (string, bool, error) {
	lookup := func(key string) string {
		if value, ok := values[key]; ok {
			return value
		}
		value, _ := lookupProcess(key)
		return value
	}
	deferred := false
	for _, match := range parameterExpression.FindAllStringSubmatch(raw, -1) {
		if match[2] != "" {
			continue
		}
		name := match[1]
		if value, present := values[name]; present {
			if strings.TrimSpace(value) == "" {
				return "", false, validationError(
					"empty_database_config_binding",
					fmt.Sprintf("%s is explicitly empty in the selected azd environment", name),
					fmt.Sprintf("unset %s until it is produced or restore its nonempty deployment output", name),
				)
			}
			continue
		}
		if processValue, processPresent := lookupProcess(name); processPresent {
			if strings.TrimSpace(processValue) == "" {
				return "", false, validationError(
					"empty_database_config_binding",
					fmt.Sprintf("%s is explicitly empty in the process environment", name),
					fmt.Sprintf("remove the empty %s value or provide its nonempty deployment output", name),
				)
			}
			continue
		}
		deferred = true
	}
	if deferred && allowDeferred {
		_, err := osutil.NewExpandableString(raw).Envsubst(func(key string) string {
			if value := lookup(key); value != "" {
				return value
			}
			return deferredConfigurationValuePlaceholder
		})
		if err != nil {
			return "", false, invalidExpressionError(service, field, err)
		}
		return raw, true, nil
	}
	expanded, err := osutil.NewExpandableString(raw).Envsubst(lookup)
	if err != nil {
		return "", false, invalidExpressionError(service, field, err)
	}
	expanded = strings.TrimSpace(expanded)
	if expanded == "" {
		return "", false, validationError(
			"empty_database_config_value",
			fmt.Sprintf("service %q %s is explicitly empty or resolved to an empty value", service.GetName(), field),
			fmt.Sprintf("set %s to a nonempty supported value", field),
		)
	}
	return expanded, false, nil
}

func invalidExpressionError(service *azdext.ServiceConfig, field string, err error) error {
	return validationError(
		"invalid_database_config_expression",
		fmt.Sprintf("service %q has an invalid %s expression: %v", service.GetName(), field, err),
		"use a literal value or a valid azd environment expression",
	)
}

func validateBoundValue(
	service *azdext.ServiceConfig,
	name string,
	expected string,
	values map[string]string,
	lookupProcess func(string) (string, bool),
	caseSensitive bool,
) error {
	matches := func(actual string) bool {
		if caseSensitive {
			return actual == expected
		}
		return strings.EqualFold(actual, expected)
	}
	if persisted, present := values[name]; present {
		if persisted == "" {
			return validationError(
				"empty_database_binding",
				fmt.Sprintf("%s is explicitly empty in the selected azd environment", name),
				fmt.Sprintf("unset %s or set it to %q", name, expected),
			)
		}
		if !matches(persisted) {
			return validationError(
				"conflicting_database_binding",
				fmt.Sprintf("service %q selects %q but %s is %q", service.GetName(), expected, name, persisted),
				"use the same database setting in azure.yaml and the selected azd environment",
			)
		}
	}
	if provided, present := lookupProcess(name); present {
		if provided == "" {
			return validationError(
				"empty_database_binding",
				fmt.Sprintf("%s is explicitly empty in the process environment", name),
				fmt.Sprintf("remove the empty process value or set it to %q", expected),
			)
		}
		if !matches(provided) {
			return validationError(
				"conflicting_database_binding",
				fmt.Sprintf("service %q selects %q but the process %s is %q", service.GetName(), expected, name, provided),
				"use the same database setting in azure.yaml and the process environment",
			)
		}
	}
	return nil
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
			fmt.Sprintf("set %s to a complete Azure resource ID or a deferred deployment-output expression", field),
		)
	}
	return nil
}

func missingLateValueError(service *azdext.ServiceConfig, field string) error {
	return validationError(
		"missing_provisioned_database_output",
		fmt.Sprintf("service %q %s has not resolved to a provisioned resource", service.GetName(), field),
		"run azd provision successfully and restore the current root deployment outputs",
	)
}

func decodeDatabaseConfig(service *azdext.ServiceConfig) (*databaseConfigDocument, error) {
	if service == nil {
		return nil, validationError("missing_service_config", "service configuration is missing", "check azure.yaml")
	}
	document := &databaseConfigDocument{Type: string(databaseH2)}
	if _, legacy := service.GetConfig().GetFields()["database"]; legacy {
		return nil, validationError(
			"legacy_database_config",
			fmt.Sprintf("service %q uses config.database; services.<name>.database is required", service.GetName()),
			"move database directly beside host and language; do not supply both forms",
		)
	}
	config := service.GetAdditionalProperties().AsMap()
	databaseValue, present := config["database"]
	if !present {
		return document, nil
	}
	database, ok := databaseValue.(map[string]any)
	if !ok {
		return nil, validationError(
			"invalid_database_config",
			fmt.Sprintf("service %q database must be an object", service.GetName()),
			"set database.type to h2 or postgresql",
		)
	}
	if err := rejectUnknownFields(
		service,
		"database",
		database,
		"type",
		"serverResourceId",
		"databaseName",
		"auth",
	); err != nil {
		return nil, err
	}

	var err error
	if value, fieldPresent := database["type"]; fieldPresent {
		document.Type, err = requiredString(service, "database.type", value)
		if err != nil {
			return nil, err
		}
	}
	if value, fieldPresent := database["serverResourceId"]; fieldPresent {
		document.ServerPresent = true
		document.ServerResourceID, err = requiredString(service, "database.serverResourceId", value)
		if err != nil {
			return nil, err
		}
	}
	if value, fieldPresent := database["databaseName"]; fieldPresent {
		document.DatabasePresent = true
		document.DatabaseName, err = requiredString(service, "database.databaseName", value)
		if err != nil {
			return nil, err
		}
	}
	authValue, authPresent := database["auth"]
	if !authPresent {
		return document, nil
	}
	auth, ok := authValue.(map[string]any)
	if !ok {
		return nil, validationError(
			"invalid_database_auth",
			fmt.Sprintf("service %q database.auth must be an object", service.GetName()),
			"set the complete auth object with type and username",
		)
	}
	if err := rejectUnknownFields(
		service,
		"database.auth",
		auth,
		"type",
		"username",
		"administratorName",
		"appIdentity",
	); err != nil {
		return nil, err
	}
	for _, required := range []string{"type", "username"} {
		if _, fieldPresent := auth[required]; !fieldPresent {
			return nil, validationError(
				"missing_database_auth_field",
				fmt.Sprintf("service %q database.auth is missing %q", service.GetName(), required),
				"set the complete auth object with type and username",
			)
		}
	}
	document.Auth = &databaseAuthConfigDocument{}
	document.Auth.Type, err = requiredString(service, "database.auth.type", auth["type"])
	if err != nil {
		return nil, err
	}
	document.Auth.Username, err = requiredString(service, "database.auth.username", auth["username"])
	if err != nil {
		return nil, err
	}
	if value, fieldPresent := auth["administratorName"]; fieldPresent {
		document.Auth.AdministratorNamePresent = true
		document.Auth.AdministratorName, err = requiredString(
			service,
			"database.auth.administratorName",
			value,
		)
		if err != nil {
			return nil, err
		}
	}
	identityValue, identityPresent := auth["appIdentity"]
	if !identityPresent {
		return document, nil
	}
	identity, ok := identityValue.(map[string]any)
	if !ok {
		return nil, validationError(
			"invalid_database_app_identity",
			fmt.Sprintf("service %q database.auth.appIdentity must be an object", service.GetName()),
			"set appIdentity.type and appIdentity.resourceId",
		)
	}
	if err := rejectUnknownFields(
		service,
		"database.auth.appIdentity",
		identity,
		"type",
		"resourceId",
	); err != nil {
		return nil, err
	}
	for _, required := range []string{"type", "resourceId"} {
		if _, fieldPresent := identity[required]; !fieldPresent {
			return nil, validationError(
				"missing_database_app_identity_field",
				fmt.Sprintf("service %q database.auth.appIdentity is missing %q", service.GetName(), required),
				"set appIdentity.type to userAssigned and provide its resourceId",
			)
		}
	}
	document.Auth.AppIdentity = &databaseAppIdentityConfigDocument{}
	document.Auth.AppIdentity.Type, err = requiredString(
		service,
		"database.auth.appIdentity.type",
		identity["type"],
	)
	if err != nil {
		return nil, err
	}
	document.Auth.AppIdentity.ResourceID, err = requiredString(
		service,
		"database.auth.appIdentity.resourceId",
		identity["resourceId"],
	)
	if err != nil {
		return nil, err
	}
	return document, nil
}

func requiredString(service *azdext.ServiceConfig, field string, value any) (string, error) {
	raw, ok := value.(string)
	if !ok {
		return "", validationError(
			"invalid_database_config_value",
			fmt.Sprintf("service %q %s must be a string", service.GetName(), field),
			fmt.Sprintf("set %s to a supported string value", field),
		)
	}
	if strings.TrimSpace(raw) == "" {
		return "", validationError(
			"empty_database_config_value",
			fmt.Sprintf("service %q %s is explicitly empty", service.GetName(), field),
			fmt.Sprintf("set %s to a nonempty supported value", field),
		)
	}
	return raw, nil
}

func rejectUnknownFields(
	service *azdext.ServiceConfig,
	path string,
	values map[string]any,
	supported ...string,
) error {
	for key := range values {
		if !slices.Contains(supported, key) {
			return validationError(
				"unsupported_database_config",
				fmt.Sprintf("service %q %s contains unsupported field %q", service.GetName(), path, key),
				"remove unsupported fields and use the documented azure.springboot database contract",
			)
		}
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
