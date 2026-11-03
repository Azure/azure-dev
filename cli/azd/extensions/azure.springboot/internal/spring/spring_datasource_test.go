// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const testSpringClientID = "11111111-2222-3333-4444-555555555555"

func datasourceProperties(t *testing.T, text string) *springConfiguration {
	t.Helper()
	documents, err := parseSpringProperties(text)
	require.NoError(t, err)
	raw := springProperties{}
	for key, value := range documents[0] {
		raw[key] = springProperty{value, "test"}
	}
	properties, err := normalizeSpringProperties(raw)
	require.NoError(t, err)
	return &springConfiguration{properties: properties}
}

func TestSpringDatasourceDiscoversCurrentDirectHikariPlugin(t *testing.T) {
	config := datasourceProperties(t, `spring.datasource.url=${POSTGRES_URL:jdbc:postgresql://localhost:5432/petclinic}
spring.datasource.username=petclinic_app
spring.datasource.azure.passwordless-enabled=true
spring.datasource.hikari.data-source-properties[authenticationPluginClassName]=`+azurePostgreSQLPlugin+`
spring.datasource.hikari.data-source-properties[azure.managedIdentityEnabled]=true
spring.datasource.hikari.data-source-properties[azure.clientId]=${AZURE_CLIENT_ID:}
spring.datasource.hikari.data-source-properties[sslmode]=verify-full
spring.datasource.hikari.data-source-properties[sslrootcert]=/etc/ssl/certs/ca-certificates.crt
`)
	dependencies := springDependencies{postgresql: true, azureIdentity: true}
	intent, err := inferSpringDatasource(config, dependencies, false)
	require.NoError(t, err)
	require.Equal(t, databasePostgreSQL, intent.engine)
	require.Equal(t, databaseAuthenticationManagedIdentity, intent.authentication)
	require.Equal(t, "petclinic", intent.name)
	require.Equal(t, "petclinic_app", intent.username)
	require.True(t, intent.pending)
	config.environment = map[string]string{
		"POSTGRES_URL":    "jdbc:postgresql://actual.postgres.database.azure.com:5432/petclinic",
		"AZURE_CLIENT_ID": testSpringClientID,
	}
	intent, err = inferSpringDatasource(config, dependencies, true)
	require.NoError(t, err)
	require.False(t, intent.pending)
	require.True(t, intent.clientIDConfigured)
	require.Equal(t, testSpringClientID, intent.clientID)
	require.Equal(t, "actual.postgres.database.azure.com", intent.host)
	require.Equal(t, 5432, intent.port)
	require.Equal(t, "/etc/ssl/certs/ca-certificates.crt", intent.rootCert)
}

func TestSpringDatasourceUnresolvedSelectorsAreNotSyntheticPostgresOrH2(t *testing.T) {
	for _, selector := range []string{
		"spring.datasource.url=${POSTGRES_URL}",
		"spring.datasource.url=jdbc:postgresql://${HOST}/petclinic?sslmode=verify-full",
		"spring.datasource.url=jdbc:postgresql://actual.example/petclinic?sslmode=verify-full\n" +
			"spring.datasource.azure.passwordless-enabled=${AUTH}",
	} {
		config := datasourceProperties(t, selector+"\nspring.datasource.username=app\nspring.datasource.password=local-test")
		intent, err := inferSpringDatasource(config, springDependencies{postgresql: true, h2: true}, false)
		require.NoError(t, err)
		require.True(t, intent.selectionPending)
		require.True(t, intent.pending)
		require.Empty(t, intent.engine)
		require.Empty(t, intent.authentication)
		require.Empty(t, intent.host)
		require.Empty(t, intent.name)
		_, err = inferSpringDatasource(config, springDependencies{postgresql: true, h2: true}, true)
		require.Error(t, err)
		config.environment = map[string]string{
			"POSTGRES_URL": "jdbc:postgresql://actual.example/petclinic?sslmode=verify-full",
			"HOST":         "actual.example", "AUTH": "false",
		}
		intent, err = inferSpringDatasource(config, springDependencies{postgresql: true, h2: true}, true)
		require.NoError(t, err)
		require.False(t, intent.selectionPending)
		require.Equal(t, databasePostgreSQL, intent.engine)
	}
}
func TestSpringDatasourceDriverAndHikariOverridePrecedence(t *testing.T) {
	config := datasourceProperties(t, `spring.datasource.url=jdbc:postgresql://unused.example/unused
spring.datasource.username=source_user
spring.datasource.password=source_password
spring.datasource.hikari.jdbc-url=jdbc:postgresql://actual.example:5432/actual?\
sslmode=verify-full&user=url_user&password=url_password
spring.datasource.hikari.username=hikari_user
spring.datasource.hikari.password=hikari_password
spring.datasource.hikari.data-source-properties[user]=map_user
spring.datasource.hikari.data-source-properties[password]=map_password
spring.datasource.hikari.data-source-properties[PGHOST]=ignored.example
spring.datasource.hikari.data-source-properties[PGPORT]=6432
spring.datasource.hikari.data-source-properties[PGDBNAME]=ignored
`)
	intent, err := inferSpringDatasource(config, springDependencies{postgresql: true}, true)
	require.NoError(t, err)
	require.Equal(t, databaseAuthenticationPassword, intent.authentication)
	require.Equal(t, "actual", intent.name)
	require.Equal(t, "actual.example", intent.host)
	require.Equal(t, 5432, intent.port)
	require.Equal(t, "url_user", intent.username)
	require.Equal(t, "url_password", intent.password)
}

