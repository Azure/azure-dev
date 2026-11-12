// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// #nosec G101 -- public OAuth scope, not a credential.
	postgresTokenScope         = "https://ossrdbms-aad.database.windows.net/.default"
	postgresResourceAPIVersion = "2024-08-01"
	postgresCleanupTimeout     = 2 * time.Minute
	postgresFirewallStaleAfter = time.Hour
)

type postgresAccessReconciler struct {
	client       *azdext.AzdClient
	ipProvider   publicIPProvider
	connect      func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error)
	now          func() time.Time
	randomSuffix func() (string, error)
}

type verifiedPostgresResources struct {
	SubscriptionID           string
	ResourceGroup            string
	ServerName               string
	ServerID                 string
	Host                     string
	AdministratorName        string
	SessionAdministratorName string
	IdentityPrincipalID      string
	IdentityClientID         string
}

func (r *verifiedPostgresResources) sessionAdministratorName() string {
	if r.SessionAdministratorName != "" {
		return r.SessionAdministratorName
	}
	return r.AdministratorName
}

func newPostgresAccessReconciler(client *azdext.AzdClient) *postgresAccessReconciler {
	return &postgresAccessReconciler{
		client:     client,
		ipProvider: newPublicIPProvider(),
		connect:    pgx.ConnectConfig,
		now:        time.Now,
		randomSuffix: func() (string, error) {
			bytes := make([]byte, 8)
			if _, err := rand.Read(bytes); err != nil {
				return "", fmt.Errorf("generating PostgreSQL firewall rule ownership suffix: %w", err)
			}
			return hex.EncodeToString(bytes), nil
		},
	}
}

func (r *postgresAccessReconciler) Reconcile(
	ctx context.Context,
	envName string,
	database resolvedDatabase,
	values map[string]string,
) error {
	resources, credential, err := r.verifyResources(ctx, envName, database, values)
	if err != nil {
		return err
	}
	firewalls, err := armpostgresqlflexibleservers.NewFirewallRulesClient(
		resources.SubscriptionID,
		credential,
		nil,
	)
	if err != nil {
		return fmt.Errorf("creating PostgreSQL firewall client: %w", err)
	}
	return nonDisclosureError(r.withBootstrapFirewall(ctx, firewalls, resources, envName, func(ctx context.Context) error {
		return r.reconcileSQL(ctx, resources, credential, database, values, envName)
	}))
}

func (r *postgresAccessReconciler) withBootstrapFirewall(
	ctx context.Context,
	firewalls *armpostgresqlflexibleservers.FirewallRulesClient,
	resources *verifiedPostgresResources,
	envName string,
	run func(context.Context) error,
) error {
	allPublic, err := hasAllPublicIPv4Rule(ctx, firewalls, resources.ResourceGroup, resources.ServerName)
	if err != nil {
		return err
	}
	if allPublic {
		return run(ctx)
	}
	ip, err := discoverBootstrapIPv4(ctx, os.LookupEnv, r.ipProvider)
	if err != nil {
		return err
	}
	prefix := bootstrapFirewallRulePrefix(envName, resources.ServerID)
	now := r.now()
	if err := r.recoverStaleFirewallRules(
		ctx,
		firewalls,
		resources.ResourceGroup,
		resources.ServerName,
		prefix,
		now,
	); err != nil {
		return nonDisclosureError(err)
	}
	suffix, err := r.randomSuffix()
	if err != nil {
		return err
	}
	ruleName := bootstrapFirewallRuleName(prefix, now, suffix)
	primaryErr := withTemporaryFirewall(
		ctx,
		func(createContext context.Context) error {
			poller, createErr := firewalls.BeginCreateOrUpdate(
				createContext,
				resources.ResourceGroup,
				resources.ServerName,
				ruleName,
				armpostgresqlflexibleservers.FirewallRule{
					Properties: &armpostgresqlflexibleservers.FirewallRuleProperties{
						StartIPAddress: new(ip),
						EndIPAddress:   new(ip),
					},
				},
				nil,
			)
			if createErr != nil {
				return fmt.Errorf("creating an exact owned PostgreSQL bootstrap firewall rule: %w", createErr)
			}
			if _, createErr = poller.PollUntilDone(createContext, nil); createErr != nil {
				return fmt.Errorf(
					"waiting for the exact owned PostgreSQL bootstrap firewall rule: %w",
					createErr,
				)
			}
			return nil
		},
		func(cleanupContext context.Context) error {
			return deleteBootstrapFirewallRule(
				cleanupContext,
				firewalls,
				resources.ResourceGroup,
				resources.ServerName,
				ruleName,
			)
		},
		run,
	)
	return primaryErr
}

