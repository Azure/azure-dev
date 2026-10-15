// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
)

func TestGenerateInfrastructure(t *testing.T) {
	for _, tt := range []struct {
		name string
		kind ResourceKind
		sql  string
	}{
		{"embedded", "", ""},
		{"mysql", MySQL, "Microsoft.DBforMySQL/flexibleServers"},
		{"postgresql", PostgreSQL, "Microsoft.DBforPostgreSQL/flexibleServers"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := &Application{
				Name: "inventory", JavaVersion: 21, JarName: "inventory-1.0.jar", Port: 9090,
				Profiles: []string{"release"}, MavenWrapper: true,
				ExternalConfigurations: []string{"config/application-release.properties"},
			}
			if tt.kind != "" {
				a.Requirements = []Requirement{{
					Kind: tt.kind, Name: "inventory", Authentication: "password",
					JDBCOptions: "connectTimeout=10",
				}}
			}
			plan, err := Generate(t.Context(), a)
			require.NoError(t, err)
			config, err := project.Parse(t.Context(), string(plan.Files["azure.yaml"]))
			require.NoError(t, err)
			require.Contains(t, config.Services, "inventory-app")
			require.Equal(t, project.ServiceLanguageDocker, config.Services["inventory-app"].Language)
			require.Equal(t, "application", config.Services["inventory-app"].Module)
			require.Empty(t, config.Resources)
			var parameters struct {
				Parameters map[string]struct {
					Value any `json:"value"`
				} `json:"parameters"`
			}
			require.NoError(t, json.Unmarshal(plan.Files["infra/main.parameters.json"], &parameters))
			require.Equal(t, float64(9090), parameters.Parameters["port"].Value)
			var deploymentParameters struct {
				Parameters map[string]struct {
					Value any `json:"value"`
				} `json:"parameters"`
			}
			require.NoError(t, json.Unmarshal(plan.Files["infra/application.parameters.json"], &deploymentParameters))
			require.Equal(t, parameters.Parameters["port"], deploymentParameters.Parameters["port"])
			require.Equal(t, parameters.Parameters["appImage"], deploymentParameters.Parameters["appImage"])
			require.Equal(t, parameters.Parameters["databaseEngine"], deploymentParameters.Parameters["engine"])
			require.Equal(t, "${SPRING_CONTAINER_APP_ENVIRONMENT_ID}",
				deploymentParameters.Parameters["environmentId"].Value)
			require.Contains(t, string(plan.Files["infra/main.bicep"]), "output SPRING_CONTAINER_APP_ENVIRONMENT_ID")
			require.Contains(t, string(plan.Files["infra/application.bicep"]), "targetPort: placeholder ? 80 : port")
			var bicep strings.Builder
			for name, content := range plan.Files {
				if strings.HasSuffix(name, ".bicep") {
					bicep.Write(content)
				}
			}
			if tt.kind == "" {
				require.Equal(t, "none", parameters.Parameters["databaseEngine"].Value)
			} else {
				require.Equal(t, string(tt.kind), parameters.Parameters["databaseEngine"].Value)
				require.Equal(t, "inventory", parameters.Parameters["databaseName"].Value)
				require.Contains(t, bicep.String(), tt.sql)
				require.Contains(t, bicep.String(), "secretRef")
				require.Contains(t, string(plan.Files[".azd-spring/application-azd.properties"]), "_PASSWORD}")
				require.NotContains(t, string(plan.Files["azure.yaml"]), "_PASSWORD}")
			}
			require.Contains(t, bicep.String(), "@onlyIfNotExists()")
			require.NotContains(t, bicep.String(), "br/public:")
			require.NotContains(t, bicep.String(), "Microsoft.KeyVault")
			require.Contains(t, string(plan.Files[".azd-spring/database.py"]), "def prepare")
			docker := string(plan.Files[".azd-spring/Dockerfile"])
			require.Contains(t, docker, "eclipse-temurin:21-jre")
			require.Contains(t, docker, "target/inventory-1.0.jar")
			require.Contains(t, docker, "./mvnw")
			require.Contains(t, docker, "/app/config/application-azd.properties")
			require.Contains(t, docker,
				`["/workspace/config/application-release.properties","/app/config/application-release.properties"]`)
			directory := t.TempDir()
			require.NoError(t, plan.Write(t.Context(), directory))
			for name, content := range plan.Files {
				data, err := fs.ReadFile(os.DirFS(directory), name)
				require.NoError(t, err)
				require.Equal(t, content, data)
			}
			require.ErrorContains(t, plan.Write(t.Context(), directory), "already exists")
		})
	}
}

func TestDestinationOwnership(t *testing.T) {
	for _, name := range []string{"azure.yaml", "infra", ".azd-spring", "Dockerfile"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, name, "user-owned")
			require.ErrorContains(t, CheckDestination(directory), "already exists")
			data, err := fs.ReadFile(os.DirFS(directory), name)
			require.NoError(t, err)
			require.Equal(t, "user-owned", string(data))
		})
	}
}

