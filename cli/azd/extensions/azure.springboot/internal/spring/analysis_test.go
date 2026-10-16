// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
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

func effectiveModel(t *testing.T, directory, drivers string) []byte {
	t.Helper()
	var escaped strings.Builder
	require.NoError(t, xml.EscapeText(&escaped, []byte(filepath.Join(directory, "target"))))
	return fmt.Appendf(nil, `<project>
<artifactId>inventory-api</artifactId><version>1.0</version><packaging>jar</packaging>
<properties><java.version>17</java.version></properties>
<build><directory>%s</directory><finalName>inventory-api-1.0</finalName><plugins><plugin>
<groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId>
<executions><execution><goals><goal>repackage</goal></goals></execution></executions>
</plugin></plugins></build><dependencies>%s</dependencies></project>`, escaped.String(), drivers)
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
			name: "groups expand before the next explicit profile",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.active=release,local\n" +
					"spring.profiles.group.release=sql",
				"src/main/resources/application-sql.properties": "spring.datasource.url=jdbc:postgresql://localhost/catalog",
				"src/main/resources/application-local.properties": "spring.datasource.url=" +
					"jdbc:mysql://localhost/warehouse",
			},
			drivers: allDrivers(), kind: MySQL, database: "warehouse", profiles: []string{"release", "sql", "local"},
		},
		{
			name: "nested groups expand depth first",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.active=release,local\n" +
					"spring.profiles.group.release=sql,shared\nspring.profiles.group.sql=database",
				"src/main/resources/application-database.properties": "spring.datasource.url=" +
					"jdbc:postgresql://localhost/catalog",
				"src/main/resources/application-local.properties": "spring.datasource.url=" +
					"jdbc:mysql://localhost/warehouse",
			},
			drivers: allDrivers(), kind: MySQL, database: "warehouse",
			profiles: []string{"release", "sql", "database", "shared", "local"},
		},
		{
			name: "cyclic groups retain first activation and explicit precedence",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.active=release,sql,local\n" +
					"spring.profiles.group.release=sql\nspring.profiles.group.sql=release",
				"src/main/resources/application-sql.properties": "spring.datasource.url=jdbc:postgresql://localhost/catalog",
				"src/main/resources/application-local.properties": "spring.datasource.url=" +
					"jdbc:mysql://localhost/warehouse",
			},
			drivers: allDrivers(), kind: MySQL, database: "warehouse", profiles: []string{"release", "sql", "local"},
		},
		{
			name: "default profile groups preserve activation order",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.default=release,local\n" +
					"spring.profiles.group.release=sql",
				"src/main/resources/application-sql.properties": "spring.datasource.url=jdbc:postgresql://localhost/catalog",
				"src/main/resources/application-local.properties": "spring.datasource.url=" +
					"jdbc:mysql://localhost/warehouse",
			},
			drivers: allDrivers(), kind: MySQL, database: "warehouse", profiles: []string{"release", "sql", "local"},
		},
		{
			name: "profile group names are case sensitive",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.active=Prod\n" +
					"spring.profiles.group.Prod=postgres\nspring.profiles.group.prod=mysql",
				"src/main/resources/application-postgres.properties": "spring.datasource.url=" +
					"jdbc:postgresql://localhost/catalog",
				"src/main/resources/application-mysql.properties": "spring.datasource.url=" +
					"jdbc:mysql://localhost/warehouse",
			},
			drivers: allDrivers(), kind: PostgreSQL, database: "catalog", profiles: []string{"Prod", "postgres"},
		},
		{
			name: "profile group punctuation remains distinct",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.active=prod_sql\n" +
					"spring.profiles.group.prod-sql=postgres\nspring.profiles.group.prod_sql=mysql",
				"src/main/resources/application-postgres.properties": "spring.datasource.url=" +
					"jdbc:postgresql://localhost/catalog",
				"src/main/resources/application-mysql.properties": "spring.datasource.url=" +
					"jdbc:mysql://localhost/warehouse",
			},
			drivers: allDrivers(), kind: MySQL, database: "warehouse", profiles: []string{"prod_sql", "mysql"},
		},
		{
			name: "exact nested group names expand depth first",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.active=Prod-Group\n" +
					"spring.profiles.group.Prod-Group=SQL_Group\nspring.profiles.group.SQL_Group=postgres\n" +
					"spring.profiles.group.sql-group=mysql",
				"src/main/resources/application-postgres.properties": "spring.datasource.url=" +
					"jdbc:postgresql://localhost/catalog",
				"src/main/resources/application-mysql.properties": "spring.datasource.url=" +
					"jdbc:mysql://localhost/warehouse",
			},
			drivers: allDrivers(), kind: PostgreSQL, database: "catalog",
			profiles: []string{"Prod-Group", "SQL_Group", "postgres"},
		},
		{
			name: "YAML profile group keys retain exact suffixes",
			files: map[string]string{
				"src/main/resources/application.yml": `spring:
  profiles:
    active: Prod.SQL
    group:
      Prod.SQL: postgres
      prod_sql: mysql
`,
				"src/main/resources/application-postgres.properties": "spring.datasource.url=" +
					"jdbc:postgresql://localhost/catalog",
				"src/main/resources/application-mysql.properties": "spring.datasource.url=" +
					"jdbc:mysql://localhost/warehouse",
			},
			drivers: allDrivers(), kind: PostgreSQL, database: "catalog", profiles: []string{"Prod.SQL", "postgres"},
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
			name: "unrelated YAML scalar lists do not block datasource analysis",
			files: map[string]string{"src/main/resources/application.yml": `spring:
  datasource:
    url: jdbc:postgresql://localhost/catalog
app:
  ports: [8080, 9090]
  flags: [true, false]
  weights: [0.5, 1.5]
`},
			drivers: allDrivers(), kind: PostgreSQL, database: "catalog", profiles: []string{"default"},
		},
		{
			name: "JSON scalar lists use the same flattening rules",
			env: map[string]string{
				"SPRING_APPLICATION_JSON": `{"spring":{"datasource":{"url":"jdbc:postgresql://localhost/catalog"}},
"app":{"ports":[8080,9090],"flags":[true,false],"weights":[0.5,1.5]}}`,
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
			name: "includes accumulate across packaged and external sources",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.include=sql",
				"src/main/resources/application-sql.properties": "spring.datasource.url=" +
					"jdbc:postgresql://localhost/catalog",
				"application.properties": "spring.profiles.include=cloud",
			},
			drivers: allDrivers(), kind: PostgreSQL, database: "catalog", profiles: []string{"cloud", "sql"},
		},
		{
			name: "includes retain high to low source ordering",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.include=sql",
				"config/application.properties":             "spring.profiles.include=cloud",
				"src/main/resources/application-sql.properties": "spring.datasource.url=" +
					"jdbc:postgresql://localhost/catalog",
				"src/main/resources/application-cloud.properties": "spring.datasource.url=" +
					"jdbc:mysql://localhost/ignored",
			},
			drivers: allDrivers(), kind: PostgreSQL, database: "catalog", profiles: []string{"cloud", "sql"},
		},
		{
			name: "includes accumulate across property documents",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.include=sql\n" +
					"#---\nspring.profiles.include=cloud\nspring.profiles.active=release",
				"src/main/resources/application-sql.properties": "spring.datasource.url=" +
					"jdbc:postgresql://localhost/catalog",
			},
			drivers: allDrivers(), kind: PostgreSQL, database: "catalog", profiles: []string{"cloud", "sql", "release"},
		},
		{
			name: "JSON and environment includes retain file includes and explicit precedence",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.include=${FILE_PROFILE:sql}",
				"config/application.properties":             "spring.profiles.include=cloud",
				"src/main/resources/application-sql.properties": "spring.datasource.url=" +
					"jdbc:postgresql://localhost/ignored",
				"src/main/resources/application-local.properties": "spring.datasource.url=" +
					"jdbc:mysql://localhost/warehouse",
			},
			env: map[string]string{
				"SPRING_PROFILES_INCLUDE": "shared,cloud",
				"SPRING_PROFILES_ACTIVE":  "local",
				"SPRING_APPLICATION_JSON": `{"spring":{"profiles":{"include":"json,shared"}}}`,
			},
			drivers: allDrivers(), kind: MySQL, database: "warehouse",
			profiles: []string{"json", "shared", "cloud", "sql", "local"},
		},
		{
			name: "empty higher precedence include does not clear other sources",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.include=sql",
				"src/main/resources/application-sql.properties": "spring.datasource.url=" +
					"jdbc:postgresql://localhost/catalog",
			},
			env:     map[string]string{"SPRING_PROFILES_INCLUDE": ""},
			drivers: allDrivers(), kind: PostgreSQL, database: "catalog", profiles: []string{"sql"},
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
			a, err := Analyze(t.Context(), directory,
				staticResolver{model: effectiveModel(t, directory, tt.drivers)}, envLookup(tt.env))
			if tt.errorText != "" {
				require.ErrorContains(t, err, tt.errorText)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.embedded, a.Embedded != nil)
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
					staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(nil))
				require.NoError(t, err)
				require.Equal(t, []string{"cloud"}, a.Profiles)
				require.Len(t, a.Requirements, 1)
				require.Equal(t, MySQL, a.Requirements[0].Kind)
				require.Equal(t, "warehouse", a.Requirements[0].Name)
			})
		}
	}
}