func (r *postgresAccessReconciler) verifyResources(
	ctx context.Context,
	envName string,
	database resolvedDatabase,
	values map[string]string,
) (*verifiedPostgresResources, azcore.TokenCredential, error) {
	serverID, err := arm.ParseResourceID(database.ServerResourceID)
	if err != nil ||
		!strings.EqualFold(serverID.ResourceType.String(), "Microsoft.DBforPostgreSQL/flexibleServers") {
		return nil, nil, validationError(
			"invalid_postgres_resource_id",
			"database.serverResourceId is not a PostgreSQL Flexible Server resource ID",
			"restore the PostgreSQL server resource ID from the current root deployment",
		)
	}
	if !strings.EqualFold(serverID.SubscriptionID, values[azureSubscriptionIDEnv]) ||
		!strings.EqualFold(serverID.ResourceGroupName, values[azureResourceGroupEnv]) {
		return nil, nil, validationError(
			"mismatched_postgres_resource_scope",
			"the selected PostgreSQL server does not belong to the current subscription and resource group",
			"restore the current root deployment outputs and selected environment",
		)
	}
	credential, err := credentialForSubscription(ctx, r.client, serverID.SubscriptionID)
	if err != nil {
		return nil, nil, err
	}
	servers, err := armpostgresqlflexibleservers.NewServersClient(serverID.SubscriptionID, credential, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("creating PostgreSQL server client: %w", err)
	}
	serverResponse, err := servers.Get(ctx, serverID.ResourceGroupName, serverID.Name, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the selected PostgreSQL server: %w", err)
	}
	server := serverResponse.Server
	if server.ID == nil || !strings.EqualFold(*server.ID, database.ServerResourceID) ||
		server.Properties == nil ||
		server.Properties.FullyQualifiedDomainName == nil {
		return nil, nil, validationError(
			"postgres_resource_ownership_unverified",
			"the selected PostgreSQL server identity or azd ownership could not be verified",
			"restore the authored root deployment outputs or select the original azd environment",
		)
	}
	if err := checkResourceOwnership(ctx, r.client, envName, values, *server.ID, server.Tags); err != nil {
		return nil, nil, err
	}
	host := strings.TrimSpace(*server.Properties.FullyQualifiedDomainName)
	if host == "" || !strings.EqualFold(host, strings.TrimSpace(values[postgresHostEnv])) {
		return nil, nil, validationError(
			"mismatched_postgres_host",
			"the current PostgreSQL hostname does not match the root deployment output",
			"restore the PostgreSQL hostname from the current root deployment output",
		)
	}
	databases, err := armpostgresqlflexibleservers.NewDatabasesClient(serverID.SubscriptionID, credential, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("creating PostgreSQL databases client: %w", err)
	}
	databaseResponse, err := databases.Get(
		ctx,
		serverID.ResourceGroupName,
		serverID.Name,
		database.Name,
		nil,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the selected PostgreSQL database: %w", err)
	}
	if databaseResponse.Database.ID == nil ||
		!strings.EqualFold(
			*databaseResponse.Database.ID,
			database.ServerResourceID+"/databases/"+database.Name,
		) {
		return nil, nil, validationError(
			"mismatched_postgres_database",
			"the selected PostgreSQL database identity could not be verified",
			"restore the authored database resource and root deployment outputs",
		)
	}

	generic, err := armresources.NewClient(serverID.SubscriptionID, credential, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("creating Azure resources client: %w", err)
	}
	resourceResponse, err := generic.GetByID(ctx, database.ServerResourceID, postgresResourceAPIVersion, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("reading PostgreSQL authentication configuration: %w", err)
	}
	properties, ok := resourceResponse.GenericResource.Properties.(map[string]any)
	if !ok {
		return nil, nil, validationError(
			"postgres_authentication_unverified",
			"the PostgreSQL server returned an invalid authentication configuration",
			"reprovision the authored PostgreSQL server before retrying",
		)
	}
	authConfig, _ := properties["authConfig"].(map[string]any)
	activeDirectoryAuth, _ := authConfig["activeDirectoryAuth"].(string)
	passwordAuth, _ := authConfig["passwordAuth"].(string)

	result := &verifiedPostgresResources{
		SubscriptionID: serverID.SubscriptionID,
		ResourceGroup:  serverID.ResourceGroupName,
		ServerName:     serverID.Name,
		ServerID:       database.ServerResourceID,
		Host:           host,
	}
	switch database.Authentication {
	case databaseAuthenticationPassword:
		if !strings.EqualFold(activeDirectoryAuth, "Disabled") ||
			!strings.EqualFold(passwordAuth, "Enabled") ||
			server.Properties.AdministratorLogin == nil {
			return nil, nil, validationError(
				"postgres_password_authentication_unverified",
				"the selected PostgreSQL server is not configured for the authored password authentication mode",
				"reprovision the server with password auth enabled and Entra auth disabled",
			)
		}
		result.AdministratorName = strings.TrimSpace(*server.Properties.AdministratorLogin)
		if result.AdministratorName != database.AdministratorName {
			return nil, nil, validationError(
				"postgres_administrator_unverified",
				"the actual PostgreSQL administrator login does not match the current deployment output",
				"verify the selected database administrator and reprovision the current environment",
			)
		}
	case databaseAuthenticationManagedIdentity:
		if !strings.EqualFold(activeDirectoryAuth, "Enabled") ||
			!strings.EqualFold(passwordAuth, "Disabled") {
			return nil, nil, validationError(
				"postgres_managed_identity_authentication_unverified",
				"the selected PostgreSQL server is not Entra-only as required by managedidentity auth",
				"reprovision the server with Entra auth enabled and password auth disabled",
			)
		}
		if err := verifyPostgresAdministrator(
			ctx,
			generic,
			database,
			values,
			authConfig,
		); err != nil {
			return nil, nil, err
		}
		result.AdministratorName = database.AdministratorName
		principalID, clientID, verifyErr := r.verifySelectedIdentity(
			ctx,
			credential,
			envName,
			database,
			values,
		)
		if verifyErr != nil {
			return nil, nil, verifyErr
		}
		result.IdentityPrincipalID = principalID
		result.IdentityClientID = clientID
	}
	return result, credential, nil
}

func verifyPostgresAdministrator(
	ctx context.Context,
	resources *armresources.Client,
	database resolvedDatabase,
	values map[string]string,
	authConfig map[string]any,
) error {
	objectID := strings.TrimSpace(values[databaseAdministratorObjectIDEnv])
	if !validAdministratorObjectID(objectID) {
		return validationError(
			"missing_database_administrator_object_id",
			"the selected PostgreSQL User administrator object ID is missing or invalid",
			"run azd provision again with the configured User administrator inputs",
		)
	}
	tenantID, _ := authConfig["tenantId"].(string)
	if !validAdministratorObjectID(tenantID) {
		return validationError(
			"postgres_administrator_unverified",
			"the PostgreSQL authentication tenant could not be verified",
			"restore the server's Entra authentication configuration and reprovision",
		)
	}
	administratorID := database.ServerResourceID + "/administrators/" + objectID
	response, err := resources.GetByID(ctx, administratorID, postgresResourceAPIVersion, nil)
	if err != nil {
		return fmt.Errorf("reading the PostgreSQL Entra administrator: %w", err)
	}
	properties, ok := response.GenericResource.Properties.(map[string]any)
	if !ok || response.GenericResource.ID == nil ||
		!strings.EqualFold(*response.GenericResource.ID, administratorID) {
		return validationError(
			"postgres_administrator_unverified",
			"the PostgreSQL Entra administrator returned invalid metadata",
			"reprovision the server with the configured User as its Entra administrator",
		)
	}
	principalName, _ := properties["principalName"].(string)
	principalType, _ := properties["principalType"].(string)
	actualObjectID, _ := properties["objectId"].(string)
	actualTenantID, _ := properties["tenantId"].(string)
	if principalName != database.AdministratorName ||
		!strings.EqualFold(principalType, "User") ||
		!strings.EqualFold(actualObjectID, objectID) ||
		!strings.EqualFold(actualTenantID, tenantID) {
		return validationError(
			"postgres_administrator_unverified",
			"the actual PostgreSQL Entra administrator does not match the configured User identity, tenant and name",
			"reprovision with the selected User administrator or create a new azd environment",
		)
	}
	return nil
}

