package project

import (
	"context"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/test/helpers"
	"github.com/stretchr/testify/require"
)

func TestProjectConfigDefaults(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test-env
services:
  web:
    project: src/web
    language: js
    host: appservice
  api:
    project: src/api
    language: js
    host: appservice
`

	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("test-env")

	projectConfig, err := ParseProjectConfig(testProj, &e)
	require.Nil(t, err)
	require.NotNil(t, projectConfig)

	require.Equal(t, "test-proj", projectConfig.Name)
	require.Equal(t, "test-proj-template", projectConfig.Metadata.Template)
	require.Equal(t, fmt.Sprintf("rg-%s", e.GetEnvName()), projectConfig.ResourceGroupName)
	require.Equal(t, 2, len(projectConfig.Services))

	for key, svc := range projectConfig.Services {
		require.Equal(t, key, svc.Module)
		require.Equal(t, key, svc.Name)
		require.Equal(t, projectConfig, svc.Project)
	}
}

func TestProjectConfigHasService(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  web:
    project: src/web
    language: js
    host: appservice
  api:
    project: src/api
    language: js
    host: appservice
`

	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("test-env")

	projectConfig, err := ParseProjectConfig(testProj, &e)
	require.Nil(t, err)

	require.True(t, projectConfig.HasService("web"))
	require.True(t, projectConfig.HasService("api"))
	require.False(t, projectConfig.HasService("foobar"))
}

func TestProjectConfigGetProject(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  web:
    project: src/web
    language: js
    host: appservice
  api:
    project: src/api
    language: js
    host: appservice
`

	ctx := helpers.CreateTestContext(context.Background(), gblCmdOptions, azCli, mockHttpClient)
	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("test-env")

	projectConfig, err := ParseProjectConfig(testProj, &e)
	require.Nil(t, err)

	project, err := projectConfig.GetProject(ctx, &e)
	require.Nil(t, err)
	require.NotNil(t, project)

	require.Same(t, projectConfig, project.Config)

	for _, svc := range project.Services {
		require.Same(t, project, svc.Project)
		require.NotNil(t, svc.Config)
		require.NotNil(t, svc.Framework)
		require.NotNil(t, svc.Target)
		require.NotNil(t, svc.Scope)
	}
}

func TestProjectWithCustomDockerOptions(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  web:
    project: src/web
    language: js
    host: containerapp
    docker:
      path: ./Dockerfile.dev
      context: ../
`

	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("test-env")

	projectConfig, err := ParseProjectConfig(testProj, &e)

	require.NotNil(t, projectConfig)
	require.Nil(t, err)

	service := projectConfig.Services["web"]

	require.Equal(t, "./Dockerfile.dev", service.Docker.Path)
	require.Equal(t, "../", service.Docker.Context)
}

func TestProjectWithCustomModule(t *testing.T) {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  api:
    project: src/api
    language: js
    host: containerapp
    module: ./api/api
`

	e := environment.Environment{Values: make(map[string]string)}
	e.SetEnvName("test-env")

	projectConfig, err := ParseProjectConfig(testProj, &e)

	require.NotNil(t, projectConfig)
	require.Nil(t, err)

	service := projectConfig.Services["api"]

	require.Equal(t, "./api/api", service.Module)
}