func TestSpringDatasourceStarterUsesEffectiveManagedIdentitySelection(t *testing.T) {
	config := datasourceProperties(t, `spring.datasource.url=jdbc:postgresql://actual.example/petclinic?sslmode=verify-full
spring.datasource.username=app
spring.datasource.azure.passwordless-enabled=true
spring.cloud.azure.credential.managed-identity-enabled=true
spring.cloud.azure.credential.client-id=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee
spring.datasource.azure.credential.client-id=`+testSpringClientID)
	intent, err := inferSpringDatasource(config, springDependencies{
		postgresql: true, azureIdentity: true, azureJDBC: true,
	}, true)
	require.NoError(t, err)
	require.Equal(t, databaseAuthenticationManagedIdentity, intent.authentication)
	require.True(t, intent.clientIDConfigured)
	require.Equal(t, testSpringClientID, intent.clientID)
	config.properties[springPropertyKey("spring.datasource.password")] = springProperty{"actual-password", "test"}
	intent, err = inferSpringDatasource(config, springDependencies{
		postgresql: true, azureIdentity: true, azureJDBC: true,
	}, true)
	require.NoError(t, err)
	require.Equal(t, databaseAuthenticationPassword, intent.authentication)
	require.Equal(t, "actual-password", intent.password)
}

func TestSpringDatasourceManagedIdentitySelectsSystemAssignedWithoutClientID(t *testing.T) {
	for name, text := range map[string]string{
		"direct plugin": `spring.datasource.url=jdbc:postgresql://actual.example/petclinic?sslmode=verify-full
spring.datasource.username=app
spring.datasource.hikari.data-source-properties[authenticationPluginClassName]=` + azurePostgreSQLPlugin + `
spring.datasource.hikari.data-source-properties[azure.managedIdentityEnabled]=true`,
		"Spring Azure starter": `spring.datasource.url=jdbc:postgresql://actual.example/petclinic?sslmode=verify-full
spring.datasource.username=app
spring.datasource.azure.passwordless-enabled=true
spring.datasource.azure.credential.managed-identity-enabled=true`,
	} {
		t.Run(name, func(t *testing.T) {
			dependencies := springDependencies{postgresql: true, azureIdentity: true}
			if name == "Spring Azure starter" {
				dependencies.azureJDBC = true
			}
			intent, err := inferSpringDatasource(datasourceProperties(t, text), dependencies, true)
			require.NoError(t, err)
			require.Equal(t, databaseAuthenticationManagedIdentity, intent.authentication)
			require.False(t, intent.clientIDConfigured)
			require.Empty(t, intent.clientID)
		})
	}
}

func TestSpringDatasourceManagedIdentityRejectsExplicitEmptyClientID(t *testing.T) {
	for _, clientID := range []string{"", "${AZURE_CLIENT_ID:}"} {
		config := datasourceProperties(t, `spring.datasource.url=jdbc:postgresql://actual.example/petclinic?sslmode=verify-full
spring.datasource.username=app
spring.datasource.hikari.data-source-properties[authenticationPluginClassName]=`+azurePostgreSQLPlugin+`
spring.datasource.hikari.data-source-properties[azure.managedIdentityEnabled]=true
spring.datasource.hikari.data-source-properties[azure.clientId]=`+clientID)
		_, err := inferSpringDatasource(config, springDependencies{postgresql: true, azureIdentity: true}, true)
		require.ErrorContains(t, err, "explicitly configured")
	}
}

func TestSpringDatasourceDoesNotDefaultFailuresToH2(t *testing.T) {
	for name, text := range map[string]string{
		"missing":      "",
		"unknown":      "spring.datasource.url=jdbc:mysql://actual.example/petclinic",
		"test only H2": "spring.datasource.url=jdbc:h2:mem:petclinic",
		"no integration": "spring.datasource.url=jdbc:postgresql://actual.example/petclinic?sslmode=verify-full\n" +
			"spring.datasource.username=app\nspring.datasource.azure.passwordless-enabled=true",
		"multiple hosts": "spring.datasource.url=jdbc:postgresql://one.example,two.example/petclinic?sslmode=verify-full",
		"no password": "spring.datasource.url=jdbc:postgresql://actual.example/petclinic?sslmode=verify-full\n" +
			"spring.datasource.username=app",
		"insecure TLS": "spring.datasource.url=jdbc:postgresql://actual.example/petclinic?sslmode=require\n" +
			"spring.datasource.username=app\nspring.datasource.password=private-password",
		"URL userinfo": "spring.datasource.url=jdbc:postgresql://private-user:private-password@actual.example/petclinic",
		"repeated":     "spring.datasource.url=jdbc:postgresql://actual.example/petclinic?user=private-user&user=other",
		"external service": "spring.datasource.url=jdbc:postgresql://actual.example/petclinic" +
			"?service=private-service&sslmode=verify-full",
		"custom datasource": "spring.datasource.url=jdbc:postgresql://actual.example/petclinic\n" +
			"spring.datasource.hikari.data-source-class-name=custom.DataSource",
	} {
		t.Run(name, func(t *testing.T) {
			intent, err := inferSpringDatasource(datasourceProperties(t, text), springDependencies{postgresql: true}, true)
			require.Error(t, err)
			require.Nil(t, intent)
			for _, secret := range []string{"private-password", "private-user", "private-service"} {
				require.NotContains(t, err.Error(), secret)
			}
		})
	}
	intent, err := inferSpringDatasource(datasourceProperties(t, "spring.datasource.url=jdbc:h2:mem:petclinic"),
		springDependencies{h2: true}, true)
	require.NoError(t, err)
	require.Equal(t, databaseH2, intent.engine)
}