func TestAnalyzeDatasourceBeanDeclarations(t *testing.T) {
	for _, tt := range []struct {
		name, code string
		reject     bool
	}{
		{name: "injected datasource consumer",
			code: "@Bean JdbcTemplate template(DataSource source) { return new JdbcTemplate(source); }"},
		{name: "qualified injected datasource consumer",
			code: "@Bean JdbcTemplate template(javax.sql.DataSource source) { return new JdbcTemplate(source); }"},
		{name: "injected connection factory consumer",
			code: "@Bean DatabaseClient client(ConnectionFactory factory) { return DatabaseClient.create(factory); }"},
		{name: "annotated consumer", code: `@Bean(name = {"client"})
@DependsOn({"database", "metrics"})
JdbcTemplate template(@Qualifier("primary") DataSource source) { return new JdbcTemplate(source); }`},
		{name: "unrelated datasource reference in body",
			code: "@Bean Object marker() { Class<?> type = DataSource.class; return type; }"},
		{name: "unrelated datasource method after bean", code: `@Bean Object marker() { return null; }
DataSource other() { return externalFactory(); }`},
		{name: "datasource return type", reject: true,
			code: "@Bean public DataSource source() { return externalFactory(); }"},
		{name: "qualified datasource return type", reject: true,
			code: "@Bean public javax.sql.DataSource source() { return externalFactory(); }"},
		{name: "connection factory return type", reject: true,
			code: "@Bean ConnectionFactory factory() { return externalFactory(); }"},
		{name: "qualified bean annotation", reject: true,
			code: "@org.springframework.context.annotation.Bean DataSource source() { return externalFactory(); }"},
		{name: "annotated datasource producer", reject: true, code: `@Bean(name = {"primary", "database"})
@DependsOn({"metrics"})
public DataSource source() { return externalFactory(); }`},
		{name: "consumer followed by datasource producer", reject: true, code: `@Bean
JdbcTemplate template(DataSource source) { return new JdbcTemplate(source); }
@Bean DataSource source() { return externalFactory(); }`},
		{name: "Hikari construction", reject: true, code: "DataSource source = new HikariDataSource();"},
		{name: "qualified Hikari construction", reject: true,
			code: "DataSource source = new com.zaxxer.hikari.HikariDataSource();"},
		{name: "driver manager construction", reject: true,
			code: "DataSource source = new DriverManagerDataSource();"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "pom.xml", "<project/>")
			writeFixture(t, directory, "src/main/java/example/Config.java", tt.code)
			analysis, err := AnalyzeProject(t.Context(), directory,
				staticResolver{model: effectiveModel(t, directory, allDrivers())}, DeploymentInputs{})
			require.NoError(t, err)
			if tt.reject {
				require.Len(t, analysis.Diagnostics, 1)
				require.Contains(t, analysis.Diagnostics[0].Message, "custom datasource construction")
				_, err := analysis.ResolvedApplication()
				require.Error(t, err)
				return
			}
			require.Empty(t, analysis.Decisions)
			require.Empty(t, analysis.Diagnostics)
			application, err := analysis.ResolvedApplication()
			require.NoError(t, err)
			_, err = Generate(t.Context(), application)
			require.NoError(t, err)
		})
	}
}

