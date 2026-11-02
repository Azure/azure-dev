// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestPostgresPasswordAccessUsesApplicationOwnedPolicyIntegration(t *testing.T) {
	if os.Getenv("AZD_TEST_SPRING_POSTGRES_LOCAL") != "1" {
		t.Skip("set AZD_TEST_SPRING_POSTGRES_LOCAL=1 and AZD_TEST_POSTGRES_URL to run")
	}
	base, err := pgx.ParseConfig(os.Getenv("AZD_TEST_POSTGRES_URL"))
	require.NoError(t, err)
	require.NotEmpty(t, base.User)

	suffixBytes := make([]byte, 6)
	_, err = rand.Read(suffixBytes)
	require.NoError(t, err)
	suffix := hex.EncodeToString(suffixBytes)
	databaseName := "azd_spring_" + suffix
	applicationUsername := "azd_app_" + suffix
	applicationPassword := "App-" + suffix + "-Pass9!"

	admin, err := pgx.ConnectConfig(t.Context(), base.Copy())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = admin.Close(context.Background())
	})
	_, err = admin.Exec(
		t.Context(),
		"CREATE DATABASE "+pgx.Identifier{databaseName}.Sanitize()+
			" OWNER "+pgx.Identifier{base.User}.Sanitize(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(
			cleanupContext,
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()",
			databaseName,
		)
		_, _ = admin.Exec(
			cleanupContext,
			"DROP DATABASE IF EXISTS "+pgx.Identifier{databaseName}.Sanitize(),
		)
		_, _ = admin.Exec(
			cleanupContext,
			"DROP ROLE IF EXISTS "+pgx.Identifier{applicationUsername}.Sanitize(),
		)
	})

	reconciler := &postgresAccessReconciler{
		connect: func(ctx context.Context, requested *pgx.ConnConfig) (*pgx.Conn, error) {
			config := base.Copy()
			config.Database = requested.Database
			return pgx.ConnectConfig(ctx, config)
		},
	}
	database := resolvedDatabase{
		Type: databasePostgreSQL,
		ServerResourceID: "/subscriptions/local/resourceGroups/local/providers/" +
			"Microsoft.DBforPostgreSQL/flexibleServers/local",
		Name:           databaseName,
		Authentication: databaseAuthenticationPassword,
		Username:       applicationUsername,
	}
	resources := &verifiedPostgresResources{
		Host:              "local.test",
		AdministratorName: base.User,
	}
	marker := databaseRoleMarker("integration", database)
	require.NoError(t, reconciler.verifyDatabaseScope(
		t.Context(),
		resources,
		database,
		base.Password,
		marker,
	))
	require.NoError(t, reconciler.reconcileRole(
		t.Context(),
		resources,
		database,
		base.Password,
		applicationPassword,
		marker,
	))
	require.NoError(t, reconciler.reconcileDatabasePrivileges(
		t.Context(),
		resources,
		database,
		base.Password,
		marker,
	))
	var casingVariantExists bool
	require.NoError(t, admin.QueryRow(
		t.Context(),
		"SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)",
		strings.ToUpper(applicationUsername),
	).Scan(&casingVariantExists))
	require.False(t, casingVariantExists, "quoted PostgreSQL role names must retain exact casing")

	targetConfig := base.Copy()
	targetConfig.Database = databaseName
	targetAdmin, err := pgx.ConnectConfig(t.Context(), targetConfig)
	require.NoError(t, err)
	_, err = targetAdmin.Exec(t.Context(), `
		CREATE SCHEMA azd_initialization AUTHORIZATION CURRENT_USER;
		CREATE TABLE azd_initialization.receipt (id integer PRIMARY KEY);
	`)
	require.NoError(t, err)
	require.NoError(t, targetAdmin.Close(t.Context()))

	appConfig := base.Copy()
	appConfig.Database = databaseName
	appConfig.User = applicationUsername
	appConfig.Password = applicationPassword
	app, err := pgx.ConnectConfig(t.Context(), appConfig)
	require.NoError(t, err)
	_, err = app.Exec(t.Context(), `
		CREATE TABLE public.items (
		  id bigserial PRIMARY KEY,
		  name text NOT NULL
		);
	`)
	require.NoError(t, err, "application startup must create its own tables and sequences")
	var connect, temporary, createDatabase, schemaUsage, schemaCreate bool
	require.NoError(t, app.QueryRow(t.Context(), `
		SELECT has_database_privilege(current_user, current_database(), 'CONNECT'),
		       has_database_privilege(current_user, current_database(), 'TEMPORARY'),
		       has_database_privilege(current_user, current_database(), 'CREATE'),
		       has_schema_privilege(current_user, 'public', 'USAGE'),
		       has_schema_privilege(current_user, 'public', 'CREATE')
	`).Scan(&connect, &temporary, &createDatabase, &schemaUsage, &schemaCreate))
	require.True(t, connect)
	require.False(t, temporary)
	require.False(t, createDatabase)
	require.True(t, schemaUsage)
	require.True(t, schemaCreate)
	var itemID int64
	require.NoError(t, app.QueryRow(
		t.Context(),
		"INSERT INTO public.items(name) VALUES ($1) RETURNING id",
		"first",
	).Scan(&itemID))
	require.NotZero(t, itemID)
	_, err = app.Exec(t.Context(), "UPDATE public.items SET name = $1 WHERE id = $2", "updated", itemID)
	require.NoError(t, err)
	var name string
	require.NoError(t, app.QueryRow(
		t.Context(),
		"SELECT name FROM public.items WHERE id = $1",
		itemID,
	).Scan(&name))
	require.Equal(t, "updated", name)
	_, err = app.Exec(t.Context(), "DELETE FROM public.items WHERE id = $1", itemID)
	require.NoError(t, err)

	requireInsufficientPrivilege(t, app, "CREATE TEMP TABLE forbidden_temp(id integer)")
	requireInsufficientPrivilege(t, app, "CREATE SCHEMA forbidden_schema")
	requireInsufficientPrivilege(t, app, "SELECT * FROM azd_initialization.receipt")
	require.NoError(t, app.Close(t.Context()))

	targetAdmin, err = pgx.ConnectConfig(t.Context(), targetConfig)
	require.NoError(t, err)
	var owner string
	require.NoError(t, targetAdmin.QueryRow(
		t.Context(),
		`SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'public.items'::regclass`,
	).Scan(&owner))
	require.Equal(t, applicationUsername, owner)
	require.NoError(t, targetAdmin.QueryRow(
		t.Context(),
		`SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'public.items_id_seq'::regclass`,
	).Scan(&owner))
	require.Equal(t, applicationUsername, owner)
	require.NoError(t, targetAdmin.QueryRow(
		t.Context(),
		`SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = current_database()`,
	).Scan(&owner))
	require.Equal(t, base.User, owner, "the application must not own the database")
	require.NoError(t, targetAdmin.Close(t.Context()))

	require.NoError(t, reconciler.verifyDatabaseScope(
		t.Context(),
		resources,
		database,
		base.Password,
		marker,
	))
	require.NoError(t, reconciler.reconcileRole(
		t.Context(),
		resources,
		database,
		base.Password,
		"Different-Pass8!",
		marker,
	))
	require.NoError(t, reconciler.reconcileDatabasePrivileges(
		t.Context(),
		resources,
		database,
		base.Password,
		marker,
	))
	app, err = pgx.ConnectConfig(t.Context(), appConfig)
	require.NoError(t, err, "rerun must not rotate the application password")
	require.NoError(t, app.QueryRow(
		t.Context(),
		"INSERT INTO public.items(name) VALUES ($1) RETURNING id",
		"after-rerun",
	).Scan(&itemID), "rerun must preserve application table and sequence access")
	_, err = app.Exec(t.Context(), `
		ALTER TABLE public.items ADD COLUMN description text;
		TRUNCATE TABLE public.items;
		CREATE TABLE public.startup_probe(id integer);
		DROP TABLE public.startup_probe;
	`)
	require.NoError(t, err, "object ownership permits ALTER/DROP as well as read/write/create")
	requireInsufficientPrivilege(t, app, "CREATE TEMP TABLE forbidden_temp(id integer)")
	requireInsufficientPrivilege(t, app, "CREATE SCHEMA forbidden_schema")
	requireInsufficientPrivilege(t, app, "SELECT * FROM azd_initialization.receipt")
	require.NoError(t, app.Close(t.Context()))

	targetAdmin, err = pgx.ConnectConfig(t.Context(), targetConfig)
	require.NoError(t, err)
	_, err = targetAdmin.Exec(t.Context(), "CREATE TABLE public.administrator_items(id integer PRIMARY KEY)")
	require.NoError(t, err)
	err = reconciler.reconcileDatabasePrivileges(
		t.Context(),
		resources,
		database,
		base.Password,
		marker,
	)
	require.ErrorContains(t, err, "outside the application-owned public schema policy")
	require.NoError(t, targetAdmin.QueryRow(
		t.Context(),
		`SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'public.administrator_items'::regclass`,
	).Scan(&owner))
	require.Equal(t, base.User, owner, "administrator objects must not be automatically adopted or transferred")
	require.NoError(t, targetAdmin.QueryRow(
		t.Context(),
		`SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'public.items'::regclass`,
	).Scan(&owner))
	require.Equal(t, applicationUsername, owner)
	require.NoError(t, targetAdmin.Close(t.Context()))

	blockedDatabaseName := databaseName + "_existing"
	blockedUsername := applicationUsername + "_existing"
	_, err = admin.Exec(
		t.Context(),
		"CREATE DATABASE "+pgx.Identifier{blockedDatabaseName}.Sanitize()+
			" OWNER "+pgx.Identifier{base.User}.Sanitize(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(
			cleanupContext,
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()",
			blockedDatabaseName,
		)
		_, _ = admin.Exec(
			cleanupContext,
			"DROP DATABASE IF EXISTS "+pgx.Identifier{blockedDatabaseName}.Sanitize(),
		)
		_, _ = admin.Exec(
			cleanupContext,
			"DROP ROLE IF EXISTS "+pgx.Identifier{blockedUsername}.Sanitize(),
		)
	})
	blockedConfig := base.Copy()
	blockedConfig.Database = blockedDatabaseName
	blockedAdmin, err := pgx.ConnectConfig(t.Context(), blockedConfig)
	require.NoError(t, err)
	_, err = blockedAdmin.Exec(t.Context(), `
		CREATE SCHEMA existing_scope;
		CREATE TABLE existing_scope.preexisting(id integer PRIMARY KEY);
	`)
	require.NoError(t, err)
	require.NoError(t, blockedAdmin.Close(t.Context()))

	blockedDatabase := database
	blockedDatabase.Name = blockedDatabaseName
	blockedDatabase.Username = blockedUsername
	blockedMarker := databaseRoleMarker("integration", blockedDatabase)
	err = reconciler.verifyDatabaseScope(
		t.Context(),
		resources,
		blockedDatabase,
		base.Password,
		blockedMarker,
	)
	require.ErrorContains(t, err, "contains unowned application objects")
	var blockedRoleExists bool
	require.NoError(t, admin.QueryRow(
		t.Context(),
		"SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)",
		blockedUsername,
	).Scan(&blockedRoleExists))
	require.False(t, blockedRoleExists, "scope validation must run before role creation")
}

func requireInsufficientPrivilege(t *testing.T, connection *pgx.Conn, statement string) {
	t.Helper()
	_, err := connection.Exec(t.Context(), statement)
	require.Error(t, err)
	postgresError, ok := errors.AsType[*pgconn.PgError](err)
	require.True(t, ok)
	require.Equal(t, "42501", postgresError.Code)
}
