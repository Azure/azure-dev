// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnalyzePostgresIdentity(t *testing.T) {
	for _, source := range []string{
		"spring.datasource.url=jdbc:postgresql://localhost/catalog\nspring.datasource.azure.passwordless-enabled=true",
		"spring.datasource.url=jdbc:postgresql://localhost/catalog?" +
			"authenticationPluginClassName=com.azure.identity.extensions.jdbc.postgresql." +
			"AzurePostgresqlAuthenticationPlugin&azure.managedIdentityEnabled=true",
	} {
		directory := t.TempDir()
		writeFixture(t, directory, "pom.xml", "<project/>")
		writeFixture(t, directory, "src/main/resources/application.properties", source)
		application, err := Analyze(t.Context(), directory, staticResolver{model: effectiveModel(
			t, directory, allDrivers()+dependency("com.azure", "azure-identity-extensions", "runtime"))}, envLookup(nil))
		require.NoError(t, err)
		require.Equal(t, "managedIdentity", application.Requirements[0].Authentication)
		require.True(t, application.IdentityExtensions)
		_, err = Generate(t.Context(), application)
		require.NoError(t, err)
	}
}

func TestGeneratePostgresIdentityTLSForNonHikariPool(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "pom.xml", "<project/>")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.datasource.url=jdbc:postgresql://localhost/catalog\n"+
			"spring.datasource.type=org.apache.tomcat.jdbc.pool.DataSource\n"+
			"spring.datasource.azure.passwordless-enabled=true")
	application, err := Analyze(t.Context(), directory, staticResolver{model: effectiveModel(
		t, directory, allDrivers()+dependency("com.azure", "azure-identity-extensions", "runtime"))}, envLookup(nil))
	require.NoError(t, err)

	plan, err := Generate(t.Context(), application)
	require.NoError(t, err)
	binding := string(plan.Files[".azd-spring/application-azd.properties"])
	require.Contains(t, binding, "sslmode=verify-full")
	require.Contains(t, binding, "sslrootcert="+url.QueryEscape("/etc/ssl/certs/ca-certificates.crt"))
	require.NotContains(t, binding, "spring.datasource.hikari.")
}

func TestIdentityDependencyMustBePackaged(t *testing.T) {
	for _, scope := range []string{"provided", "test"} {
		m, _, err := parseModel(effectiveModel(t, t.TempDir(), dependency("com.azure", "azure-identity-extensions", scope)))
		require.NoError(t, err)
		require.False(t, m.hasPackagedIdentityExtensions())
	}
}

func TestRejectsAmbiguousAuthenticationPlugins(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "pom.xml", "<project/>")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.datasource.url=jdbc:postgresql://localhost/catalog?"+
			"authenticationPluginClassName=com.azure.identity.extensions.jdbc.postgresql."+
			"AzurePostgresqlAuthenticationPlugin&authenticationPluginClassName=other.Plugin")
	_, err := Analyze(t.Context(), directory, staticResolver{model: effectiveModel(
		t, directory, allDrivers()+dependency("com.azure", "azure-identity-extensions", "runtime"))}, envLookup(nil))
	require.ErrorContains(t, err, "authentication plugin is not supported")
}
