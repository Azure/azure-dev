// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
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
		{"mysql", MySQL, "db-for-my-sql"},
		{"postgresql", PostgreSQL, "db-for-postgre-sql"},
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
			host := config.Resources["inventory-app"]
			require.NotNil(t, host)
			props, ok := host.Props.(project.ContainerAppProps)
			require.True(t, ok)
			require.Equal(t, 9090, props.Port)
			var bicep strings.Builder
			for name, content := range plan.Files {
				if strings.HasSuffix(name, ".bicep") {
					bicep.Write(content)
				}
			}
			if tt.kind == "" {
				require.Len(t, config.Resources, 1)
				require.NotContains(t, bicep.String(), "db-for-my-sql")
				require.NotContains(t, bicep.String(), "db-for-postgre-sql")
			} else {
				require.Len(t, config.Resources, 2)
				require.Contains(t, bicep.String(), tt.sql)
				require.Contains(t, bicep.String(), "secretRef")
				require.Contains(t, string(plan.Files[".azd-spring/application-azd.properties"]), "_PASSWORD}")
				require.NotContains(t, string(plan.Files["azure.yaml"]), "_PASSWORD}")
			}
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
