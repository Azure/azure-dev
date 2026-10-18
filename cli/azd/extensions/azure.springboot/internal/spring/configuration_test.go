// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFlattenConfigurationLists(t *testing.T) {
	for _, tt := range []struct {
		name   string
		values []any
		want   string
		reject bool
	}{
		{name: "strings", values: []any{"blue", "green"}, want: "blue,green"},
		{name: "numbers", values: []any{1, int64(2), 3.5}, want: "1,2,3.5"},
		{name: "booleans", values: []any{true, false}, want: "true,false"},
		{name: "mixed scalars", values: []any{"one", 2, true, 0.25}, want: "one,2,true,0.25"},
		{name: "empty", values: []any{}, want: ""},
		{name: "object element", values: []any{map[string]any{"key": "value"}}, reject: true},
		{name: "nested list", values: []any{[]any{1, 2}}, reject: true},
		{name: "null element", values: []any{nil}, reject: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := map[string]property{}
			err := flatten(map[string]any{"app": map[string]any{"values": tt.values}}, "", "application.yml", result)
			if tt.reject {
				require.ErrorContains(t, err, "non-scalar configuration list in application.yml")
				require.NotContains(t, result, "app.values")
				return
			}
			require.NoError(t, err)
			require.Equal(t, property{text: tt.want, source: "application.yml", key: "app.values"}, result["app.values"])
		})
	}
}

func TestAnalyzeRejectsKnownDatasourceCredentials(t *testing.T) {
	const secret = "sensitive-do-not-disclose"
	for _, tt := range []struct {
		name string
		file string
		data string
	}{
		{
			name: "common password",
			file: "src/main/resources/application.properties",
			data: "spring.datasource.password=" + secret,
		},
		{
			name: "password fallback",
			file: "src/main/resources/application.properties",
			data: "spring.datasource.password=${DATABASE_PASSWORD:" + secret + "}",
		},
		{
			name: "inactive profile",
			file: "src/main/resources/application-unused.properties",
			data: "spring.datasource.password=" + secret,
		},
		{
			name: "external YAML",
			file: "config/application.yaml",
			data: "spring:\n  datasource:\n    password: " + secret,
		},
		{
			name: "JDBC user info",
			file: "src/main/resources/application.properties",
			data: "spring.datasource.url=jdbc:postgresql://app:" + secret + "@localhost/catalog",
		},
		{
			name: "JDBC query password",
			file: "src/main/resources/application.properties",
			data: "spring.datasource.url=jdbc:mysql://localhost/catalog?user=app&password=" + secret,
		},
		{
			name: "JDBC fallback password",
			file: "src/main/resources/application.properties",
			data: "spring.datasource.url=${DATABASE_URL:jdbc:postgresql://localhost/catalog?password=" + secret + "}",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "pom.xml", "<project/>")
			writeFixture(t, directory, tt.file, tt.data)

			application, err := Analyze(t.Context(), directory,
				staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(nil))
			require.ErrorContains(t, err, tt.file)
			require.ErrorContains(t, err, "runtime placeholder without a default")
			require.NotContains(t, err.Error(), secret)
			require.Nil(t, application)
		})
	}
}

func TestKnownDatasourceCredentialReferences(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
		jdbc  bool
	}{
		{"common password reference", "${DATABASE_PASSWORD}", false},
		{"empty password fallback", "${DATABASE_PASSWORD:}", false},
		{"JDBC query reference", "jdbc:mysql://localhost/catalog?password=${DATABASE_PASSWORD}", true},
		{"JDBC user info reference", "jdbc:postgresql://app:${DATABASE_PASSWORD}@localhost/catalog", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.jdbc {
				require.False(t, jdbcURLHasLiteralPassword(tt.value))
			} else {
				require.False(t, hasLiteralCredential(tt.value))
			}
		})
	}
}

func TestApplyCredentialRepairs(t *testing.T) {
	for _, tt := range []struct {
		name        string
		original    string
		replacement string
	}{
		{"placeholder fallback", "${POSTGRES_PASSWORD:local-default}", "${POSTGRES_PASSWORD}"},
		{"literal", "local-default", "${SPRING_DATASOURCE_PASSWORD}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			file := "src/main/resources/application-postgres.properties"
			writeFixture(t, directory, file,
				"# keep this comment\r\nspring.datasource.password = "+tt.original+"\r\n")
			err := ApplyCredentialRepairs(t.Context(), directory, []CredentialRepair{{
				File: file, Property: "spring.datasource.password",
				Replacement: tt.replacement, original: tt.original,
			}})
			require.NoError(t, err)
			data, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(file)))
			require.NoError(t, err)
			require.Equal(t,
				"# keep this comment\r\nspring.datasource.password = "+tt.replacement+"\r\n", string(data))
		})
	}
}

func TestApplyCredentialRepairsRejectsChangedSource(t *testing.T) {
	directory := t.TempDir()
	file := "src/main/resources/application.properties"
	writeFixture(t, directory, file, "spring.datasource.password=${DATABASE_PASSWORD:changed}\n")
	err := ApplyCredentialRepairs(t.Context(), directory, []CredentialRepair{{
		File: file, Property: "spring.datasource.password",
		Replacement: "${DATABASE_PASSWORD}", original: "${DATABASE_PASSWORD:analyzed}",
	}})
	require.ErrorContains(t, err, "changed after analysis")
	data, readErr := os.ReadFile(filepath.Join(directory, filepath.FromSlash(file)))
	require.NoError(t, readErr)
	require.Contains(t, string(data), "changed")
}

func TestAnalyzeIgnoresUnknownCredentialLikeProperties(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "pom.xml", "<project/>")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"app.password=top-secret\napp.token=sensitive-token\n")

	application, err := Analyze(t.Context(), directory,
		staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(nil))
	require.NoError(t, err)
	require.NotNil(t, application)
}

func TestAnalyzeIgnoresUncopiedExternalProfileCredentials(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "pom.xml", "<project/>")
	writeFixture(t, directory, "config/application-unused.properties",
		"spring.datasource.password=not-retained\n")

	application, err := Analyze(t.Context(), directory,
		staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(nil))
	require.NoError(t, err)
	require.NotNil(t, application)
	require.Empty(t, application.ExternalConfigurations)
}