func TestProfileGroupActivationLimits(t *testing.T) {
	for _, count := range []int{64, 65} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "pom.xml", "<project/>")
			var configuration strings.Builder
			configuration.WriteString("spring.profiles.active=group0\n")
			for i := range count {
				fmt.Fprintf(&configuration, "spring.profiles.group.group%d=group%d\n", i, (i+1)%count)
			}
			writeFixture(t, directory, "src/main/resources/application.properties", configuration.String())
			application, err := Analyze(t.Context(), directory,
				staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(nil))
			if count > 64 {
				require.ErrorContains(t, err, "exceeds 64 profiles")
				require.Nil(t, application)
			} else {
				require.NoError(t, err)
				require.Len(t, application.Profiles, count)
				require.Equal(t, "group0", application.Profiles[0])
				require.Equal(t, "group63", application.Profiles[count-1])
			}
		})
	}
	t.Run("invalid member", func(t *testing.T) {
		directory := t.TempDir()
		writeFixture(t, directory, "pom.xml", "<project/>")
		writeFixture(t, directory, "src/main/resources/application.properties",
			"spring.profiles.active=release\nspring.profiles.group.release=invalid profile")
		application, err := Analyze(t.Context(), directory,
			staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(nil))
		require.ErrorContains(t, err, "profile activation is invalid")
		require.Nil(t, application)
	})
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
			a, err := Analyze(t.Context(), directory,
				staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(nil))
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

