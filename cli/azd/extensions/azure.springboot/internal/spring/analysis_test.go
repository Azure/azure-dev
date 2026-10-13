// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type staticResolver struct {
	model []byte
	err   error
}

func (r staticResolver) Resolve(context.Context, string) ([]byte, error) { return r.model, r.err }

func effectiveModel(drivers string) []byte {
	return fmt.Appendf(nil, `<project>
<artifactId>inventory-api</artifactId><version>1.0</version><packaging>jar</packaging>
<properties><java.version>17</java.version></properties>
<build><finalName>inventory-api-1.0</finalName><plugins><plugin>
<groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId>
<executions><execution><goals><goal>repackage</goal></goals></execution></executions>
</plugin></plugins></build><dependencies>%s</dependencies></project>`, drivers)
}

func dependency(group, artifact, scope string) string {
	return fmt.Sprintf("<dependency><groupId>%s</groupId><artifactId>%s</artifactId><scope>%s</scope></dependency>",
		group, artifact, scope)
}

func allDrivers() string {
	return dependency("com.h2database", "h2", "runtime") +
		dependency("com.mysql", "mysql-connector-j", "runtime") +
		dependency("org.postgresql", "postgresql", "runtime")
}

func writeFixture(t *testing.T, directory, name, content string) {
	t.Helper()
	file := filepath.Join(directory, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
	require.NoError(t, os.WriteFile(file, []byte(content), 0o600))
}

func envLookup(values map[string]string) LookupEnv {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func TestAnalyzeActiveDatasource(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		env       map[string]string
		drivers   string
		kind      ResourceKind
		embedded  bool
		database  string
		profiles  []string
		errorText string
	}{
		{
			name: "all drivers default to embedded",
			files: map[string]string{
				"src/main/resources/application-remote.properties": "spring.datasource.url=jdbc:mysql://localhost/warehouse",
			},
			drivers: allDrivers(), embedded: true, profiles: []string{"default"},
		},
		{
			name: "normal environment activates mysql alternative",
			files: map[string]string{
				"src/main/resources/application-cloud.properties": "spring.datasource.url=" +
					"${DB_URL:jdbc:mysql://localhost/warehouse}",
				"src/main/resources/application-other.properties": "spring.datasource.url=" +
					"jdbc:postgresql://localhost/ignored",
			},
			env: map[string]string{"SPRING_PROFILES_ACTIVE": "cloud"}, drivers: allDrivers(),
			kind: MySQL, database: "warehouse", profiles: []string{"cloud"},
		},
		{
			name: "active yaml postgres document",
			files: map[string]string{"src/main/resources/application.yml": `spring:
  profiles:
    active: release
---
spring:
  config:
    activate:
      on-profile: "release & !local"
  datasource:
    url: "jdbc:postgresql://localhost/catalog_db?sslmode=disable"
---
spring:
  config:
    activate:
      on-profile: local
  datasource:
    url: "jdbc:mysql://localhost/ignored"
`},
			drivers: allDrivers(), kind: PostgreSQL, database: "catalog_db", profiles: []string{"release"},
		},
		{
			name: "profile includes and groups",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.active=release\n" +
					"spring.profiles.group.release=sql\n",
				"src/main/resources/application-sql.properties": "spring.datasource.url=jdbc:postgresql://localhost/catalog",
			},
			drivers: allDrivers(), kind: PostgreSQL, database: "catalog", profiles: []string{"release", "sql"},
		},
		{
			name: "default profile",
			files: map[string]string{
				"src/main/resources/application.properties":       "spring.profiles.default=cloud",
				"src/main/resources/application-cloud.properties": "spring.datasource.url=jdbc:mysql://localhost/warehouse",
			},
			drivers: allDrivers(), kind: MySQL, database: "warehouse", profiles: []string{"cloud"},
		},
		{
			name: "environment wins over profile and nested placeholders",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.datasource.url=jdbc:postgresql://localhost/catalog",
			},
			env: map[string]string{
				"SPRING_DATASOURCE_URL": "jdbc:mysql://${DB_HOST:localhost}/${DB_NAME:${DEFAULT_DB:warehouse}}",
			},
			drivers: allDrivers(), kind: MySQL, database: "warehouse", profiles: []string{"default"},
		},
		{
			name: "json wins over ordinary environment",
			env: map[string]string{
				"SPRING_DATASOURCE_URL":   "jdbc:mysql://localhost/ignored",
				"SPRING_APPLICATION_JSON": `{"spring":{"datasource":{"url":"jdbc:postgresql://localhost/catalog"}}}`,
			},
			drivers: allDrivers(), kind: PostgreSQL, database: "catalog", profiles: []string{"default"},
		},
		{
			name: "default external wildcard configuration",
			files: map[string]string{
				"config/deployment/application.properties": "spring.datasource.url=jdbc:mysql://localhost/warehouse",
			},
			drivers: allDrivers(), kind: MySQL, database: "warehouse", profiles: []string{"default"},
		},
		{
			name: "external base overrides packaged profile",
			files: map[string]string{
				"application.properties":                    "spring.datasource.url=jdbc:postgresql://localhost/catalog",
				"src/main/resources/application.properties": "spring.profiles.active=cloud",
				"src/main/resources/application-cloud.properties": "spring.datasource.url=" +
					"jdbc:mysql://localhost/ignored",
			},
			drivers: allDrivers(), kind: PostgreSQL, database: "catalog", profiles: []string{"cloud"},
		},
		{
			name: "multi document Java properties",
			files: map[string]string{"src/main/resources/application.properties": "spring.profiles.active=cloud\n" +
				"#---\nspring.config.activate.on-profile=cloud\nspring.datasource.url=jdbc:mysql://localhost/warehouse\n" +
				"#---\nspring.config.activate.on-profile=local\n" +
				"spring.datasource.url=jdbc:postgresql://localhost/ignored\n"},
			drivers: allDrivers(), kind: MySQL, database: "warehouse", profiles: []string{"cloud"},
		},
		{
			name: "includes are activated before explicit profiles",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.include=shared\nspring.profiles.active=cloud",
				"src/main/resources/application-shared.properties": "spring.datasource.url=" +
					"jdbc:postgresql://localhost/ignored",
				"src/main/resources/application-cloud.properties": "spring.datasource.url=" +
					"jdbc:mysql://localhost/warehouse",
			},
			drivers: allDrivers(), kind: MySQL, database: "warehouse", profiles: []string{"shared", "cloud"},
		},
		{
			name: "comments are not custom datasource evidence",
			files: map[string]string{
				"src/main/java/example/Comment.java": "// @Bean DataSource create()\n" +
					"class Comment { String text = \"new HikariDataSource()\"; }",
			},
			drivers: allDrivers(), embedded: true, profiles: []string{"default"},
		},
		{
			name:    "direct Hikari binding is not silently ignored",
			env:     map[string]string{"SPRING_DATASOURCE_HIKARI_JDBC_URL": "jdbc:mysql://localhost/warehouse"},
			drivers: allDrivers(), errorText: "custom datasource",
		},
		{
			name:    "file based embedded storage is not assumed durable",
			env:     map[string]string{"SPRING_DATASOURCE_URL": "jdbc:h2:file:./catalog"},
			drivers: allDrivers(), errorText: "persistence policy",
		},
		{
			name:    "test dependency is not a runtime driver",
			env:     map[string]string{"SPRING_DATASOURCE_URL": "jdbc:mysql://localhost/warehouse"},
			drivers: dependency("com.mysql", "mysql-connector-j", "test"), errorText: "runtime MySQL JDBC driver",
		},
		{
			name:    "missing external activation is not guessed",
			drivers: dependency("org.postgresql", "postgresql", "runtime"), errorText: "no active datasource URL",
		},
		{
			name:    "unresolved placeholder",
			files:   map[string]string{"src/main/resources/application.properties": "spring.datasource.url=${REQUIRED_DB}"},
			drivers: allDrivers(), errorText: "unresolved",
		},
		{
			name: "circular placeholder",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.datasource.url=${first}\n" +
					"first=${second}\nsecond=${first}",
			},
			drivers: allDrivers(), errorText: "circular",
		},
		{
			name:    "managed identity is not replaced with password",
			env:     map[string]string{"SPRING_DATASOURCE_AZURE_PASSWORDLESS_ENABLED": "true"},
			drivers: allDrivers(), errorText: "passwordless",
		},
		{
			name: "custom bean is explicit ambiguity",
			files: map[string]string{
				"src/main/java/example/Config.java": "@Bean public DataSource source() { return create(); }",
			},
			drivers: allDrivers(), errorText: "custom datasource",
		},
		{
			name:    "programmatic activation is not guessed",
			files:   map[string]string{"src/main/java/example/App.java": `app.setAdditionalProfiles("cloud");`},
			drivers: allDrivers(), errorText: "programmatic profile",
		},
		{
			name:    "unsupported active resource",
			env:     map[string]string{"SPRING_DATA_MONGODB_URI": "mongodb://localhost/catalog"},
			drivers: allDrivers(), errorText: "not yet supported",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "pom.xml", "<project/>")
			for name, content := range tt.files {
				writeFixture(t, directory, name, content)
			}
			a, err := Analyze(t.Context(), directory, staticResolver{model: effectiveModel(tt.drivers)}, envLookup(tt.env))
			if tt.errorText != "" {
				require.ErrorContains(t, err, tt.errorText)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.embedded, a.Embedded)
			require.Equal(t, tt.profiles, a.Profiles)
			if tt.kind == "" {
				require.Empty(t, a.Requirements)
			} else {
				require.Len(t, a.Requirements, 1)
				require.Equal(t, tt.kind, a.Requirements[0].Kind)
				require.Equal(t, tt.database, a.Requirements[0].Name)
				require.Equal(t, "password", a.Requirements[0].Authentication)
			}
		})
	}
}

