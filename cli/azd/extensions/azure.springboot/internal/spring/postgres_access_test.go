// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

type postgresTestRow func(...any) error

func (r postgresTestRow) Scan(destinations ...any) error {
	return r(destinations...)
}

type recordingPostgresRowQuerier struct {
	query string
	args  []any
	row   pgx.Row
}

func (q *recordingPostgresRowQuerier) QueryRow(
	_ context.Context,
	query string,
	args ...any,
) pgx.Row {
	q.query = query
	q.args = args
	return q.row
}

func TestVerifyEntraRoleMappingUsesActualFunctionColumn(t *testing.T) {
	objectID := "11111111-2222-3333-4444-555555555555"
	querier := &recordingPostgresRowQuerier{
		row: postgresTestRow(func(destinations ...any) error {
			*destinations[0].(*string) = objectID
			*destinations[1].(*string) = "service"
			*destinations[2].(*int32) = 0
			return nil
		}),
	}

	err := verifyEntraRoleMapping(t.Context(), querier, "petclinic_app", objectID)

	require.NoError(t, err)
	require.Contains(t, querier.query, "WHERE rolname = $1")
	require.NotContains(t, querier.query, "rolename")
	require.Equal(t, []any{"petclinic_app"}, querier.args)
}

func TestSelectedApplicationIdentitySupportsSystemAndUserAssigned(t *testing.T) {
	appID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/containerApps/app"
	systemPrincipalID := "11111111-2222-3333-4444-555555555555"
	identityID := "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.ManagedIdentity/userAssignedIdentities/app"
	userPrincipalID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	clientID := "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	app := &armappcontainers.ContainerApp{
		ID: new(appID),
		Identity: &armappcontainers.ManagedServiceIdentity{
			Type:        new(armappcontainers.ManagedServiceIdentityTypeSystemAssignedUserAssigned),
			PrincipalID: new(systemPrincipalID),
			UserAssignedIdentities: map[string]*armappcontainers.UserAssignedIdentity{
				identityID: {PrincipalID: new(userPrincipalID), ClientID: new(clientID)},
			},
		},
	}

	principalID, selectedClientID, err := selectedApplicationIdentity(resolvedDatabase{
		AppIdentity: &databaseAppIdentity{
			Type:       databaseAppIdentityTypeSystemAssigned,
			ResourceID: appID,
		},
	}, map[string]string{serviceIdentityPrincipalIDEnv: systemPrincipalID}, app)
	require.NoError(t, err)
	require.Equal(t, systemPrincipalID, principalID)
	require.Empty(t, selectedClientID)

	principalID, selectedClientID, err = selectedApplicationIdentity(resolvedDatabase{
		AppIdentity: &databaseAppIdentity{
			Type:       databaseAppIdentityTypeUserAssigned,
			ResourceID: identityID,
		},
	}, map[string]string{
		serviceIdentityPrincipalIDEnv: userPrincipalID,
		serviceIdentityClientIDEnv:    clientID,
	}, app)
	require.NoError(t, err)
	require.Equal(t, userPrincipalID, principalID)
	require.Equal(t, clientID, selectedClientID)

	app.Identity.PrincipalID = new("different")
	_, _, err = selectedApplicationIdentity(resolvedDatabase{
		AppIdentity: &databaseAppIdentity{
			Type:       databaseAppIdentityTypeSystemAssigned,
			ResourceID: appID,
		},
	}, map[string]string{serviceIdentityPrincipalIDEnv: systemPrincipalID}, app)
	require.ErrorContains(t, err, "does not match")
}

func TestPostgresConnectionConfigUsesVerifiedTLSWithoutCredentialURL(t *testing.T) {
	config, err := postgresConnectionConfig(
		"server.postgres.database.azure.com",
		"petclinic",
		"developer@example.com",
		"sensitive-token",
	)
	require.NoError(t, err)
	require.Equal(t, "sensitive-token", config.Password)
	require.NotNil(t, config.TLSConfig)
	require.False(t, config.TLSConfig.InsecureSkipVerify)
	require.Equal(t, "server.postgres.database.azure.com", config.TLSConfig.ServerName)
	require.Equal(t, "pg_catalog", config.RuntimeParams["search_path"])
	connectionString := postgresConnectionString(
		"server.postgres.database.azure.com",
		"petclinic",
		"developer@example.com",
	)
	require.NotContains(t, connectionString, "sensitive-token")
	require.NotContains(t, connectionString, "://")
}

func TestPostgresOwnedNamesAreStableAndScoped(t *testing.T) {
	server := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg"
	prefix := bootstrapFirewallRulePrefix("env", server)
	require.Equal(
		t,
		prefix,
		bootstrapFirewallRulePrefix("env", server),
	)
	require.NotEqual(
		t,
		prefix,
		bootstrapFirewallRulePrefix("other", server),
	)
	state := postgresLocalBootstrapState{
		ServerID:    server,
		Environment: "env",
		Suffix:      "1111111111111111",
	}
	require.True(t, len(state.firewallName()) <= 128)
	database := resolvedDatabase{
		Type:             databasePostgreSQL,
		ServerResourceID: server,
		Name:             "petclinic",
		Authentication:   databaseAuthenticationManagedIdentity,
		Username:         "petclinic_app",
	}
	firstMarker := databaseRoleMarker("env", database)
	require.Equal(t, firstMarker, databaseRoleMarker("env", database))
	database.Username = "other"
	require.NotEqual(t, firstMarker, databaseRoleMarker("env", database))
	database.Username = "petclinic_APP"
	require.NotEqual(t, firstMarker, databaseRoleMarker("env", database))
}

func TestPostgresRetryClassification(t *testing.T) {
	require.True(t, isTransientPostgresError(errors.New("connection refused")))
	require.True(t, isTransientPostgresError(&pgconn.PgError{Code: "08006"}))
	require.False(t, isTransientPostgresError(&pgconn.PgError{Code: "28P01"}))
	require.False(t, isTransientPostgresError(context.Canceled))
}

func TestVerifiedPostgresResourcesSessionAdministrator(t *testing.T) {
	resources := &verifiedPostgresResources{AdministratorName: "developer"}
	require.Equal(t, "developer", resources.sessionAdministratorName())

	resources.SessionAdministratorName = "azd-local-0123456789abcdef"
	require.Equal(t, "azd-local-0123456789abcdef", resources.sessionAdministratorName())
	require.Equal(t, "developer", resources.AdministratorName)
}

func TestValidateDatabaseOwnerAllowsOnlyVerifiedAzureAdminRoles(t *testing.T) {
	require.NoError(t, validateDatabaseOwner("petclinic", "Developer", "Developer", false))
	require.NoError(t, validateDatabaseOwner("petclinic", "azure_pg_admin", "Developer", true))
	require.NoError(t, validateDatabaseOwner("petclinic", "azure_ad_admin", "Developer", true))

	err := validateDatabaseOwner("petclinic", "azure_pg_admin", "Developer", false)
	require.ErrorContains(t, err, "unexpected or inaccessible")
	err = validateDatabaseOwner("petclinic", "OtherOwner", "Developer", true)
	require.ErrorContains(t, err, "unexpected or inaccessible")
	err = validateDatabaseOwner("petclinic", "developer", "Developer", false)
	require.ErrorContains(t, err, "unexpected or inaccessible")
}