func TestGeneratedConfigurationDoesNotReplaceApplicationProfile(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "pom.xml", "<project/>")
	writeFixture(t, directory, "src/main/resources/application.properties", "spring.profiles.active=azd")
	const applicationConfig = "config/application-azd.properties"
	const content = "spring.datasource.url=jdbc:postgresql://localhost/catalog\napp.required-setting=preserved\n"
	writeFixture(t, directory, applicationConfig, content)
	application, err := Analyze(t.Context(), directory,
		staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(nil))
	require.NoError(t, err)
	require.Equal(t, []string{applicationConfig}, application.ExternalConfigurations)
	plan, err := Generate(t.Context(), application)
	require.NoError(t, err)
	docker := string(plan.Files[".azd-spring/Dockerfile"])
	require.Contains(t, docker,
		`["/workspace/config/application-azd.properties","/app/config/application-azd.properties"]`)
	require.Contains(t, docker,
		"COPY --chown=10001:10001 .azd-spring/application-azd.properties /app/.azd-spring/application.properties")
	require.Equal(t, 1, strings.Count(docker, "/app/config/application-azd.properties"))
	require.Equal(t, 1, strings.Count(docker, "/app/.azd-spring/application.properties"))
	require.Contains(t, string(plan.Files["infra/application.bicep"]),
		"{ name: 'SPRING_CONFIG_ADDITIONAL_LOCATION', value: 'file:/app/.azd-spring/application.properties' }")
	require.NotContains(t, string(plan.Files["infra/application.bicep"]),
		"file:/app/config/application-azd.properties")
	require.NoError(t, plan.Write(t.Context(), directory))
	data, err := fs.ReadFile(os.DirFS(directory), applicationConfig)
	require.NoError(t, err)
	require.Equal(t, content, string(data))
}

func TestCredentialNonDisclosure(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "pom.xml", "<project/>")
	resolver := staticResolver{model: effectiveModel(t, directory, allDrivers())}
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
			data := strings.ReplaceAll(string(effectiveModel(t, t.TempDir(), "")), tt.before, tt.after)
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

func TestEffectiveModelCompilerConfiguration(t *testing.T) {
	execution := func(phase, goal, configuration string) string {
		return "<execution><phase>" + phase + "</phase><goals><goal>" + goal + "</goal></goals>" +
			"<configuration>" + configuration + "</configuration></execution>"
	}
	for _, tt := range []struct {
		name, plugin, executions, properties, errorText string
		want                                            int
	}{
		{name: "plugin release", plugin: "<release>21</release>", want: 21},
		{name: "execution release", plugin: "<release>17</release>",
			executions: execution("compile", "compile", "<release>21</release>"), want: 21},
		{name: "execution override", plugin: "<release>21</release>",
			executions: strings.Replace(execution("compile", "compile", "<release>17</release>"),
				"<configuration>", `<configuration combine.self="override">`, 1), want: 17},
		{name: "multiple executions", executions: execution("compile", "compile", "<release>17</release>") +
			execution("test-compile", "testCompile", "<release>21</release>"), want: 21},
		{name: "target", plugin: "<target>21</target>", want: 21},
		{name: "inactive execution",
			executions: execution("verify", "compile", "<release>21</release>"), want: 17},
		{name: "plugin release without Java property", plugin: "<release>21</release>",
			properties: "<java.version>17</java.version>", want: 21},
		{name: "unresolved release", plugin: "<release>${compiler.release}</release>",
			errorText: "resolved Java release"},
		{name: "source exceeds target", plugin: "<source>21</source><target>17</target>",
			errorText: "must not exceed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := string(effectiveModel(t, t.TempDir(), ""))
			if tt.properties != "" {
				data = strings.Replace(data, tt.properties, "", 1)
			}
			compiler := "<plugin><groupId>org.apache.maven.plugins</groupId>" +
				"<artifactId>maven-compiler-plugin</artifactId><configuration>" + tt.plugin +
				"</configuration><executions>" + tt.executions + "</executions></plugin>"
			data = strings.Replace(data, "</plugins>", compiler+"</plugins>", 1)
			_, java, err := parseModel([]byte(data))
			if tt.errorText != "" {
				require.ErrorContains(t, err, tt.errorText)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, java)
		})
	}
}

func TestAnalyzeUsesCompilerPluginReleaseForGeneratedContainer(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "pom.xml", "<project/>")
	data := string(effectiveModel(t, directory, ""))
	compiler := "<plugin><groupId>org.apache.maven.plugins</groupId>" +
		"<artifactId>maven-compiler-plugin</artifactId><configuration><release>21</release></configuration></plugin>"
	data = strings.Replace(data, "</plugins>", compiler+"</plugins>", 1)
	application, err := Analyze(t.Context(), directory, staticResolver{model: []byte(data)}, envLookup(nil))
	require.NoError(t, err)
	require.Equal(t, 21, application.JavaVersion)

	plan, err := Generate(t.Context(), application)
	require.NoError(t, err)
	dockerfile := string(plan.Files[".azd-spring/Dockerfile"])
	require.Contains(t, dockerfile, "maven:3.9-eclipse-temurin-21")
	require.Contains(t, dockerfile, "eclipse-temurin:21-jre")
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
			data := string(effectiveModel(t, t.TempDir(), ""))
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