func (r *postgresAccessReconciler) verifySelectedIdentity(
	ctx context.Context,
	credential azcore.TokenCredential,
	envName string,
	database resolvedDatabase,
	values map[string]string,
) (string, string, error) {
	if database.AppIdentity == nil {
		return "", "", validationError(
			"managed_identity_binding_missing",
			"managedidentity auth has no selected application identity",
			"restore the selected Container App managed identity and current deployment outputs",
		)
	}
	appID, err := arm.ParseResourceID(values[containerAppIDEnv])
	if err != nil ||
		!strings.EqualFold(appID.ResourceType.String(), "Microsoft.App/containerApps") {
		return "", "", validationError(
			"invalid_container_app_resource_id",
			"the provisioned Container App resource ID is invalid",
			"restore the service Container App from the current deployment or reprovision the selected environment",
		)
	}
	if !strings.EqualFold(appID.SubscriptionID, values[azureSubscriptionIDEnv]) ||
		!strings.EqualFold(appID.ResourceGroupName, values[azureResourceGroupEnv]) {
		return "", "", validationError(
			"mismatched_container_app_resource_scope",
			"the selected Container App does not belong to the current subscription and resource group",
			"restore the current root deployment outputs and selected environment",
		)
	}
	apps, err := armappcontainers.NewContainerAppsClient(appID.SubscriptionID, credential, nil)
	if err != nil {
		return "", "", fmt.Errorf("creating Container Apps client: %w", err)
	}
	response, err := apps.Get(ctx, appID.ResourceGroupName, appID.Name, nil)
	if err != nil {
		return "", "", fmt.Errorf("reading the provisioned Container App identity: %w", err)
	}
	app := response.ContainerApp
	if app.ID == nil || !strings.EqualFold(*app.ID, values[containerAppIDEnv]) ||
		app.Identity == nil {
		return "", "", validationError(
			"container_app_identity_unverified",
			"the selected Container App ownership or managed identity could not be verified",
			"restore the authored Container App and selected azd environment",
		)
	}
	if err := checkResourceOwnership(ctx, r.client, envName, values, *app.ID, app.Tags); err != nil {
		return "", "", err
	}
	return selectedApplicationIdentity(database, values, &app)
}

func selectedApplicationIdentity(
	database resolvedDatabase,
	values map[string]string,
	app *armappcontainers.ContainerApp,
) (string, string, error) {
	if database.AppIdentity == nil || app == nil || app.ID == nil || app.Identity == nil {
		return "", "", validationError(
			"container_app_identity_unverified",
			"the selected Container App managed identity could not be verified",
			"restore the authored Container App identity and current deployment outputs",
		)
	}
	if database.AppIdentity.Type == databaseAppIdentityTypeSystemAssigned {
		if !strings.EqualFold(database.AppIdentity.ResourceID, *app.ID) ||
			!hasSystemAssignedIdentity(app.Identity) || app.Identity.PrincipalID == nil {
			return "", "", validationError(
				"database_app_identity_not_attached",
				"the selected system-assigned identity is not enabled on the provisioned Container App",
				"restore the authored system-assigned identity and application principal output",
			)
		}
		principalID := strings.TrimSpace(*app.Identity.PrincipalID)
		if principalID == "" || !strings.EqualFold(principalID, values[serviceIdentityPrincipalIDEnv]) {
			return "", "", validationError(
				"database_app_identity_output_mismatch",
				"the system-assigned identity principal does not match the current deployment output",
				"restore the current application principal output before retrying",
			)
		}
		return principalID, "", nil
	}
	if database.AppIdentity.Type != databaseAppIdentityTypeUserAssigned {
		return "", "", validationError(
			"unsupported_database_app_identity",
			"the selected application managed identity type is unsupported",
			"select the Container App system identity or one attached user-assigned identity",
		)
	}
	var selected *armappcontainers.UserAssignedIdentity
	for resourceID, identity := range app.Identity.UserAssignedIdentities {
		if strings.EqualFold(resourceID, database.AppIdentity.ResourceID) {
			selected = identity
			break
		}
	}
	if selected == nil || selected.PrincipalID == nil || selected.ClientID == nil {
		return "", "", validationError(
			"database_app_identity_not_attached",
			"the selected user-assigned identity is not attached to the provisioned Container App",
			"restore the authored identity attachment and service identity output",
		)
	}
	principalID := strings.TrimSpace(*selected.PrincipalID)
	clientID := strings.TrimSpace(*selected.ClientID)
	if principalID == "" || clientID == "" ||
		!strings.EqualFold(principalID, values[serviceIdentityPrincipalIDEnv]) ||
		!strings.EqualFold(clientID, values[serviceIdentityClientIDEnv]) {
		return "", "", validationError(
			"database_app_identity_output_mismatch",
			"the selected user-assigned identity object or client ID does not match the current deployment outputs",
			"restore the current service identity outputs before retrying",
		)
	}
	return principalID, clientID, nil
}

func withTemporaryFirewall(
	ctx context.Context,
	create func(context.Context) error,
	cleanup func(context.Context) error,
	run func(context.Context) error,
) error {
	primaryErr := create(ctx)
	if primaryErr == nil {
		primaryErr = run(ctx)
	}

	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), postgresCleanupTimeout)
	defer cancel()
	cleanupErr := cleanup(cleanupContext)
	return errors.Join(primaryErr, cleanupErr)
}