func TestCredentialNonDisclosure(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "pom.xml", "<project/>")
	resolver := staticResolver{model: effectiveModel(allDrivers())}
	for _, jdbc := range []string{
		"jdbc:mysql://sensitive-user:sensitive-password@localhost/catalog",
		"jdbc:postgresql://sensitive-user:sensitive-password@localhost/catalog?token=sensitive-token#sensitive-fragment",
		"jdbc:mysql://localhost/catalog?password=sensitive-password&sig=sensitive-signature",
	} {
		a, err := Analyze(t.Context(), directory, resolver, envLookup(map[string]string{"SPRING_DATASOURCE_URL": jdbc}))
		output := fmt.Sprintf("%+v", a)
		if err != nil {
			output += err.Error()
		}
		require.NotContains(t, output, "sensitive-")
	}
	cause := errors.New("Maven failed at https://sensitive-user:sensitive-password@host/?sig=sensitive-token")
	err := &modelError{cause: cause}
	require.NotContains(t, err.Error(), "sensitive-")
	require.ErrorIs(t, err, cause)
}

func TestEffectiveModelValidation(t *testing.T) {
	for _, tt := range []struct {
		name, before, after, errorText string
	}{
		{"java release", "<java.version>17</java.version>", "<java.version>21</java.version>", ""},
		{"multi module", "<packaging>jar</packaging>", "<packaging>pom</packaging><modules><module>app</module></modules>",
			"single-module"},
		{"unsafe jar", "<finalName>inventory-api-1.0</finalName>", "<finalName>../secrets</finalName>", "safe file"},
		{"missing java", "<java.version>17</java.version>", "", "Java release"},
		{"missing repackage", "<goal>repackage</goal>", "<goal>run</goal>", "repackage execution"},
		{"classifier", "</plugin>", "<configuration><classifier>exec</classifier></configuration></plugin>",
			"custom or disabled"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := strings.ReplaceAll(string(effectiveModel("")), tt.before, tt.after)
			_, java, err := parseModel([]byte(data))
			if tt.errorText != "" {
				require.ErrorContains(t, err, tt.errorText)
			} else {
				require.NoError(t, err)
				require.Equal(t, 21, java)
			}
		})
	}
}