func TestAnalyzeBuildDirectory(t *testing.T) {
	for _, tt := range []struct {
		name      string
		directory string
		absolute  bool
		omit      bool
		errorText string
	}{
		{name: "default", directory: "target", absolute: true},
		{name: "custom", directory: "build", absolute: true},
		{name: "nested", directory: "artifacts/jars", absolute: true},
		{name: "spaces", directory: "build output", absolute: true},
		{name: "escaped XML", directory: "build & output", absolute: true},
		{name: "missing", omit: true, errorText: "resolved absolute build.directory"},
		{name: "empty", errorText: "resolved absolute build.directory"},
		{name: "unresolved property", directory: "${output.directory}", absolute: true,
			errorText: "resolved absolute build.directory"},
		{name: "relative model value", directory: "build", errorText: "resolved absolute build.directory"},
		{name: "outside context", directory: "../outside", absolute: true, errorText: "application Docker context"},
		{name: "project root", directory: ".", absolute: true, errorText: "application Docker context"},
		{name: "source overlap", directory: "src", absolute: true, errorText: "overlap application inputs"},
		{name: "configuration overlap", directory: "config/build", absolute: true,
			errorText: "overlap application inputs"},
		{name: "generated file overlap", directory: ".azd-spring/build", absolute: true,
			errorText: "overlap application inputs"},
		{name: "infrastructure overlap", directory: "infra", absolute: true,
			errorText: "overlap application inputs"},
		{name: "nested infrastructure overlap", directory: "infra/build", absolute: true,
			errorText: "overlap application inputs"},
		{name: "project file overlap", directory: "azure.yaml", absolute: true,
			errorText: "overlap application inputs"},
		{name: "nested project file overlap", directory: "azure.yaml/build", absolute: true,
			errorText: "overlap application inputs"},
		{name: "Docker wildcard", directory: "build[1]", absolute: true, errorText: "without Docker patterns"},
		{name: "Docker substitution", directory: "$output", absolute: true, errorText: "without Docker patterns"},
		{name: "ignore line break", directory: "build\noutput", absolute: true, errorText: "without Docker patterns"},
		{name: "trailing whitespace", directory: "build ", absolute: true, errorText: "end with whitespace"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "pom.xml", "<project/>")
			resolved := tt.directory
			if tt.absolute {
				resolved = filepath.Join(directory, filepath.FromSlash(resolved))
			}
			var escaped strings.Builder
			require.NoError(t, xml.EscapeText(&escaped, []byte(resolved)))
			element := "<directory>" + escaped.String() + "</directory>"
			if tt.omit {
				element = ""
			}
			data := string(effectiveModel(t, directory, ""))
			start := strings.Index(data, "<directory>")
			end := strings.Index(data, "</directory>") + len("</directory>")
			data = data[:start] + element + data[end:]
			application, err := Analyze(t.Context(), directory, staticResolver{model: []byte(data)}, envLookup(nil))
			var plan *Plan
			if err == nil {
				plan, err = Generate(t.Context(), application)
			}
			if tt.errorText != "" {
				require.ErrorContains(t, err, tt.errorText)
				require.Nil(t, plan)
				entries, err := os.ReadDir(directory)
				require.NoError(t, err)
				require.Len(t, entries, 1, "failed analysis or generation must not write deployment files")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.directory, application.BuildDirectory)
			prefix := "COPY --from=build --chown=10001:10001 "
			var copies [][]string
			for line := range strings.SplitSeq(string(plan.Files[".azd-spring/Dockerfile"]), "\n") {
				if strings.HasPrefix(line, prefix) {
					var paths []string
					require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &paths))
					copies = append(copies, paths)
				}
			}
			require.Equal(t, [][]string{{
				"/workspace/" + tt.directory + "/inventory-api-1.0.jar", "/app/application.jar",
			}}, copies)
			require.Contains(t, string(plan.Files[".azd-spring/Dockerfile.dockerignore"]), "\n/"+tt.directory+"\n")
		})
	}
}

func TestAnalyzeBuildDirectoryWithRelativeProjectPath(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "application")
	writeFixture(t, directory, "pom.xml", "<project/>")
	t.Chdir(parent)
	application, err := Analyze(t.Context(), "application",
		staticResolver{model: effectiveModel(t, directory, "")}, envLookup(nil))
	require.NoError(t, err)
	require.Equal(t, "target", application.BuildDirectory)
}

