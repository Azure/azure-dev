// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
			project, service := automaticPostgresProject(t)
			directory := filepath.Join(project.Path, "src", "main", "java")
			require.NoError(t, os.MkdirAll(directory, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(directory, "Config.java"), []byte(source), 0o600))
			_, err := new(Runtime).readServiceDatasource(t.Context(), project, service, nil, false)
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

func TestExplicitPasswordMatchesEffectiveApplicationWithoutDisclosure(t *testing.T) {
	project, service := automaticPostgresProject(t)
	properties := "spring.datasource.url=jdbc:postgresql://actual.example/petclinic?" +
		"sslmode=verify-full&sslrootcert=/etc/ssl/certs/ca-certificates.crt\n" +
		"spring.datasource.username=petclinic_app\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(project.Path, "src", "main", "resources", "application.properties"), []byte(properties), 0o600,
	))
	password := strings.Repeat("fixture-", 8)
	variable := &armappcontainers.EnvironmentVar{Name: new("SPRING_DATASOURCE_PASSWORD"), Value: new(password)}
	binding := &deploymentBinding{
		values: map[string]string{postgresHostEnv: "actual.example"},
		app: &armappcontainers.ContainerApp{Properties: &armappcontainers.ContainerAppProperties{
			Template: &armappcontainers.Template{Containers: []*armappcontainers.Container{{
				Env: []*armappcontainers.EnvironmentVar{variable},
			}}},
		}},
	}
	ctx := context.WithValue(t.Context(), deploymentBindingKey{}, binding)
	expected := resolvedDatabase{
		Type: databasePostgreSQL, Authentication: databaseAuthenticationPassword,
		Name: "petclinic", Username: "petclinic_app",
	}
	values := map[string]string{databaseAppPasswordEnv: password}
	runtime := new(Runtime)
	require.NoError(t, runtime.verifyExplicitDatasource(ctx, project, service, nil, expected, values))
	variable.Value = new(password + "different")
	err := runtime.verifyExplicitDatasource(ctx, project, service, nil, expected, values)
	require.Error(t, err)
	require.NotContains(t, err.Error(), password)
	require.NotContains(t, err.Error(), *variable.Value)
}

func TestPendingDatasourceDoesNotMaskKnownUnsupportedConstruction(t *testing.T) {
	for _, invalid := range []string{
		"spring.datasource.hikari.jndi-name=java:comp/env/database",
		"spring.datasource.type=custom.DataSource",
		"spring.autoconfigure.exclude=org.springframework.boot.jdbc.autoconfigure.DataSourceAutoConfiguration",
	} {
		t.Run(invalid, func(t *testing.T) {
			project, service := automaticPostgresProject(t)
			properties := "spring.datasource.url=${UNRESOLVED_URL}\n" + invalid + "\n"
			require.NoError(t, os.WriteFile(
				filepath.Join(project.Path, "src", "main", "resources", "application.properties"),
				[]byte(properties), 0o600,
			))
			_, err := new(Runtime).readServiceDatasource(t.Context(), project, service, nil, false)
			require.Error(t, err)
		})
	}
}

func TestAuthoredDiscoveryRejectedBeforeProjectOrCoreAccess(t *testing.T) {
	service := serviceWithConfig(t, map[string]any{})
	service.Docker = &azdext.DockerProjectOptions{Path: "Dockerfile"}
	_, err := new(Runtime).resolveService(t.Context(), nil, service, nil, nil, false)
	require.ErrorContains(t, err, "native Maven")
	_, err = new(serviceTarget).Package(t.Context(), service, nil, nil)
	require.ErrorContains(t, err, "native Maven")
}