func TestSharedTemplateAuthenticationMatrix(t *testing.T) {
	var infrastructure map[string][]byte
	for _, tt := range []struct {
		name string
		kind ResourceKind
		auth string
	}{
		{"memory", "", "password"},
		{"mysql-password", MySQL, "password"},
		{"postgres-password", PostgreSQL, "password"},
		{"postgres-identity", PostgreSQL, "managedIdentity"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := &Application{
				Name: "catalog", JavaVersion: 17, Port: 8080, JarName: "catalog.jar",
				Profiles: []string{"cloud"}, IdentityExtensions: true,
			}
			if tt.kind != "" {
				a.Requirements = []Requirement{{
					Kind: tt.kind, Name: "catalog", Authentication: tt.auth, JDBCOptions: "sslmode=require",
				}}
			}
			plan, err := Generate(t.Context(), a)
			require.NoError(t, err)
			if infrastructure == nil {
				infrastructure = map[string][]byte{}
				for name, content := range plan.Files {
					if strings.HasSuffix(name, ".bicep") {
						infrastructure[name] = content
					}
				}
			}
			for name, content := range infrastructure {
				require.Equal(t, content, plan.Files[name], "all modes must use exactly the same Bicep")
			}
			config, err := project.Parse(t.Context(), string(plan.Files["azure.yaml"]))
			require.NoError(t, err)
			require.Len(t, config.Services, 1)
			require.Contains(t, config.Services, "catalog-app")
			binding := string(plan.Files[".azd-spring/application-azd.properties"])
			if tt.auth == "managedIdentity" {
				require.Contains(t, binding, "azure.managedIdentityEnabled=true")
				require.Contains(t, binding, "sslmode=verify-full")
				require.Contains(t, binding, "spring.datasource.password=\n")
				require.NotContains(t, binding, "_PASSWORD}")
				require.Contains(t, binding, "spring.sql.init.mode=never")
				require.Contains(t, string(plan.Files["azure.yaml"]), "postprovision:")
			} else {
				require.NotContains(t, binding, "authenticationPlugin")
				require.NotContains(t, string(plan.Files["azure.yaml"]), "postprovision:")
			}
			require.Contains(t, string(plan.Files["infra/main.parameters.json"]), "SERVICE_CATALOG_APP_IMAGE_NAME")
			require.Contains(t, string(plan.Files["infra/application.parameters.json"]), "SERVICE_CATALOG_APP_IMAGE_NAME")
		})
	}
}

func TestTemplateRejectsUnsupportedAuthentication(t *testing.T) {
	for _, tt := range []struct {
		name       string
		kind       ResourceKind
		auth       string
		dependency bool
		errorText  string
	}{
		{"mysql identity", MySQL, "managedIdentity", true, "MySQL managed identity is deferred"},
		{"missing token dependency", PostgreSQL, "managedIdentity", false, "azure-identity-extensions"},
		{"unknown authentication", PostgreSQL, "automatic", true, "authentication must be"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := &Application{
				Name: "catalog", JavaVersion: 17, Port: 8080, JarName: "catalog.jar",
				IdentityExtensions: tt.dependency,
				Requirements:       []Requirement{{Kind: tt.kind, Name: "catalog", Authentication: tt.auth}},
			}
			_, err := Generate(t.Context(), a)
			require.ErrorContains(t, err, tt.errorText)
		})
	}
}

func TestExplicitAuthenticationValidation(t *testing.T) {
	a := &Application{}
	require.ErrorContains(t, SetAuthentication(a, "managedIdentity"), "active external datasource")
	a.Requirements = []Requirement{{Kind: PostgreSQL, Authentication: "managedIdentity"}}
	require.ErrorContains(t, SetAuthentication(a, "password"), "conflicts")
	require.ErrorContains(t, SetAuthentication(a, "automatic"), "--database-auth")
	a.Requirements[0].Authentication = "password"
	require.NoError(t, SetAuthentication(a, "managedIdentity"))
	require.Equal(t, "managedIdentity", a.Requirements[0].Authentication)
}

type cancelDuringWrite struct {
	context.Context
	checks int
}

func (c *cancelDuringWrite) Err() error {
	c.checks++
	if c.checks > 1 {
		return context.Canceled
	}
	return nil
}

func TestWriteRollbackAndConfinement(t *testing.T) {
	directory := t.TempDir()
	plan := &Plan{Files: map[string][]byte{
		".azd-spring/first": []byte("first"), ".azd-spring/second": []byte("second"), "azure.yaml": []byte("project"),
	}}
	ctx := &cancelDuringWrite{Context: t.Context()}
	require.ErrorIs(t, plan.Write(ctx, directory), context.Canceled)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
	plan.Files["../outside"] = []byte("must not escape")
	require.ErrorContains(t, plan.Write(t.Context(), directory), "invalid output path")
	entries, err = os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestWriteRejectsSymlink(t *testing.T) {
	directory, target := t.TempDir(), t.TempDir()
	err := os.Symlink(target, filepath.Join(directory, "infra"))
	if err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	plan := &Plan{Files: map[string][]byte{"infra/main.bicep": []byte("must not escape")}}
	require.ErrorContains(t, plan.Write(t.Context(), directory), "already exists")
	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	require.Empty(t, entries)
}