func TestAnalyzeRepackageOutputDirectory(t *testing.T) {
	for _, tt := range []struct {
		name, plugin, execution, additional, want, errorText string
		override                                             bool
	}{
		{name: "default", want: "target"},
		{name: "plugin relative output", plugin: "<outputDirectory>boot</outputDirectory>", want: "boot"},
		{name: "execution absolute output", execution: "<outputDirectory>@absolute@</outputDirectory>", want: "boot jars"},
		{name: "execution relative output", execution: "<outputDirectory>boot jars</outputDirectory>", want: "boot jars"},
		{name: "inherited output", plugin: "<outputDirectory>boot</outputDirectory>",
			execution: "<skip>false</skip>", want: "boot"},
		{name: "empty output inherits", plugin: "<outputDirectory>boot</outputDirectory>",
			execution: "<outputDirectory/>", want: "boot"},
		{name: "execution overrides plugin", plugin: "<outputDirectory>ignored</outputDirectory>",
			execution: "<outputDirectory>boot</outputDirectory>", want: "boot"},
		{name: "override drops plugin output", plugin: "<outputDirectory>ignored</outputDirectory>",
			override: true, want: "target"},
		{name: "equivalent execution outputs", additional: `<execution><goals><goal>repackage</goal></goals>
<configuration><outputDirectory>@build@</outputDirectory></configuration></execution>`, want: "target"},
		{name: "conflicting execution outputs", additional: `<execution><goals><goal>repackage</goal></goals>
<configuration><outputDirectory>boot</outputDirectory></configuration></execution>`,
			errorText: "different output directories"},
		{name: "inactive output ignored", additional: `<execution><phase>none</phase><goals><goal>repackage</goal></goals>
<configuration><outputDirectory>ignored</outputDirectory></configuration></execution>`, want: "target"},
		{name: "unrelated goal ignored", additional: `<execution><goals><goal>run</goal></goals>
<configuration><outputDirectory>ignored</outputDirectory></configuration></execution>`, want: "target"},
		{name: "unresolved output", execution: "<outputDirectory>${repackage.directory}</outputDirectory>",
			errorText: "outputDirectory must be resolved"},
		{name: "outside context", execution: "<outputDirectory>../outside</outputDirectory>",
			errorText: "application Docker context"},
		{name: "project root", execution: "<outputDirectory>.</outputDirectory>",
			errorText: "application Docker context"},
		{name: "source overlap", execution: "<outputDirectory>src/boot</outputDirectory>",
			errorText: "overlap application inputs"},
		{name: "configuration overlap", execution: "<outputDirectory>config/boot</outputDirectory>",
			errorText: "overlap application inputs"},
		{name: "generated file overlap", execution: "<outputDirectory>.azd-spring/boot</outputDirectory>",
			errorText: "overlap application inputs"},
		{name: "infrastructure overlap", execution: "<outputDirectory>infra</outputDirectory>",
			errorText: "overlap application inputs"},
		{name: "nested infrastructure overlap", execution: "<outputDirectory>infra/boot</outputDirectory>",
			errorText: "overlap application inputs"},
		{name: "project file overlap", execution: "<outputDirectory>azure.yaml</outputDirectory>",
			errorText: "overlap application inputs"},
		{name: "nested project file overlap", execution: "<outputDirectory>azure.yaml/boot</outputDirectory>",
			errorText: "overlap application inputs"},
		{name: "Docker wildcard", execution: "<outputDirectory>boot[1]</outputDirectory>",
			errorText: "without Docker patterns"},
		{name: "ignore line break", execution: "<outputDirectory>boot\noutput</outputDirectory>",
			errorText: "without Docker patterns"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "pom.xml", "<project/>")
			data := string(effectiveModel(t, directory, ""))
			configuration := "<configuration>"
			if tt.override {
				configuration = `<configuration combine.self="override">`
			}
			data = strings.Replace(data, "<executions>",
				"<configuration>"+tt.plugin+"</configuration><executions>", 1)
			data = strings.Replace(data, "</goals></execution>",
				"</goals>"+configuration+tt.execution+"</configuration></execution>"+tt.additional, 1)
			for marker, path := range map[string]string{
				"@absolute@": filepath.Join(directory, "boot jars"),
				"@build@":    filepath.Join(directory, "target"),
			} {
				var escaped strings.Builder
				require.NoError(t, xml.EscapeText(&escaped, []byte(path)))
				data = strings.ReplaceAll(data, marker, escaped.String())
			}
			analysis, err := AnalyzeProject(t.Context(), directory,
				staticResolver{model: []byte(data)}, DeploymentInputs{})
			require.NoError(t, err)
			application, err := analysis.ResolvedApplication()
			var plan *Plan
			if err == nil {
				plan, err = Generate(t.Context(), application)
			}
			if tt.errorText != "" {
				if len(analysis.Diagnostics) != 0 {
					require.Contains(t, analysis.Diagnostics[0].Message, tt.errorText)
				} else {
					require.ErrorContains(t, err, tt.errorText)
				}
				require.Nil(t, plan)
				entries, err := os.ReadDir(directory)
				require.NoError(t, err)
				require.Len(t, entries, 1)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "target", application.BuildDirectory)
			require.Equal(t, tt.want, application.JarDirectory)
			paths, err := json.Marshal([]string{"/workspace/" + tt.want + "/inventory-api-1.0.jar", "/app/application.jar"})
			require.NoError(t, err)
			require.Contains(t, string(plan.Files[".azd-spring/Dockerfile"]),
				"COPY --from=build --chown=10001:10001 "+string(paths))
			ignore := string(plan.Files[".azd-spring/Dockerfile.dockerignore"])
			require.Contains(t, ignore, "\n/target\n")
			require.Contains(t, ignore, "\n/"+tt.want+"\n")
		})
	}
}

