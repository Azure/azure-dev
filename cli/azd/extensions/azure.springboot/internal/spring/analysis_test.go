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
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o750))
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

func TestAnalyzePropertiesDocumentSeparators(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		for _, separator := range []string{"#---", "!---"} {
			t.Run(fmt.Sprintf("%q/%s", newline, separator), func(t *testing.T) {
				directory := t.TempDir()
				writeFixture(t, directory, "pom.xml", "<project/>")
				content := strings.Join([]string{
					"spring.profiles.active=cloud",
					separator,
					"spring.config.activate.on-profile=cloud",
					"spring.datasource.url=jdbc:mysql://localhost/warehouse",
					separator,
					"spring.config.activate.on-profile=local",
					"spring.datasource.url=jdbc:postgresql://localhost/ignored",
				}, newline)
				writeFixture(t, directory, "src/main/resources/application.properties", content)
				a, err := Analyze(t.Context(), directory,
					staticResolver{model: effectiveModel(allDrivers())}, envLookup(nil))
				require.NoError(t, err)
				require.Equal(t, []string{"cloud"}, a.Profiles)
				require.Len(t, a.Requirements, 1)
				require.Equal(t, MySQL, a.Requirements[0].Kind)
				require.Equal(t, "warehouse", a.Requirements[0].Name)
			})
		}
	}
}

func TestAnalyzeRetainsActiveExternalConfiguration(t *testing.T) {
	for _, location := range []string{"", "config/", "config/deployment/"} {
		t.Run(location, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "pom.xml", "<project/>")
			writeFixture(t, directory, "src/main/resources/application.properties", "spring.profiles.active=cloud")
			writeFixture(t, directory, "src/main/resources/application-cloud.properties", "server.port=9999")
			base := location + "application.properties"
			active := location + "application-cloud.properties"
			inactive := location + "application-local.properties"
			unmatched := location + "application-cloud.yml"
			writeFixture(t, directory, base, "example.setting=base")
			writeFixture(t, directory, active, "spring.datasource.url=jdbc:mysql://localhost/warehouse\n"+
				"server.port=9090\n#---\nspring.config.activate.on-profile=cloud\nexample.setting=deployed\n")
			writeFixture(t, directory, inactive, "spring.datasource.url=jdbc:postgresql://localhost/ignored")
			writeFixture(t, directory, unmatched, "spring:\n  config:\n    activate:\n      on-profile: local\n")
			a, err := Analyze(t.Context(), directory, staticResolver{model: effectiveModel(allDrivers())}, envLookup(nil))
			require.NoError(t, err)
			require.Equal(t, []string{base, active}, a.ExternalConfigurations)
			require.Equal(t, 9090, a.Port)
			require.Len(t, a.Requirements, 1)
			require.Equal(t, MySQL, a.Requirements[0].Kind)
			plan, err := Generate(t.Context(), a)
			require.NoError(t, err)
			docker := string(plan.Files[".azd-spring/Dockerfile"])
			for _, file := range []string{base, active} {
				require.Contains(t, docker, fmt.Sprintf(`["/workspace/%s","/app/%s"]`, file, file))
			}
			for _, file := range []string{inactive, unmatched, "src/main/resources/application-cloud.properties"} {
				require.NotContains(t, docker, "/workspace/"+file)
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

func TestEffectiveModelExecutionConfiguration(t *testing.T) {
	execution := func(phase, configuration string) string {
		return "<execution><phase>" + phase + "</phase><goals><goal>repackage</goal></goals>" +
			"<configuration>" + configuration + "</configuration></execution>"
	}
	valid := execution("", "")
	for _, tt := range []struct {
		name, plugin, executions, errorText string
	}{
		{"default execution", "", valid, ""},
		{"explicit package phase", "", execution("package", ""), ""},
		{"execution skip", "", execution("", "<skip>true</skip>"), "custom or disabled"},
		{"execution classifier", "", execution("", "<classifier>exec</classifier>"), "custom or disabled"},
		{"execution layout", "", execution("", "<layout>NONE</layout>"), "custom or disabled"},
		{"inherited skip", "<skip>true</skip>", valid, "custom or disabled"},
		{"inherited classifier", "<classifier>exec</classifier>", valid, "custom or disabled"},
		{"inherited layout", "<layout>NONE</layout>", valid, "custom or disabled"},
		{"override skip", "<skip>true</skip>", execution("", "<skip>false</skip>"), ""},
		{"override layout", "<layout>NONE</layout>", execution("", "<layout>JAR</layout>"), ""},
		{"retain inherited classifier", "<classifier>exec</classifier>",
			execution("", "<skip>false</skip>"), "custom or disabled"},
		{"empty classifier inherits", "<classifier>exec</classifier>",
			execution("", "<classifier/>"), "custom or disabled"},
		{"override configuration", "<skip>true</skip><classifier>exec</classifier><layout>NONE</layout>",
			strings.Replace(valid, "<configuration>", `<configuration combine.self="override">`, 1), ""},
		{"multiple valid executions", "", valid + execution("package", "<skip>false</skip>"), ""},
		{"invalid later execution", "", valid + execution("package", "<skip>true</skip>"), "custom or disabled"},
		{"invalid earlier execution", "", execution("package", "<classifier>exec</classifier>") + valid,
			"custom or disabled"},
		{"inactive execution", "", valid + execution("none", "<skip>true</skip>"), ""},
		{"unrelated goal", "", valid + strings.Replace(execution("", "<skip>true</skip>"), "repackage", "run", 1), ""},
		{"no package execution", "", execution("none", "<skip>true</skip>"), "active Spring Boot repackage execution"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := string(effectiveModel(""))
			start := strings.Index(data, "<executions>")
			end := strings.Index(data, "</executions>") + len("</executions>")
			data = data[:start] + "<configuration>" + tt.plugin + "</configuration><executions>" +
				tt.executions + "</executions>" + data[end:]
			_, java, err := parseModel([]byte(data))
			if tt.errorText != "" {
				require.ErrorContains(t, err, tt.errorText)
			} else {
				require.NoError(t, err)
				require.Equal(t, 17, java)
			}
		})
	}
}
