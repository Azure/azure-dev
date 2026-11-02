// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

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
	require.Equal(t, "public,pg_catalog", config.RuntimeParams["search_path"])
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
	now := time.Unix(1_700_000_000, 0)
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
	firstRule := bootstrapFirewallRuleName(prefix, now, "1111111111111111")
	secondRule := bootstrapFirewallRuleName(prefix, now, "2222222222222222")
	require.NotEqual(t, firstRule, secondRule)
	require.True(t, len(firstRule) <= 128)
	database := resolvedDatabase{
		Type:             databasePostgreSQL,
		ServerResourceID: server,
		Name:             "petclinic",
		Authentication:   databaseAuthenticationPassword,
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

func TestTemporaryFirewallAlwaysCleansUpAmbiguousCreate(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	cancel()
	cleanupCalled := false
	err := withTemporaryFirewall(
		parent,
		func(context.Context) error {
			return errors.New("create request outcome unknown")
		},
		func(ctx context.Context) error {
			cleanupCalled = true
			require.NoError(t, ctx.Err(), "cleanup must not inherit parent cancellation")
			return errors.New("cleanup failed")
		},
		func(context.Context) error {
			t.Fatal("SQL must not run when firewall creation fails")
			return nil
		},
	)
	require.True(t, cleanupCalled)
	require.ErrorContains(t, err, "create request outcome unknown")
	require.ErrorContains(t, err, "cleanup failed")
}

func TestStaleFirewallRecoveryPreservesRecentAndUnrelatedRules(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	prefix := "azd-springboot-bootstrap-owned-"
	stale := bootstrapFirewallRuleName(
		prefix,
		now.Add(-postgresFirewallStaleAfter-time.Second),
		"1111111111111111",
	)
	recent := bootstrapFirewallRuleName(prefix, now.Add(-time.Minute), "2222222222222222")
	unrelated := "customer-firewall-rule"
	malformed := prefix + "100-not-owned-format-extra"

	require.Equal(
		t,
		[]string{stale},
		staleBootstrapFirewallRules(prefix, now, []string{recent, unrelated, malformed, stale}),
	)
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