func TestAnalyzeHikariCredentialBindings(t *testing.T) {
	const marker = "sensitive-pool-value"
	for _, field := range []string{"username", "password"} {
		key := "spring.datasource.hikari." + field
		for _, tt := range []struct {
			name   string
			files  map[string]string
			env    map[string]string
			reject bool
		}{
			{"properties", map[string]string{"src/main/resources/application.properties": key + "=" + marker}, nil, true},
			{"empty properties", map[string]string{"src/main/resources/application.properties": key + "="}, nil, true},
			{"unresolved properties", map[string]string{
				"src/main/resources/application.properties": key + "=${MISSING_POOL_VALUE}",
			}, nil, true},
			{"YAML", map[string]string{
				"src/main/resources/application.yml": "spring:\n  datasource:\n    hikari:\n      " + field + ": " + marker,
			}, nil, true},
			{"environment", nil, map[string]string{strings.ToUpper(strings.ReplaceAll(key, ".", "_")): marker}, true},
			{"empty environment", nil, map[string]string{strings.ToUpper(strings.ReplaceAll(key, ".", "_")): ""}, true},
			{"JSON", nil, map[string]string{
				"SPRING_APPLICATION_JSON": fmt.Sprintf(`{"spring":{"datasource":{"hikari":{"%s":%q}}}}`, field, marker),
			}, true},
			{"active profile", map[string]string{
				"src/main/resources/application-cloud.properties": key + "=" + marker,
			}, map[string]string{"SPRING_PROFILES_ACTIVE": "cloud"}, true},
			{"inactive profile", map[string]string{
				"src/main/resources/application-local.properties": key + "=" + marker,
			}, map[string]string{"SPRING_PROFILES_ACTIVE": "cloud"}, false},
			{"ordinary pool settings", map[string]string{
				"src/main/resources/application.properties": "spring.datasource.hikari.maximum-pool-size=20\n" +
					"spring.datasource." + field + "=" + marker,
			}, nil, false},
		} {
			t.Run(field+"/"+tt.name, func(t *testing.T) {
				directory := t.TempDir()
				writeFixture(t, directory, "pom.xml", "<project/>")
				for name, content := range tt.files {
					writeFixture(t, directory, name, content)
				}
				env := map[string]string{"SPRING_DATASOURCE_URL": "jdbc:mysql://localhost/warehouse"}
				maps.Copy(env, tt.env)
				application, err := Analyze(t.Context(), directory,
					staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(env))
				if tt.reject {
					require.ErrorContains(t, err, key)
					require.ErrorContains(t, err, "use spring.datasource.username/password")
					require.NotContains(t, err.Error(), marker)
					require.Nil(t, application)
				} else {
					require.NoError(t, err)
					require.Equal(t, MySQL, application.Requirements[0].Kind)
				}
				entries, err := os.ReadDir(directory)
				require.NoError(t, err)
				for _, entry := range entries {
					require.NotContains(t, []string{"azure.yaml", "infra", ".azd-spring"}, entry.Name())
				}
			})
		}
	}
}

func TestDerivedContainerAppNameValidation(t *testing.T) {
	for _, tt := range []struct {
		name, artifact, expected string
	}{
		{"shortest artifact", "a", "a-app"},
		{"normalized artifact", "Inventory.Api", "inventory-api-app"},
		{"ending in a number", "inventory9", "inventory9-app"},
		{"maximum length", strings.Repeat("a", 28), strings.Repeat("a", 28) + "-app"},
		{"too long with suffix", strings.Repeat("a", 29), ""},
		{"starts with a number", "123-api", ""},
		{"consecutive hyphens", "inventory--api", ""},
		{"normalized consecutive hyphens", "inventory__api", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "pom.xml", "<project/>")
			data := strings.Replace(string(effectiveModel(t, directory, "")),
				"<artifactId>inventory-api</artifactId>", "<artifactId>"+tt.artifact+"</artifactId>", 1)
			application, err := Analyze(t.Context(), directory, staticResolver{model: []byte(data)}, envLookup(nil))
			require.NoError(t, err)
			plan, err := Generate(t.Context(), application)
			if tt.expected == "" {
				require.ErrorContains(t, err, "derived Container App name")
				require.Nil(t, plan)
				entries, err := os.ReadDir(directory)
				require.NoError(t, err)
				require.Len(t, entries, 1, "invalid resource names must fail before writing deployment files")
				return
			}
			require.NoError(t, err)
			for _, module := range []string{"main", "application"} {
				var parameters struct {
					Parameters struct {
						ServiceName struct {
							Value string `json:"value"`
						} `json:"serviceName"`
					} `json:"parameters"`
				}
				require.NoError(t, json.Unmarshal(plan.Files["infra/"+module+".parameters.json"], &parameters))
				require.Equal(t, tt.expected, parameters.Parameters.ServiceName.Value)
				require.LessOrEqual(t, len(parameters.Parameters.ServiceName.Value), 32)
			}
		})
	}
}