func bootstrapFirewallRulePrefix(envName, serverID string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(envName + "|" + serverID)))
	return "azd-springboot-bootstrap-" + hex.EncodeToString(sum[:6]) + "-"
}

func bootstrapFirewallRuleName(prefix string, now time.Time, suffix string) string {
	return prefix + strconv.FormatInt(now.UTC().Unix(), 10) + "-" + suffix
}

func staleBootstrapFirewallRules(prefix string, now time.Time, names []string) []string {
	cutoff := now.UTC().Add(-postgresFirewallStaleAfter).Unix()
	stale := make([]string, 0)
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(name, prefix), "-")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		ownershipBytes, decodeErr := hex.DecodeString(parts[1])
		if decodeErr != nil || len(ownershipBytes) != 8 {
			continue
		}
		timestamp, err := strconv.ParseInt(parts[0], 10, 64)
		if err == nil && timestamp < cutoff {
			stale = append(stale, name)
		}
	}
	return stale
}

func (r *postgresAccessReconciler) recoverStaleFirewallRules(
	ctx context.Context,
	client *armpostgresqlflexibleservers.FirewallRulesClient,
	resourceGroup string,
	serverName string,
	prefix string,
	now time.Time,
) error {
	pager := client.NewListByServerPager(resourceGroup, serverName, nil)
	names := make([]string, 0)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("listing owned PostgreSQL bootstrap firewall rules: %w", err)
		}
		for _, rule := range page.Value {
			if rule != nil && rule.Name != nil {
				names = append(names, *rule.Name)
			}
		}
	}
	for _, name := range staleBootstrapFirewallRules(prefix, now, names) {
		if err := deleteBootstrapFirewallRule(ctx, client, resourceGroup, serverName, name); err != nil {
			return fmt.Errorf("recovering stale owned PostgreSQL bootstrap firewall rule %q: %w", name, err)
		}
	}
	return nil
}

func deleteBootstrapFirewallRule(
	ctx context.Context,
	client *armpostgresqlflexibleservers.FirewallRulesClient,
	resourceGroup string,
	serverName string,
	ruleName string,
) error {
	poller, err := client.BeginDelete(ctx, resourceGroup, serverName, ruleName, nil)
	if isAzureNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("deleting the exact owned PostgreSQL bootstrap firewall rule: %w", err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil && !isAzureNotFound(err) {
		return fmt.Errorf("waiting to delete the exact owned PostgreSQL bootstrap firewall rule: %w", err)
	}
	return nil
}

func isAzureNotFound(err error) bool {
	if err == nil {
		return false
	}
	responseError, ok := errors.AsType[*azcore.ResponseError](err)
	return ok && responseError.StatusCode == 404
}

func (r *postgresAccessReconciler) reconcileSQL(
	ctx context.Context,
	resources *verifiedPostgresResources,
	credential azcore.TokenCredential,
	database resolvedDatabase,
	values map[string]string,
	envName string,
) error {
	password := ""
	switch database.Authentication {
	case databaseAuthenticationPassword:
		password = values[databasePasswordEnv]
		if password == "" {
			return missingExistingDatabasePasswordError()
		}
	case databaseAuthenticationManagedIdentity:
		token, err := credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{postgresTokenScope}})
		if err != nil {
			return fmt.Errorf("acquiring the developer PostgreSQL access token: %w", err)
		}
		password = token.Token
	}
	return r.reconcileSQLWithAdministratorPassword(ctx, resources, database, values, envName, password)
}

func (r *postgresAccessReconciler) reconcileSQLWithAdministratorPassword(
	ctx context.Context,
	resources *verifiedPostgresResources,
	database resolvedDatabase,
	values map[string]string,
	envName string,
	administratorPassword string,
) error {
	marker := databaseRoleMarker(envName, database)
	if err := r.verifyDatabaseScope(
		ctx,
		resources,
		database,
		administratorPassword,
		marker,
	); err != nil {
		return err
	}
	if err := r.reconcileRole(
		ctx,
		resources,
		database,
		administratorPassword,
		values[databaseAppPasswordEnv],
		marker,
	); err != nil {
		return err
	}
	if err := r.reconcileDatabasePrivileges(
		ctx,
		resources,
		database,
		administratorPassword,
		marker,
	); err != nil {
		return err
	}
	if database.Authentication == databaseAuthenticationPassword {
		return r.verifyPasswordApplicationLogin(ctx, resources, database, values[databaseAppPasswordEnv])
	}
	return nil
}

func (r *postgresAccessReconciler) verifyPasswordApplicationLogin(
	ctx context.Context,
	resources *verifiedPostgresResources,
	database resolvedDatabase,
	applicationPassword string,
) error {
	if applicationPassword == "" {
		return missingExistingDatabaseAppPasswordError()
	}
	config, err := postgresConnectionConfig(
		resources.Host,
		database.Name,
		database.Username,
		applicationPassword,
	)
	if err != nil {
		return err
	}
	delete(config.RuntimeParams, "search_path")
	connection, err := r.connectConfigWithRetry(ctx, config)
	if err != nil {
		return errors.Join(
			validationError(
				"database_application_password_mismatch",
				fmt.Sprintf(
					"the effective Spring password cannot authenticate PostgreSQL role %q",
					database.Username,
				),
				"restore the password for the existing application role or use a new application username",
			),
			err,
		)
	}
	defer connection.Close(context.WithoutCancel(ctx))

	var username string
	var databaseName string
	if err := connection.QueryRow(
		ctx,
		"SELECT current_user, pg_catalog.current_database()",
	).Scan(&username, &databaseName); err != nil {
		return fmt.Errorf("verifying the ordinary PostgreSQL application login: %w", nonDisclosureError(err))
	}
	if username != database.Username || databaseName != database.Name {
		return validationError(
			"database_application_login_mismatch",
			"the effective Spring credential connected with an unexpected PostgreSQL identity or database",
			"restore the application role and database binding before deployment",
		)
	}
	return nil
}

