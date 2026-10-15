// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"errors"
	"fmt"
	"io/fs"
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
				if tt.fixture == "profile-choice" {
					require.Equal(t, []string{"h2", "mysql", "postgres"}, decision.Choices)
					require.True(t, decision.Profiles)
					require.Empty(t, decision.BindingIDs)
				} else if tt.fixture == "missing-url" {
					require.Empty(t, decision.Choices)
					require.Equal(t, []string{"app/datasource:primary"}, decision.BindingIDs)
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
	inputs := DeploymentInputs{Properties: map[string]string{
		"DEPLOYMENT_JDBC_URL": "jdbc:postgresql://localhost/catalog",
	}}
	result, err := AnalyzeProject(t.Context(), directory, resolver, inputs)
	require.NoError(t, err)
	require.Empty(t, result.Decisions)
	require.Equal(t, PostgreSQL, result.Resources[0].Kind)
	require.Equal(t, []Evidence{
		{Property: "spring.datasource.url", Source: "src/main/resources/application.properties"},
		{Property: "DEPLOYMENT_JDBC_URL", Source: "deployment input"},
	}, result.Bindings[0].Evidence)
	require.NotContains(t, fmt.Sprint(result), "localhost")

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
