// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"errors"
	"io/fs"
	"os"
	"strings"
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

func TestAnalyzeRejectsPlaceholderDriverJDBCCredentials(t *testing.T) {
	const secret = "placeholder-driver-secret"
	for _, engine := range []string{"mysql", "postgresql"} {
		t.Run(engine, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "pom.xml", "<project/>")
			const file = "src/main/resources/application-unused.properties"
			value := "jdbc:${DB_ENGINE:" + engine + "}://localhost/catalog?password=" + secret
			writeFixture(t, directory, file, "spring.datasource.url="+value+"\n")

			application, err := Analyze(t.Context(), directory,
				staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(nil))
			require.ErrorContains(t, err, "literal JDBC credentials")
			require.ErrorContains(t, err, file)
			require.NotContains(t, err.Error(), secret)
			require.NotContains(t, err.Error(), value)
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
				literal, err := jdbcURLHasLiteralPassword(tt.value)
				require.NoError(t, err)
				require.False(t, literal)
			} else {
				require.False(t, hasLiteralCredential(tt.value))
			}
		})
	}
}

func TestAnalyzeRejectsKnownAzureOpenAIKeys(t *testing.T) {
	const secret = "foundry-key-must-not-be-disclosed"
	for _, file := range []string{
		"src/main/resources/application.properties",
		"src/main/resources/application-unused.properties",
		"config/application.properties",
	} {
		t.Run(file, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "pom.xml", "<project/>")
			writeFixture(t, directory, file, "spring.ai.azure.openai.api-key="+secret+"\n")

			application, err := Analyze(t.Context(), directory,
				staticResolver{model: effectiveModel(t, directory, "")}, envLookup(nil))
			require.ErrorContains(t, err, file)
			require.ErrorContains(t, err, "runtime placeholder without a default")
			require.NotContains(t, err.Error(), secret)
			require.Nil(t, application)
		})
	}
}

func TestAzureOpenAIKeyCredentialReferences(t *testing.T) {
	for _, value := range []string{"", "${AZURE_OPENAI_API_KEY}", "${AZURE_OPENAI_API_KEY:}"} {
		require.False(t, hasLiteralCredential(value))
	}
}

func TestAnalyzeRejectsUninspectableJDBCCredentials(t *testing.T) {
	const testValue = "jdbc-fixture-value"
	for _, engine := range []string{"mysql", "postgresql"} {
		for _, file := range []string{
			"src/main/resources/application-unused.properties",
			"config/application.properties",
		} {
			for _, tt := range []struct {
				name    string
				address string
			}{
				{"placeholder host query", "${DB_HOST:localhost}/catalog?password=" + testValue},
				{"placeholder host user info", "app:" + testValue + "@${DB_HOST:localhost}/catalog"},
				{"placeholder port", "localhost:${DB_PORT:5432}/catalog?password=" + testValue},
				{"malformed query", "localhost/catalog?option=%ZZ&password=" + testValue},
			} {
				t.Run(engine+"/"+file+"/"+tt.name, func(t *testing.T) {
					directory := t.TempDir()
					writeFixture(t, directory, "pom.xml", "<project/>")
					writeFixture(t, directory, file, "spring.datasource.url=jdbc:"+engine+"://"+tt.address+"\n")
					application, err := Analyze(t.Context(), directory,
						staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(nil))
					require.ErrorContains(t, err, "cannot safely inspect JDBC credentials")
					require.ErrorContains(t, err, file)
					require.ErrorContains(t, err, "spring.datasource.url")
					require.NotContains(t, err.Error(), testValue)
					require.NotContains(t, err.Error(), tt.address)
					require.Nil(t, application)
				})
			}
		}
	}
}

func TestAnalyzeRejectsAlternateJDBCFormats(t *testing.T) {
	const testValue = "alternate-jdbc-fixture-value"
	for _, tt := range []struct {
		name string
		url  string
	}{
		{
			name: "PostgreSQL database-only format",
			url:  "jdbc:postgresql:catalog?password=" + testValue,
		},
		{
			name: "MySQL address format",
			url: "jdbc:mysql://address=(host=localhost)(user=app)(password=" +
				testValue + ")/catalog",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "pom.xml", "<project/>")
			const file = "src/main/resources/application-unused.properties"
			writeFixture(t, directory, file, "spring.datasource.url="+tt.url+"\n")

			application, err := Analyze(t.Context(), directory,
				staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(nil))
			require.ErrorContains(t, err, "cannot safely inspect JDBC credentials")
			require.ErrorContains(t, err, file)
			require.ErrorContains(t, err, "spring.datasource.url")
			require.NotContains(t, err.Error(), testValue)
			require.NotContains(t, err.Error(), tt.url)
			require.Nil(t, application)
		})
	}
}

