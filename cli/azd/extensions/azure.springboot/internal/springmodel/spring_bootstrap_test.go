// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package springmodel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpringBootstrapRejectsRecognizableCustomConfiguration(t *testing.T) {
	for _, source := range []string{
		`@PropertySource("file:custom.properties") class Config {}`,
		`@Bean public javax.sql.DataSource database() { return factory(); }`,
		`@Bean(name = "database") public CustomDataSource database() { return factory(); }`,
		`class Config { Object database = new com.zaxxer.hikari.HikariDataSource(); }`,
		`class Config implements org.springframework.boot.env.EnvironmentPostProcessor {}`,
		`class Config { void setup() { environment.getPropertySources().addFirst(properties); } }`,
		`class Config { void setup() { app.setDefaultProperties(properties); } }`,
	} {
		t.Run(source, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			directory := filepath.Join(root, "src", "main", "java")
			require.NoError(t, os.MkdirAll(directory, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(directory, "Config.java"), []byte(source), 0o600))
			err = ValidateStandardBootstrap(t.Context(), root)
			require.ErrorContains(t, err, "runtime configuration evidence")
		})
	}
}

func TestJavaBootstrapIgnoresCommentsAndLiterals(t *testing.T) {
	source := `// @PropertySource("not active")
/* @Bean DataSource database() {} */
class App {
    String example = "@Bean DataSource database() {}";
    String block = """
        new HikariDataSource();
        """;
    @Bean public HealthIndicator health() { return indicator(); }
}`
	code, err := javaBootstrapCode([]byte(source))
	require.NoError(t, err)
	for _, pattern := range unsupportedSpringBootstrap {
		require.False(t, pattern.Match(code), "%s", pattern)
	}
}

func TestPendingDatasourceDoesNotMaskKnownUnsupportedConstruction(t *testing.T) {
	for _, invalid := range []string{
		"spring.datasource.hikari.jndi-name=java:comp/env/database",
		"spring.datasource.type=custom.DataSource",
		"spring.autoconfigure.exclude=org.springframework.boot.jdbc.autoconfigure.DataSourceAutoConfiguration",
	} {
		t.Run(invalid, func(t *testing.T) {
			root := t.TempDir()
			resDir := filepath.Join(root, "src", "main", "resources")
			require.NoError(t, os.MkdirAll(resDir, 0o700))
			properties := "spring.datasource.url=${UNRESOLVED_URL}\n" + invalid + "\n"
			require.NoError(t, os.WriteFile(
				filepath.Join(resDir, "application.properties"),
				[]byte(properties), 0o600,
			))
			cfg, err := LoadConfiguration(t.Context(), ConfigInput{
				Root: root, Deployed: true, Environment: map[string]string{}, AllowPending: true,
			})
			require.NoError(t, err)
			_, err = InferDatasource(cfg, Dependencies{PostgreSQL: true, AzureIdentity: true}, false)
			require.Error(t, err)
		})
	}
}