func (r *postgresAccessReconciler) verifyDatabaseScope(
	ctx context.Context,
	resources *verifiedPostgresResources,
	database resolvedDatabase,
	administratorPassword string,
	marker string,
) error {
	connection, err := r.connectWithRetry(
		ctx,
		resources.Host,
		database.Name,
		resources.sessionAdministratorName(),
		administratorPassword,
	)
	if err != nil {
		return err
	}
	defer connection.Close(context.WithoutCancel(ctx))

	transaction, err := connection.Begin(ctx)
	if err != nil {
		return fmt.Errorf("starting PostgreSQL scope verification transaction: %w", err)
	}
	defer transaction.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	if _, err := transaction.Exec(
		ctx,
		"SELECT pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended($1, 0))",
		"azure.springboot.grants."+database.Name,
	); err != nil {
		return fmt.Errorf("locking PostgreSQL scope verification: %w", err)
	}
	_, err = validateOwnedDatabaseScope(
		ctx,
		transaction,
		database.Name,
		resources.AdministratorName,
		marker,
	)
	return err
}

func databaseRoleMarker(envName string, database resolvedDatabase) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		envName,
		strings.ToLower(database.ServerResourceID),
		database.Name,
		database.Username,
		string(database.Authentication),
	}, "|")))
	return "azure.springboot:" + hex.EncodeToString(sum[:])
}

func (r *postgresAccessReconciler) reconcileRole(
	ctx context.Context,
	resources *verifiedPostgresResources,
	database resolvedDatabase,
	administratorPassword string,
	applicationPassword string,
	marker string,
) error {
	connection, err := r.connectWithRetry(
		ctx,
		resources.Host,
		"postgres",
		resources.sessionAdministratorName(),
		administratorPassword,
	)
	if err != nil {
		return err
	}
	defer connection.Close(context.WithoutCancel(ctx))

	transaction, err := connection.Begin(ctx)
	if err != nil {
		return fmt.Errorf("starting PostgreSQL role transaction: %w", err)
	}
	defer transaction.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	if _, err := transaction.Exec(
		ctx,
		"SELECT pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended($1, 0))",
		"azure.springboot.role."+database.Username,
	); err != nil {
		return fmt.Errorf("locking PostgreSQL application role reconciliation: %w", err)
	}

	role, err := readRoleState(ctx, transaction, database.Username)
	if err != nil {
		return err
	}
	if role.Exists && role.Comment != marker {
		return validationError(
			"database_role_ownership_unverified",
			fmt.Sprintf("PostgreSQL role %q already exists without the current azd ownership marker", database.Username),
			"use a new application username or explicitly migrate the existing role",
		)
	}
	if !role.Exists {
		switch database.Authentication {
		case databaseAuthenticationPassword:
			if applicationPassword == "" {
				return missingExistingDatabaseAppPasswordError()
			}
			statement := "CREATE ROLE " + pgx.Identifier{database.Username}.Sanitize() +
				" LOGIN PASSWORD " + quoteSQLLiteral(applicationPassword)
			if _, err := transaction.Exec(ctx, statement); err != nil {
				return fmt.Errorf("creating the ordinary PostgreSQL application login: %w", err)
			}
		case databaseAuthenticationManagedIdentity:
			if _, err := transaction.Exec(
				ctx,
				"SELECT * FROM pg_catalog.pgaadauth_create_principal_with_oid($1, $2, 'service', false, false)",
				database.Username,
				resources.IdentityPrincipalID,
			); err != nil {
				return fmt.Errorf("creating the non-admin PostgreSQL Entra application mapping: %w", err)
			}
		}
		comment := "COMMENT ON ROLE " + pgx.Identifier{database.Username}.Sanitize() +
			" IS " + quoteSQLLiteral(marker)
		if _, err := transaction.Exec(ctx, comment); err != nil {
			return fmt.Errorf("marking the owned PostgreSQL application role: %w", err)
		}
	}
	if database.Authentication == databaseAuthenticationManagedIdentity {
		if err := verifyEntraRoleMapping(
			ctx,
			transaction,
			database.Username,
			resources.IdentityPrincipalID,
		); err != nil {
			return err
		}
	}
	if err := verifyRoleIsOrdinary(ctx, transaction, database.Username, marker); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("committing PostgreSQL application role reconciliation: %w", err)
	}
	return nil
}

type roleState struct {
	Exists  bool
	Comment string
}

type postgresRowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readRoleState(ctx context.Context, transaction pgx.Tx, username string) (*roleState, error) {
	state := &roleState{}
	err := transaction.QueryRow(
		ctx,
		`SELECT true, COALESCE(pg_catalog.shobj_description(oid, 'pg_authid'), '')
		 FROM pg_catalog.pg_roles WHERE rolname = $1`,
		username,
	).Scan(&state.Exists, &state.Comment)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading PostgreSQL application role: %w", err)
	}
	return state, nil
}

func verifyEntraRoleMapping(
	ctx context.Context,
	transaction postgresRowQuerier,
	username string,
	expectedObjectID string,
) error {
	var objectID, principalType string
	var isAdmin int32
	err := transaction.QueryRow(
		ctx,
		`SELECT objectid, principaltype, isadmin
		 FROM pg_catalog.pgaadauth_list_principals(false)
		 WHERE rolname = $1`,
		username,
	).Scan(&objectID, &principalType, &isAdmin)
	if err != nil {
		return fmt.Errorf("verifying PostgreSQL Entra application mapping: %w", err)
	}
	if !strings.EqualFold(objectID, expectedObjectID) ||
		!strings.EqualFold(principalType, "service") ||
		isAdmin != 0 {
		return validationError(
			"database_app_identity_mapping_conflict",
			fmt.Sprintf("PostgreSQL role %q has a conflicting or elevated Entra mapping", username),
			"use a new application username or explicitly remove the conflicting mapping",
		)
	}
	return nil
}

