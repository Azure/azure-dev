// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func resolutionFixture(t *testing.T, name string) (string, ModelResolver) {
	t.Helper()
	directory := t.TempDir()
	source := os.DirFS(filepath.Join("testdata", "resolution", name))
	require.NoError(t, fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(source, name)
		if err != nil {
			return err
		}
		writeFixture(t, directory, name, string(data))
		return nil
	}))
	writeFixture(t, directory, "pom.xml", "<project/>")
	return directory, staticResolver{model: effectiveModel(t, directory, allDrivers())}
}

func TestResolutionFixtures(t *testing.T) {
	for _, tt := range []struct {
		fixture    string
		profiles   []string
		kind       ResourceKind
		decision   DecisionKind
		diagnostic string
	}{
		{fixture: "h2-mysql", kind: "h2"},
		{fixture: "h2-postgres", kind: "h2"},
		{fixture: "three-engines", kind: "h2"},
		{fixture: "three-engines", profiles: []string{"mysql"}, kind: MySQL},
		{fixture: "three-engines", profiles: []string{"postgres"}, kind: PostgreSQL},
		{fixture: "default-postgres", kind: PostgreSQL},
		{fixture: "profile-choice", decision: Ambiguous},
		{fixture: "profile-choice", profiles: []string{"postgres"}, kind: PostgreSQL},
		{fixture: "default-profile-choice", decision: Ambiguous},
		{fixture: "default-profile-choice", profiles: []string{"h2"}, kind: "h2"},
		{fixture: "missing-url", decision: MissingInput},
		{fixture: "missing-port", decision: MissingInput},
		{fixture: "driver-conflict", diagnostic: "invalid"},
		{fixture: "file-storage", diagnostic: "unsupported"},
	} {
		t.Run(tt.fixture+strings.Join(tt.profiles, ","), func(t *testing.T) {
			directory, resolver := resolutionFixture(t, tt.fixture)
			result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{Profiles: tt.profiles})
			require.NoError(t, err)
			if tt.decision != "" {
				require.Empty(t, result.Diagnostics)
				require.Len(t, result.Decisions, 1)
				decision := result.Decisions[0]
				require.Equal(t, tt.decision, decision.Kind)
				require.Equal(t, []string{"app"}, decision.ApplicationIDs)
				require.NotEmpty(t, decision.Evidence)
				if tt.fixture == "profile-choice" || tt.fixture == "default-profile-choice" {
					if tt.fixture == "profile-choice" {
						require.Equal(t, []string{"h2", "mysql", "postgres"}, decision.Choices)
					} else {
						require.Equal(t, []string{"h2", "postgres"}, decision.Choices)
					}
					require.True(t, decision.Profiles)
					require.Empty(t, decision.BindingIDs)
				} else if tt.fixture == "missing-url" {
					require.Empty(t, decision.Choices)
					require.Equal(t, []string{"app/datasource:primary"}, decision.BindingIDs)
					require.Len(t, result.Bindings, 1)
					require.Equal(t, result.Bindings[0].ID, decision.BindingIDs[0])
					require.Empty(t, result.Resources, "missing URLs must not guess resource kinds from drivers")
				} else {
					require.Empty(t, decision.BindingIDs)
				}
				_, err = result.ResolvedApplication()
				require.ErrorContains(t, err, "unresolved")
				return
			}
			require.Empty(t, result.Decisions)
			if tt.diagnostic != "" {
				require.Len(t, result.Diagnostics, 1)
				require.Equal(t, tt.diagnostic, result.Diagnostics[0].Kind)
				_, err = result.ResolvedApplication()
				require.ErrorContains(t, err, "blocking")
				return
			}
			require.Empty(t, result.Diagnostics)
			require.Len(t, result.Resources, 1)
			require.Len(t, result.Bindings, 1)
			require.Equal(t, tt.kind, result.Resources[0].Kind)
			require.Equal(t, result.Resources[0].ID, result.Bindings[0].ResourceID)
			require.NotEmpty(t, result.Bindings[0].Evidence)
			application, err := result.ResolvedApplication()
			require.NoError(t, err)
			plan, err := Generate(t.Context(), application)
			require.NoError(t, err)
			properties := string(plan.Files[".azd-spring/application-azd.properties"])
			if tt.kind == "h2" {
				require.Equal(t, ApplicationLocal, result.Resources[0].Disposition)
				require.Contains(t, properties, "spring.datasource.url=jdbc:h2:mem:catalog\n")
				require.Empty(t, application.Requirements)
			} else {
				require.Equal(t, Provisioned, result.Resources[0].Disposition)
				require.Len(t, application.Requirements, 1)
			}
		})
	}
}

