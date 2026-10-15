// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
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