func verifyRoleIsOrdinary(
	ctx context.Context,
	transaction pgx.Tx,
	username string,
	expectedComment string,
) error {
	var login, superuser, createDB, createRole, replication, bypassRLS bool
	var comment string
	err := transaction.QueryRow(
		ctx,
		`SELECT rolcanlogin, rolsuper, rolcreatedb, rolcreaterole, rolreplication, rolbypassrls,
		        COALESCE(pg_catalog.shobj_description(oid, 'pg_authid'), '')
		 FROM pg_catalog.pg_roles WHERE rolname = $1`,
		username,
	).Scan(&login, &superuser, &createDB, &createRole, &replication, &bypassRLS, &comment)
	if err != nil {
		return fmt.Errorf("verifying PostgreSQL application role attributes: %w", err)
	}
	if !login || superuser || createDB || createRole || replication || bypassRLS || comment != expectedComment {
		return validationError(
			"database_role_is_elevated",
			fmt.Sprintf("PostgreSQL role %q is not an owned ordinary non-admin login", username),
			"remove the conflicting role or explicitly migrate it before retrying",
		)
	}
	var memberships int
	if err := transaction.QueryRow(
		ctx,
		`SELECT count(*) FROM pg_catalog.pg_auth_members
		 WHERE member = (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = $1)`,
		username,
	).Scan(&memberships); err != nil {
		return fmt.Errorf("verifying PostgreSQL application role memberships: %w", err)
	}
	if memberships != 0 {
		return validationError(
			"database_role_has_memberships",
			fmt.Sprintf("PostgreSQL role %q inherits another role", username),
			"remove inherited role memberships before retrying",
		)
	}
	return nil
}

func (r *postgresAccessReconciler) reconcileDatabasePrivileges(
	ctx context.Context,
	resources *verifiedPostgresResources,
	database resolvedDatabase,
	administratorPassword string,
	marker string,
) error {
	connection, err := r.connectWithRetry(
		ctx,
		resources.Host,
		database.Name,
		resources.sessionAdministratorName(),
		administratorPassword,
	)
	if err != nil {
		return err
	}
	defer connection.Close(context.WithoutCancel(ctx))

	transaction, err := connection.Begin(ctx)
	if err != nil {
		return fmt.Errorf("starting PostgreSQL grant transaction: %w", err)
	}
	defer transaction.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	if _, err := transaction.Exec(
		ctx,
		"SELECT pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended($1, 0))",
		"azure.springboot.grants."+database.Name,
	); err != nil {
		return fmt.Errorf("locking PostgreSQL application grants: %w", err)
	}
	if err := reconcileApplicationPrivileges(
		ctx, transaction, database, resources.AdministratorName, marker,
	); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("committing PostgreSQL application grants: %w", err)
	}
	return nil
}

func reconcileApplicationPrivileges(
	ctx context.Context,
	transaction pgx.Tx,
	database resolvedDatabase,
	administratorName string,
	marker string,
) error {
	if err := verifyRoleIsOrdinary(ctx, transaction, database.Username, marker); err != nil {
		return err
	}
	if err := ensureOwnedDatabaseScope(
		ctx,
		transaction,
		database.Name,
		administratorName,
		marker,
	); err != nil {
		return err
	}
	if err := verifyApplicationObjectOwnership(
		ctx,
		transaction,
		database.Username,
		administratorName,
	); err != nil {
		return err
	}

	application := pgx.Identifier{database.Username}.Sanitize()
	databaseIdentifier := pgx.Identifier{database.Name}.Sanitize()
	statements := []string{
		"REVOKE ALL ON DATABASE " + databaseIdentifier + " FROM " + application,
		"GRANT CONNECT ON DATABASE " + databaseIdentifier + " TO " + application,
		"REVOKE CREATE, TEMPORARY ON DATABASE " + databaseIdentifier + " FROM PUBLIC",
		"REVOKE CREATE ON SCHEMA public FROM PUBLIC",
		"REVOKE ALL ON SCHEMA public FROM " + application,
		"GRANT USAGE, CREATE ON SCHEMA public TO " + application,
	}
	for _, statement := range statements {
		if _, err := transaction.Exec(ctx, statement); err != nil {
			return fmt.Errorf("reconciling PostgreSQL application grants: %w", err)
		}
	}
	if err := verifyEffectiveApplicationAccess(ctx, transaction, database.Username, database.Name); err != nil {
		return err
	}
	if err := verifyEffectiveObjectPrivileges(ctx, transaction, database.Username); err != nil {
		return err
	}
	return nil
}

func ensureOwnedDatabaseScope(
	ctx context.Context,
	transaction pgx.Tx,
	databaseName string,
	administratorName string,
	marker string,
) error {
	claimRequired, err := validateOwnedDatabaseScope(
		ctx,
		transaction,
		databaseName,
		administratorName,
		marker,
	)
	if err != nil {
		return err
	}
	if !claimRequired {
		return nil
	}
	statement := "COMMENT ON DATABASE " + pgx.Identifier{databaseName}.Sanitize() +
		" IS " + quoteSQLLiteral(marker)
	if _, err := transaction.Exec(ctx, statement); err != nil {
		return fmt.Errorf("marking the owned PostgreSQL database scope: %w", err)
	}
	return nil
}