func TestResolutionIgnoresAmbientConfiguration(t *testing.T) {
	t.Setenv("SPRING_PROFILES_ACTIVE", "postgres")
	t.Setenv("SPRING_DATASOURCE_URL", "jdbc:postgresql://ignored/not_selected")
	t.Setenv("SPRING_APPLICATION_JSON", `{"server":{"port":1234}}`)
	t.Setenv("DEPLOYMENT_JDBC_URL", "jdbc:mysql://ignored/not_selected")
	directory, resolver := resolutionFixture(t, "three-engines")
	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	require.Equal(t, ResourceKind("h2"), result.Resources[0].Kind)
	require.Equal(t, 8080, result.Applications[0].Port)
	directory, resolver = resolutionFixture(t, "missing-url")
	result, err = AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	require.Len(t, result.Decisions, 1)
}

func TestResolutionAnswersAndProvenance(t *testing.T) {
	directory, resolver := resolutionFixture(t, "missing-url")
	address := url.URL{
		Scheme: "postgresql", Host: "localhost", Path: "/catalog",
		User: url.UserPassword("test-user", "test-password"),
	}
	inputs := DeploymentInputs{Properties: map[string]string{
		"DEPLOYMENT_JDBC_URL": "jdbc:" + address.String(),
	}}
	result, err := AnalyzeProject(t.Context(), directory, resolver, inputs)
	require.NoError(t, err)
	require.Empty(t, result.Decisions)
	require.Equal(t, PostgreSQL, result.Resources[0].Kind)
	require.Equal(t, []Evidence{
		{Property: "spring.datasource.url", Source: "src/main/resources/application.properties"},
		{Property: "DEPLOYMENT_JDBC_URL", Source: "deployment input"},
	}, result.Bindings[0].Evidence)
	serialized, err := json.Marshal(result)
	require.NoError(t, err)
	for _, sensitive := range []string{"localhost", "test-user", "test-password"} {
		require.NotContains(t, string(serialized), sensitive)
	}

	directory, resolver = resolutionFixture(t, "missing-port")
	result, err = AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
		Properties: map[string]string{"HTTP_PORT": "9090"},
	})
	require.NoError(t, err)
	require.Equal(t, 9090, result.Applications[0].Port)
}

func TestResolutionRejectsUnusedOrUnreproducibleInputs(t *testing.T) {
	for _, properties := range []map[string]string{
		{"unused": "value"},
		{"spring.datasource.password": "do-not-copy"},
		{"spring.datasource.driver-class-name": "org.h2.Driver"},
		{"spring.profiles.active": "postgres"},
		{"server.port": "8080", "SERVER_PORT": "9090"},
	} {
		directory, resolver := resolutionFixture(t, "three-engines")
		_, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{Properties: properties})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "do-not-copy")
	}
}

func TestResolutionRejectsInvalidExplicitProfiles(t *testing.T) {
	for _, profiles := range [][]string{{}, {""}, {"postgres", ""}, {"not a profile"}} {
		directory, resolver := resolutionFixture(t, "three-engines")
		_, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{Profiles: profiles})
		require.ErrorContains(t, err, "--profiles")
	}
}

func TestResolutionPreservesOperationalErrors(t *testing.T) {
	directory, _ := resolutionFixture(t, "three-engines")
	failure := errors.New("build unavailable")
	result, err := AnalyzeProject(t.Context(), directory, staticResolver{err: failure}, DeploymentInputs{})
	require.ErrorIs(t, err, failure)
	require.Nil(t, result)
}