func TestJDBCTLSVerification(t *testing.T) {
	for _, tt := range []struct {
		name, query, mode, errorText string
		kind                         ResourceKind
	}{
		{"postgres default", "", "require", "", PostgreSQL},
		{"postgres disabled is encrypted", "sslmode=disable", "require", "", PostgreSQL},
		{"postgres certificate verification", "sslmode=verify-ca", "verify-ca", "", PostgreSQL},
		{"postgres identity verification", "sslmode=verify-full&connectTimeout=10", "verify-full", "", PostgreSQL},
		{"postgres invalid mode", "sslmode=invalid", "", "sslmode is not supported", PostgreSQL},
		{"postgres conflicting modes", "sslmode=verify-full&sslmode=require", "", "must be specified once", PostgreSQL},
		{"mysql default", "", "REQUIRED", "", MySQL},
		{"mysql preferred is encrypted", "sslMode=PREFERRED", "REQUIRED", "", MySQL},
		{"mysql certificate verification", "sslMode=VERIFY_CA", "VERIFY_CA", "", MySQL},
		{"mysql identity verification", "sslMode=VERIFY_IDENTITY&connectTimeout=10", "VERIFY_IDENTITY", "", MySQL},
		{"mysql legacy verification", "useSSL=true&verifyServerCertificate=true", "VERIFY_CA", "", MySQL},
		{"mysql legacy yes verification", "requireSSL=true&verifyServerCertificate=yes", "VERIFY_CA", "", MySQL},
		{"mysql legacy no verification", "useSSL=true&verifyServerCertificate=false", "REQUIRED", "", MySQL},
		{"mysql explicit mode wins legacy", "sslMode=VERIFY_IDENTITY&verifyServerCertificate=false",
			"VERIFY_IDENTITY", "", MySQL},
		{"mysql invalid mode", "sslMode=invalid", "", "sslMode is not supported", MySQL},
		{"mysql invalid legacy verification", "verifyServerCertificate=invalid", "",
			"verifyServerCertificate must be true, false, yes, or no", MySQL},
		{"mysql conflicting modes", "sslMode=VERIFY_IDENTITY&sslMode=REQUIRED", "", "must be specified once", MySQL},
		{"mysql conflicting legacy verification", "verifyServerCertificate=true&verifyServerCertificate=false", "",
			"must be specified once", MySQL},
	} {
		t.Run(tt.name, func(t *testing.T) {
			options, err := jdbcOptions(tt.kind, tt.query)
			if tt.errorText != "" {
				require.ErrorContains(t, err, tt.errorText)
				return
			}
			require.NoError(t, err)
			values, err := url.ParseQuery(options)
			require.NoError(t, err)
			if tt.kind == PostgreSQL {
				require.Equal(t, tt.mode, values.Get("sslmode"))
				if strings.HasPrefix(tt.mode, "verify-") {
					require.Equal(t, "/etc/ssl/certs/ca-certificates.crt", values.Get("sslrootcert"))
				}
			} else {
				require.Equal(t, tt.mode, values.Get("sslMode"))
				require.Equal(t, "true", values.Get("useSSL"))
				require.Equal(t, "true", values.Get("requireSSL"))
				verification := "false"
				if tt.mode == "VERIFY_CA" || tt.mode == "VERIFY_IDENTITY" {
					verification = "true"
				}
				require.Equal(t, verification, values.Get("verifyServerCertificate"))
			}
			if strings.Contains(tt.query, "connectTimeout") {
				require.Equal(t, "10", values.Get("connectTimeout"))
			}
		})
	}
}

func TestGeneratePreservesVerifiedPasswordBindings(t *testing.T) {
	for _, tt := range []struct {
		kind ResourceKind
		mode string
	}{
		{PostgreSQL, "sslmode=verify-full"},
		{MySQL, "sslMode=VERIFY_IDENTITY"},
	} {
		t.Run(string(tt.kind), func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "pom.xml", "<project/>")
			writeFixture(t, directory, "src/main/resources/application.properties",
				fmt.Sprintf("spring.datasource.url=jdbc:%s://localhost/catalog?%s", tt.kind, tt.mode))
			application, err := Analyze(t.Context(), directory,
				staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(nil))
			require.NoError(t, err)
			plan, err := Generate(t.Context(), application)
			require.NoError(t, err)
			binding := string(plan.Files[".azd-spring/application-azd.properties"])
			require.Contains(t, binding, tt.mode)
			if tt.kind == PostgreSQL {
				require.Contains(t, binding, "sslrootcert="+url.QueryEscape("/etc/ssl/certs/ca-certificates.crt"))
			}
			require.Contains(t, plan.Files, ".azd-spring/database.ps1")
			require.NotContains(t, plan.Files, ".azd-spring/database.py")
		})
	}
}