func validateOwnedDatabaseScope(
	ctx context.Context,
	transaction pgx.Tx,
	databaseName string,
	administratorName string,
	marker string,
) (bool, error) {
	var owner, comment string
	var administratorIsMember bool
	if err := transaction.QueryRow(
		ctx,
		`SELECT pg_catalog.pg_get_userbyid(datdba),
		        COALESCE(pg_catalog.shobj_description(oid, 'pg_database'), ''),
		        CASE
		          WHEN pg_catalog.pg_get_userbyid(datdba) = $1 THEN true
		          ELSE pg_catalog.pg_has_role($1, datdba, 'MEMBER')
		        END
		 FROM pg_catalog.pg_database WHERE datname = pg_catalog.current_database()`,
		administratorName,
	).Scan(&owner, &comment, &administratorIsMember); err != nil {
		return false, fmt.Errorf("verifying PostgreSQL database scope ownership: %w", err)
	}
	if err := validateDatabaseOwner(databaseName, owner, administratorName, administratorIsMember); err != nil {
		return false, err
	}
	if comment == marker {
		return false, nil
	}
	if comment != "" {
		return false, validationError(
			"database_scope_ownership_unverified",
			fmt.Sprintf("PostgreSQL database %q has an existing ownership marker", databaseName),
			"use a fresh database or explicitly migrate the existing database",
		)
	}
	var objects int
	if err := transaction.QueryRow(
		ctx,
		`WITH user_schemas AS (
		   SELECT oid, nspname
		   FROM pg_namespace
		   WHERE nspname NOT IN ('pg_catalog', 'information_schema', 'public')
		     AND nspname !~ '^pg_toast'
		     AND nspname !~ '^pg_temp_'
		 ), application_objects AS (
		   SELECT 1
		   FROM pg_class c
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		   WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'S', 'v', 'm', 'f')
		   UNION ALL
		   SELECT 1 FROM user_schemas
		   UNION ALL
		   SELECT 1
		   FROM pg_proc p
		   JOIN pg_namespace n ON n.oid = p.pronamespace
		   WHERE n.nspname = 'public'
		   UNION ALL
		   SELECT 1
		   FROM pg_type t
		   JOIN pg_namespace n ON n.oid = t.typnamespace
		   WHERE n.nspname = 'public' AND t.typrelid = 0 AND t.typtype IN ('c', 'd', 'e', 'r')
		 )
		 SELECT count(*) FROM application_objects`,
	).Scan(&objects); err != nil {
		return false, fmt.Errorf("checking the PostgreSQL database for existing application objects: %w", err)
	}
	if objects != 0 {
		return false, validationError(
			"database_scope_is_not_fresh",
			fmt.Sprintf("PostgreSQL database %q contains unowned application objects", databaseName),
			"use a fresh database; automatic adoption and hardening of existing objects is blocked",
		)
	}
	return true, nil
}

func validateDatabaseOwner(
	databaseName string,
	owner string,
	administratorName string,
	administratorIsMember bool,
) error {
	if owner == administratorName {
		return nil
	}
	platformOwner := owner == "azure_pg_admin" || owner == "azure_ad_admin"
	if platformOwner && administratorIsMember {
		return nil
	}
	return validationError(
		"database_owner_unverified",
		fmt.Sprintf("PostgreSQL database %q is owned by an unexpected or inaccessible role", databaseName),
		"use a fresh ARM-created database owned by the verified administrator or a verified Azure administrator role",
	)
}