func TestResolutionPlaceholderChain(t *testing.T) {
	directory, resolver := resolutionFixture(t, "missing-url")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.datasource.url=${DATABASE_URL}\nDATABASE_URL=${CONNECTION}")
	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	require.Len(t, result.Decisions, 1)
	require.Equal(t, "CONNECTION", result.Decisions[0].Input)
	require.Equal(t, []Evidence{
		{Property: "spring.datasource.url", Source: "src/main/resources/application.properties"},
		{Property: "DATABASE_URL", Source: "src/main/resources/application.properties"},
	}, result.Decisions[0].Evidence)
}

func TestResolutionValidatesReferences(t *testing.T) {
	directory, resolver := resolutionFixture(t, "three-engines")
	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	result.Bindings[0].ResourceID = "missing"
	_, err = result.ResolvedApplication()
	require.Error(t, err)
}

func TestEmbeddedRuntimeBindings(t *testing.T) {
	for _, tt := range []struct {
		jdbc      string
		errorText string
	}{
		{"jdbc:h2:mem:catalog;DB_CLOSE_DELAY=-1;DB_CLOSE_ON_EXIT=FALSE", ""},
		{"jdbc:derby:memory:catalog;create=true", ""},
		{"jdbc:hsqldb:mem:catalog", ""},
		{"jdbc:h2:mem:catalog;PASSWORD=do-not-copy", "unsupported"},
		{"jdbc:h2:mem:catalog;INIT=do-not-copy", "unsupported"},
		{"jdbc:h2:mem:catalog;DB_CLOSE_DELAY=-1;DB_CLOSE_DELAY=0", "duplicate"},
		{"jdbc:h2:mem:catalog\ninjected=value", "unsupported"},
	} {
		t.Run(tt.jdbc, func(t *testing.T) {
			directory, resolver := resolutionFixture(t, "three-engines")
			resolver = staticResolver{model: effectiveModel(t, directory, allDrivers()+
				dependency("org.apache.derby", "derby", "runtime")+dependency("org.hsqldb", "hsqldb", "runtime"))}
			result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
				Properties: map[string]string{"spring.datasource.url": tt.jdbc},
			})
			require.NoError(t, err)
			if tt.errorText != "" {
				require.Len(t, result.Diagnostics, 1)
				require.Contains(t, result.Diagnostics[0].Message, tt.errorText)
				require.NotContains(t, result.Diagnostics[0].Message, "do-not-copy")
				return
			}
			application, err := result.ResolvedApplication()
			require.NoError(t, err)
			plan, err := Generate(t.Context(), application)
			require.NoError(t, err)
			require.Contains(t, string(plan.Files[".azd-spring/application-azd.properties"]),
				"spring.datasource.url="+tt.jdbc+"\n")
			require.NotContains(t, plan.Files, ".azd-spring/database.ps1")
		})
	}
}

func TestResolutionRejectsUnreproducibleCredentialPlaceholder(t *testing.T) {
	directory, resolver := resolutionFixture(t, "three-engines")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.datasource.url=jdbc:h2:mem:catalog\nspring.datasource.password=${LOCAL_PASSWORD}")
	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	require.Empty(t, result.Decisions, "do not request a secret that generation cannot safely reproduce")
	require.Len(t, result.Diagnostics, 1)
	require.Equal(t, "unsupported", result.Diagnostics[0].Kind)
}

func TestResolutionMaterializesExplicitEmptyDatasource(t *testing.T) {
	directory, resolver := resolutionFixture(t, "missing-url")
	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
		Properties: map[string]string{"DEPLOYMENT_JDBC_URL": ""},
	})
	require.NoError(t, err)
	application, err := result.ResolvedApplication()
	require.NoError(t, err)
	plan, err := Generate(t.Context(), application)
	require.NoError(t, err)
	require.Contains(t, string(plan.Files[".azd-spring/application-azd.properties"]), "spring.datasource.url=\n")
}

func TestProfileChoicePreservesGroupNames(t *testing.T) {
	directory, resolver := resolutionFixture(t, "profile-choice")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.profiles.active=${DEPLOYMENT_PROFILE}\nspring.profiles.group.production-sql=postgres")
	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	require.Contains(t, result.Decisions[0].Choices, "production-sql")
	result, err = AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{Profiles: []string{"production-sql"}})
	require.NoError(t, err)
	require.Equal(t, PostgreSQL, result.Resources[0].Kind)
}