func TestJDBCURLCredentialPlaceholders(t *testing.T) {
	const testValue = "jdbc-fixture-value"
	for _, engine := range []string{"mysql", "postgresql"} {
		for _, tt := range []struct {
			name    string
			address string
			literal bool
		}{
			{"query reference", "localhost/catalog?password=${DB_PASSWORD}", false},
			{"user info reference", "app:${DB_PASSWORD}@localhost/catalog", false},
			{"query default", "localhost/catalog?password=${DB_PASSWORD:" + testValue + "}", true},
			{"user info default", "app:${DB_PASSWORD:" + testValue + "}@localhost/catalog", true},
			{"empty query default", "localhost/catalog?password=${DB_PASSWORD:}", false},
			{"empty user info default", "app:${DB_PASSWORD:}@localhost/catalog", false},
		} {
			t.Run(engine+"/"+tt.name, func(t *testing.T) {
				literal, err := jdbcURLHasLiteralPassword("jdbc:" + engine + "://" + tt.address)
				require.NoError(t, err)
				require.Equal(t, tt.literal, literal)
			})
		}
	}
}

func TestAnalyzeRejectsDuplicateDatasourceCredentialProperties(t *testing.T) {
	const secret = "earlier-value-must-not-be-disclosed"
	for _, property := range []string{"spring.datasource.password", "spring.datasource.url"} {
		t.Run(property, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "pom.xml", "<project/>")
			safe := "${DATABASE_PASSWORD}"
			if strings.HasSuffix(property, ".url") {
				safe = "jdbc:postgresql://localhost/catalog"
			}
			writeFixture(t, directory, "src/main/resources/application.properties",
				property+"="+secret+"\n"+property+"="+safe+"\n")
			application, err := Analyze(t.Context(), directory,
				staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(nil))
			require.ErrorContains(t, err, "duplicate datasource credential property")
			require.NotContains(t, err.Error(), secret)
			require.Nil(t, application)
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
			data, err := fs.ReadFile(os.DirFS(directory), file)
			require.NoError(t, err)
			require.Equal(t,
				"# keep this comment\r\nspring.datasource.password = "+tt.replacement+"\r\n", string(data))
		})
	}
}

func TestApplyAzureOpenAIKeyRepair(t *testing.T) {
	directory := t.TempDir()
	file := "src/main/resources/application.properties"
	writeFixture(t, directory, file, "spring.ai.azure.openai.api-key=local-default\n")
	err := ApplyCredentialRepairs(t.Context(), directory, []CredentialRepair{{
		File: file, Property: "spring.ai.azure.openai.api-key",
		Replacement: "${AZURE_OPENAI_API_KEY}", original: "local-default",
	}})
	require.NoError(t, err)
	data, err := fs.ReadFile(os.DirFS(directory), file)
	require.NoError(t, err)
	require.Equal(t, "spring.ai.azure.openai.api-key=${AZURE_OPENAI_API_KEY}\n", string(data))
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
	data, readErr := fs.ReadFile(os.DirFS(directory), file)
	require.NoError(t, readErr)
	require.Contains(t, string(data), "changed")
}

func TestApplyCredentialRepairsStagingFailureLeavesSourceUnchanged(t *testing.T) {
	directory := t.TempDir()
	file := "src/main/resources/application.properties"
	const original = "spring.datasource.password=${DATABASE_PASSWORD:local}\n"
	writeFixture(t, directory, file, original)
	ops := defaultCredentialRepairFileOps()
	ops.stage = func(*os.Root, string, []byte, os.FileMode) (string, error) {
		return "", errors.New("injected write failure")
	}
	err := applyCredentialRepairs(t.Context(), directory, []CredentialRepair{{
		File: file, Property: "spring.datasource.password",
		Replacement: "${DATABASE_PASSWORD}", original: "${DATABASE_PASSWORD:local}",
	}}, ops)
	require.ErrorContains(t, err, "injected write failure")
	data, readErr := fs.ReadFile(os.DirFS(directory), file)
	require.NoError(t, readErr)
	require.Equal(t, original, string(data))
}

func TestApplyCredentialRepairsRenameFailureRollsBack(t *testing.T) {
	directory := t.TempDir()
	files := []string{
		"src/main/resources/application-mysql.properties",
		"src/main/resources/application-postgres.properties",
	}
	var repairs []CredentialRepair
	for _, file := range files {
		writeFixture(t, directory, file, "spring.datasource.password=local\n")
		repairs = append(repairs, CredentialRepair{
			File: file, Property: "spring.datasource.password",
			Replacement: "${SPRING_DATASOURCE_PASSWORD}", original: "local",
		})
	}
	ops := defaultCredentialRepairFileOps()
	rename := ops.rename
	calls := 0
	ops.rename = func(root *os.Root, oldName, newName string) error {
		calls++
		if calls == 2 {
			return errors.New("injected rename failure")
		}
		return rename(root, oldName, newName)
	}
	err := applyCredentialRepairs(t.Context(), directory, repairs, ops)
	require.ErrorContains(t, err, "injected rename failure")
	for _, file := range files {
		data, readErr := fs.ReadFile(os.DirFS(directory), file)
		require.NoError(t, readErr)
		require.Equal(t, "spring.datasource.password=local\n", string(data))
	}
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