func verifyApplicationObjectOwnership(
	ctx context.Context,
	transaction pgx.Tx,
	applicationUsername string,
	administratorName string,
) error {
	var databaseOwner string
	if err := transaction.QueryRow(
		ctx,
		`SELECT pg_catalog.pg_get_userbyid(datdba)
		 FROM pg_catalog.pg_database WHERE datname = pg_catalog.current_database()`,
	).Scan(&databaseOwner); err != nil {
		return fmt.Errorf("verifying PostgreSQL database ownership: %w", err)
	}
	if databaseOwner == applicationUsername {
		return validationError(
			"database_application_owns_database",
			"the application role owns the target database",
			"migrate ownership to the developer provisioning identity before retrying",
		)
	}
	rows, err := transaction.Query(
		ctx,
		`SELECT n.nspname, c.relname, pg_get_userbyid(c.relowner), c.relkind::text
		 FROM pg_class c
		 JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
		   AND n.nspname !~ '^pg_toast'
		   AND n.nspname !~ '^pg_temp_'
		   AND c.relkind IN ('r', 'p', 'S', 'v', 'm', 'f')
		 UNION ALL
		 SELECT n.nspname, p.proname, pg_get_userbyid(p.proowner), 'function'
		 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		 WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
		   AND n.nspname !~ '^pg_toast' AND n.nspname !~ '^pg_temp_'
		 UNION ALL
		 SELECT n.nspname, t.typname, pg_get_userbyid(t.typowner), 'type'
		 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
		 WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
		   AND n.nspname !~ '^pg_toast' AND n.nspname !~ '^pg_temp_'
		   AND t.typrelid = 0 AND t.typtype IN ('c', 'd', 'e', 'r', 'm')`,
	)
	if err != nil {
		return fmt.Errorf("reading PostgreSQL application object ownership: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var schema, name, owner, kind string
		if err := rows.Scan(&schema, &name, &owner, &kind); err != nil {
			return fmt.Errorf("reading PostgreSQL application object owner: %w", err)
		}
		appOwned := schema == "public" && owner == applicationUsername
		bookkeeping := schema == "azd_initialization" &&
			(name == "receipt" || name == "baseline_file" || name == "baseline_object") &&
			kind == "r" && owner == administratorName
		if !appOwned && !bookkeeping {
			return validationError(
				"database_object_owner_unverified",
				fmt.Sprintf("%s.%s is outside the application-owned public schema policy", schema, name),
				"use a fresh database or explicit migration; existing administrator or foreign objects are not adopted",
			)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	schemas, err := transaction.Query(
		ctx,
		`SELECT nspname, pg_get_userbyid(nspowner),
		        pg_has_role($1, nspowner, 'USAGE'),
		        has_schema_privilege($2, oid, 'USAGE') OR has_schema_privilege($2, oid, 'CREATE')
		 FROM pg_namespace
		 WHERE nspname NOT IN ('pg_catalog', 'information_schema')
		   AND nspname !~ '^pg_toast'
		   AND nspname !~ '^pg_temp_'`,
		administratorName,
		applicationUsername,
	)
	if err != nil {
		return fmt.Errorf("reading PostgreSQL application schema ownership: %w", err)
	}
	defer schemas.Close()
	for schemas.Next() {
		var schema, owner string
		var administratorIsMember, applicationAccess bool
		if err := schemas.Scan(&schema, &owner, &administratorIsMember, &applicationAccess); err != nil {
			return fmt.Errorf("reading PostgreSQL application schema owner: %w", err)
		}
		if schema == "public" && owner != applicationUsername {
			// pg_database_owner is tied to the already verified target database owner.
			if owner == "pg_database_owner" ||
				validateDatabaseOwner("public schema", owner, administratorName, administratorIsMember) == nil {
				continue
			}
		}
		if schema == "azd_initialization" && owner == administratorName && !applicationAccess {
			continue
		}
		return validationError(
			"database_schema_owner_unverified",
			fmt.Sprintf("schema %s has unexpected ownership or application access", schema),
			"use a fresh database or explicitly migrate schemas; only public permits application object creation",
		)
	}
	return schemas.Err()
}

func verifyEffectiveApplicationAccess(
	ctx context.Context,
	transaction pgx.Tx,
	username string,
	databaseName string,
) error {
	checks := []struct {
		query    string
		expected bool
		message  string
		args     []any
	}{
		{
			query:    `SELECT has_database_privilege($1, $2, 'CONNECT')`,
			expected: true,
			message:  "application role lacks database CONNECT",
			args:     []any{username, databaseName},
		},
		{
			query:    `SELECT has_database_privilege($1, $2, 'TEMPORARY')`,
			expected: false,
			message:  "application role retains effective database TEMPORARY",
			args:     []any{username, databaseName},
		},
		{
			query:    `SELECT has_database_privilege($1, $2, 'CREATE')`,
			expected: false,
			message:  "application role retains effective database CREATE",
			args:     []any{username, databaseName},
		},
		{
			query:    `SELECT has_schema_privilege($1, 'public', 'USAGE')`,
			expected: true,
			message:  "application role lacks public schema USAGE",
			args:     []any{username},
		},
		{
			query:    `SELECT has_schema_privilege($1, 'public', 'CREATE')`,
			expected: true,
			message:  "application role lacks public schema CREATE",
			args:     []any{username},
		},
	}
	for _, check := range checks {
		var actual bool
		if err := transaction.QueryRow(ctx, check.query, check.args...).Scan(&actual); err != nil {
			return fmt.Errorf("verifying effective PostgreSQL privileges: %w", err)
		}
		if actual != check.expected {
			return validationError(
				"database_effective_privilege_conflict",
				check.message,
				"remove conflicting PUBLIC or inherited privileges before retrying",
			)
		}
	}
	return nil
}

func verifyEffectiveObjectPrivileges(
	ctx context.Context,
	transaction pgx.Tx,
	applicationUsername string,
) error {
	var inaccessible int
	if err := transaction.QueryRow(
		ctx,
		`SELECT count(*)
		 FROM pg_class object
		 JOIN pg_namespace namespace ON namespace.oid = object.relnamespace
		 WHERE namespace.nspname = 'public'
		   AND ((object.relkind IN ('r', 'p') AND NOT (
		         has_table_privilege($1, object.oid, 'SELECT') AND
		         has_table_privilege($1, object.oid, 'INSERT') AND
		         has_table_privilege($1, object.oid, 'UPDATE') AND
		         has_table_privilege($1, object.oid, 'DELETE')
		       ))
		     OR (object.relkind = 'S' AND NOT has_sequence_privilege($1, object.oid, 'USAGE')))`,
		applicationUsername,
	).Scan(&inaccessible); err != nil {
		return fmt.Errorf("verifying effective PostgreSQL object privileges: %w", err)
	}
	if inaccessible != 0 {
		return validationError(
			"database_effective_object_privilege_conflict",
			"the application role lacks read/write or sequence access to its application objects",
			"restore access to application-owned objects or explicitly migrate the database before retrying",
		)
	}
	return nil
}

func (r *postgresAccessReconciler) connectWithRetry(
	ctx context.Context,
	host string,
	databaseName string,
	username string,
	password string,
) (*pgx.Conn, error) {
	config, err := postgresConnectionConfig(host, databaseName, username, password)
	if err != nil {
		return nil, err
	}
	return r.connectConfigWithRetry(ctx, config)
}

func (r *postgresAccessReconciler) connectConfigWithRetry(
	ctx context.Context,
	config *pgx.ConnConfig,
) (*pgx.Conn, error) {
	var connection *pgx.Conn
	var err error
	for attempt := range 3 {
		connection, err = r.connect(ctx, config)
		if err == nil {
			return connection, nil
		}
		if !isTransientPostgresError(err) || attempt == 2 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return nil, fmt.Errorf("connecting to PostgreSQL with verified TLS: %w", nonDisclosureError(err))
}

func postgresConnectionConfig(host, databaseName, username, password string) (*pgx.ConnConfig, error) {
	connectionString := postgresConnectionString(host, databaseName, username)
	config, err := pgx.ParseConfig(connectionString)
	if err != nil {
		return nil, fmt.Errorf("creating PostgreSQL connection configuration: %w", nonDisclosureError(err))
	}
	config.Password = password
	config.RuntimeParams["search_path"] = "pg_catalog"
	if config.TLSConfig == nil || config.TLSConfig.InsecureSkipVerify ||
		!strings.EqualFold(config.TLSConfig.ServerName, host) {
		return nil, errors.New("PostgreSQL connection did not enable hostname-verified TLS")
	}
	config.TLSConfig.MinVersion = tls.VersionTLS12
	return config, nil
}

func postgresConnectionString(host, databaseName, username string) string {
	return strings.Join([]string{
		"host=" + quoteConnectionValue(host),
		"port=5432",
		"dbname=" + quoteConnectionValue(databaseName),
		"user=" + quoteConnectionValue(username),
		"sslmode=verify-full",
		"connect_timeout=15",
		"application_name=azure.springboot",
	}, " ")
}

func quoteConnectionValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `\'`)
	return "'" + value + "'"
}

func quoteSQLLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func isTransientPostgresError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if postgresErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return strings.HasPrefix(postgresErr.Code, "08") ||
			postgresErr.Code == "57P01" ||
			postgresErr.Code == "57P02" ||
			postgresErr.Code == "57P03"
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "timeout") ||
		strings.Contains(message, "connection refused") ||
		strings.Contains(message, "connection reset") ||
		strings.Contains(message, "temporary")
}
