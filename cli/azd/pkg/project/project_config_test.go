package project

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/require"
)

// Tests invalid project configurations.
func TestProjectConfigParse_Invalid(t *testing.T) {
	tests := []struct {
		name          string
		projectConfig string
	}{
		{
			name: "ServiceLanguage",
			projectConfig: heredoc.Doc(`
				name: proj-invalid-lang
				services:
					web:
						language: csharp-go-java++++
						host: appservice
			`),
		},
		{
			name: "ServiceHost",
			projectConfig: heredoc.Doc(`
				name: proj-invalid-host
				services:
					web:
						language: csharp
						host: appservice-containerapp-hybrid-edge-cloud
			`),
		},
		{
			name: "BadVersionConstraints",
			projectConfig: heredoc.Doc(`
				name: proj-bad-version-constraint
				requiredVersions:
					azd: notarange
			`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := Parse(ctx, tt.projectConfig)
			require.Error(t, err)
		})
	}
}

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

	e := environment.NewWithValues("test-env", map[string]string{
		environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
	})

	mockContext := mocks.NewMockContext(context.Background())
	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.Nil(t, err)
	require.NotNil(t, projectConfig)

	require.Equal(t, "test-proj", projectConfig.Name)
	require.Equal(t, "test-proj-template", projectConfig.Metadata.Template)
	require.Equal(t, fmt.Sprintf("rg-%s", e.Name()), projectConfig.ResourceGroupName.MustEnvsubst(e.Getenv))
	require.Equal(t, 2, len(projectConfig.Services))

	for key, svc := range projectConfig.Services {
		require.Equal(t, key, svc.Name)
		require.Equal(t, projectConfig, svc.Project)
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
      buildArgs:
        - 'foo'
        - 'bar'
`

	mockContext := mocks.NewMockContext(context.Background())
	projectConfig, err := Parse(*mockContext.Context, testProj)

	require.NotNil(t, projectConfig)
	require.Nil(t, err)

	service := projectConfig.Services["web"]

	require.Equal(t, "./Dockerfile.dev", service.Docker.Path)
	require.Equal(t, "../", service.Docker.Context)
	require.Equal(t, []osutil.ExpandableString{
		osutil.NewExpandableString("foo"),
		osutil.NewExpandableString("bar"),
	}, service.Docker.BuildArgs)
}

func TestProjectWithExpandableDockerArgs(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{
		"REGISTRY": "myregistry",
		"IMAGE":    "myimage",
		"TAG":      "mytag",
		"KEY1":     "val1",
		"KEY2":     "val2",
	})

	serviceConfig := &ServiceConfig{
		Docker: DockerProjectOptions{
			Registry: osutil.NewExpandableString("${REGISTRY}"),
			Image:    osutil.NewExpandableString("${IMAGE}"),
			Tag:      osutil.NewExpandableString("${TAG}"),
			BuildArgs: []osutil.ExpandableString{
				osutil.NewExpandableString("key1=${KEY1}"),
				osutil.NewExpandableString("key2=${KEY2}"),
			},
		},
	}

	require.Equal(t, env.Getenv("REGISTRY"), serviceConfig.Docker.Registry.MustEnvsubst(env.Getenv))
	require.Equal(t, env.Getenv("IMAGE"), serviceConfig.Docker.Image.MustEnvsubst(env.Getenv))
	require.Equal(t, env.Getenv("TAG"), serviceConfig.Docker.Tag.MustEnvsubst(env.Getenv))
	require.Equal(t, fmt.Sprintf("key1=%s", env.Getenv("KEY1")), serviceConfig.Docker.BuildArgs[0].MustEnvsubst(env.Getenv))
	require.Equal(t, fmt.Sprintf("key2=%s", env.Getenv("KEY2")), serviceConfig.Docker.BuildArgs[1].MustEnvsubst(env.Getenv))
}

func TestProjectConfigAddHandler(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	project := getProjectConfig()
	handlerCalled := false

	handler := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		handlerCalled = true
		return nil
	}

	err := project.AddHandler(ServiceEventDeploy, handler)
	require.Nil(t, err)

	err = project.RaiseEvent(*mockContext.Context, ServiceEventDeploy, ProjectLifecycleEventArgs{Project: project})
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestProjectConfigRemoveHandler(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	project := getProjectConfig()
	handler1Called := false
	handler2Called := false

	handler1 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		handler1Called = true
		return nil
	}

	handler2 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		handler2Called = true
		return nil
	}

	// Only handler 1 was registered
	err := project.AddHandler(ServiceEventDeploy, handler1)
	require.Nil(t, err)

	err = project.RemoveHandler(ServiceEventDeploy, handler1)
	require.Nil(t, err)

	// Handler 2 wasn't registered so should error on remove
	err = project.RemoveHandler(ServiceEventDeploy, handler2)
	require.NotNil(t, err)

	// No events are registered at the time event was raised
	err = project.RaiseEvent(*mockContext.Context, ServiceEventDeploy, ProjectLifecycleEventArgs{Project: project})
	require.Nil(t, err)
	require.False(t, handler1Called)
	require.False(t, handler2Called)
}

func TestProjectConfigWithMultipleEventHandlers(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	project := getProjectConfig()
	handlerCalled1 := false
	handlerCalled2 := false

	handler1 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		require.Equal(t, project, args.Project)
		handlerCalled1 = true
		return nil
	}

	handler2 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		require.Equal(t, project, args.Project)
		handlerCalled2 = true
		return nil
	}

	err := project.AddHandler(ServiceEventDeploy, handler1)
	require.Nil(t, err)
	err = project.AddHandler(ServiceEventDeploy, handler2)
	require.Nil(t, err)

	err = project.RaiseEvent(*mockContext.Context, ServiceEventDeploy, ProjectLifecycleEventArgs{Project: project})
	require.Nil(t, err)
	require.True(t, handlerCalled1)
	require.True(t, handlerCalled2)
}

func TestProjectConfigWithMultipleEvents(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	project := getProjectConfig()

	provisionHandlerCalled := false
	deployHandlerCalled := false

	provisionHandler := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		provisionHandlerCalled = true
		return nil
	}

	deployHandler := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		deployHandlerCalled = true
		return nil
	}

	err := project.AddHandler(ProjectEventProvision, provisionHandler)
	require.Nil(t, err)
	err = project.AddHandler(ProjectEventDeploy, deployHandler)
	require.Nil(t, err)

	err = project.RaiseEvent(*mockContext.Context, ProjectEventProvision, ProjectLifecycleEventArgs{Project: project})
	require.Nil(t, err)

	require.True(t, provisionHandlerCalled)
	require.False(t, deployHandlerCalled)
}

func TestProjectConfigWithEventHandlerErrors(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	project := getProjectConfig()

	handler1 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		return errors.New("sample error 1")
	}

	handler2 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		return errors.New("sample error 2")
	}

	err := project.AddHandler(ProjectEventProvision, handler1)
	require.Nil(t, err)
	err = project.AddHandler(ProjectEventProvision, handler2)
	require.Nil(t, err)

	err = project.RaiseEvent(*mockContext.Context, ProjectEventProvision, ProjectLifecycleEventArgs{Project: project})
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "sample error 1")
	require.Contains(t, err.Error(), "sample error 2")
}

func getProjectConfig() *ProjectConfig {
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
`

	mockContext := mocks.NewMockContext(context.Background())
	projectConfig, _ := Parse(*mockContext.Context, testProj)

	return projectConfig
}

func TestProjectConfigRaiseEventWithoutArgs(t *testing.T) {
	ctx := context.Background()
	project := getProjectConfig()
	handlerCalled := false

	handler := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		handlerCalled = true
		require.Empty(t, args.Args)
		return nil
	}

	err := project.AddHandler(ProjectEventDeploy, handler)
	require.Nil(t, err)

	err = project.RaiseEvent(ctx, ProjectEventDeploy, ProjectLifecycleEventArgs{Project: project})
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestProjectConfigRaiseEventWithArgs(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	project := getProjectConfig()
	handlerCalled := false
	eventArgs := ProjectLifecycleEventArgs{
		Project: project,
		Args:    map[string]any{"foo": "bar"},
	}

	handler := func(ctx context.Context, eventArgs ProjectLifecycleEventArgs) error {
		handlerCalled = true
		require.Equal(t, eventArgs.Args["foo"], "bar")
		return nil
	}

	err := project.AddHandler(ProjectEventDeploy, handler)
	require.Nil(t, err)

	err = project.RaiseEvent(*mockContext.Context, ProjectEventDeploy, eventArgs)
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestExpandableStringsInProjectConfig(t *testing.T) {

	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: ${foo}
services:
  api:
    project: src/api
    language: js
    host: containerapp
    `

	mockContext := mocks.NewMockContext(context.Background())
	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.NoError(t, err)

	env := environment.NewWithValues("", map[string]string{
		"foo": "hello",
		"bar": "goodbye",
	})

	require.Equal(t, "hello", projectConfig.ResourceGroupName.MustEnvsubst(env.Getenv))
}

func TestMinVersion(t *testing.T) {
	savedVersion := internal.Version
	t.Cleanup(func() {
		internal.Version = savedVersion
	})

	const testProjWithMinVersion = `
name: test-proj
requiredVersions:
  azd: ">= 0.6.0-beta.3"
metadata:
  template: test-proj-template
`

	const testProjWithoutVersion = `
name: test-proj
metadata:
  template: test-proj-template
`

	const testProjWithMaxVersion = `
name: test-proj
requiredVersions:
  azd: "<= 0.5.0"
metadata:
  template: test-proj-template
`

	t.Run("noVersion", func(t *testing.T) {
		internal.Version = "0.6.0-beta.3 (commit 0000000000000000000000000000000000000000)"

		_, err := Parse(context.Background(), testProjWithoutVersion)
		require.NoError(t, err)
	})

	t.Run("supportedVersion", func(t *testing.T) {
		// Exact match of minimum version.
		internal.Version = "0.6.0-beta.3 (commit 0000000000000000000000000000000000000000)"

		_, err := Parse(context.Background(), testProjWithMinVersion)
		require.NoError(t, err)

		// Newer version than minimum.
		internal.Version = "0.6.0 (commit 0000000000000000000000000000000000000000)"

		_, err = Parse(context.Background(), testProjWithMinVersion)
		require.NoError(t, err)
	})

	t.Run("unsupportedVersion", func(t *testing.T) {
		internal.Version = "0.6.0-beta.2 (commit 0000000000000000000000000000000000000000)"

		_, err := Parse(context.Background(), testProjWithMinVersion)
		require.Error(t, err)

		_, err = Parse(context.Background(), testProjWithMaxVersion)
		require.Error(t, err)
	})

	t.Run("devVersionAllowsAll", func(t *testing.T) {
		internal.Version = "0.0.0-dev.0 (commit 0000000000000000000000000000000000000000)"

		_, err := Parse(context.Background(), testProjWithMinVersion)
		require.NoError(t, err)

		_, err = Parse(context.Background(), testProjWithoutVersion)
		require.NoError(t, err)
	})
}

func Test_Hooks_Config_Yaml_Marshalling(t *testing.T) {
	t.Run("No hooks", func(t *testing.T) {
		expected := &ProjectConfig{
			Name: "test-proj",
			Services: map[string]*ServiceConfig{
				"api": {
					Host:         ContainerAppTarget,
					Language:     ServiceLanguageTypeScript,
					RelativePath: "src/api",
				},
			},
		}

		yamlBytes, err := yaml.Marshal(expected)
		require.NoError(t, err)
		snapshot.SnapshotT(t, string(yamlBytes))

		actual, err := Parse(context.Background(), string(yamlBytes))
		require.NoError(t, err)
		require.Equal(t, expected.Hooks, actual.Hooks)
	})

	t.Run("Single hooks per event", func(t *testing.T) {
		expected := &ProjectConfig{
			Name: "test-proj",
			Hooks: HooksConfig{
				"postprovision": {
					{
						Shell: ext.ShellTypeBash,
						Run:   "scripts/postprovision.sh",
					},
				},
			},
			Services: map[string]*ServiceConfig{
				"api": {
					Host:         ContainerAppTarget,
					Language:     ServiceLanguageTypeScript,
					RelativePath: "src/api",
					Hooks: HooksConfig{
						"postprovision": {
							{
								Shell: ext.ShellTypeBash,
								Run:   "scripts/postprovision.sh",
							},
						},
					},
				},
			},
		}

		yamlBytes, err := yaml.Marshal(expected)
		require.NoError(t, err)
		snapshot.SnapshotT(t, string(yamlBytes))

		actual, err := Parse(context.Background(), string(yamlBytes))
		require.NoError(t, err)
		require.Equal(t, expected.Hooks, actual.Hooks)
		require.Equal(t, expected.Services["api"].Hooks, actual.Services["api"].Hooks)
	})

	t.Run("Multiple hooks per event", func(t *testing.T) {
		expected := &ProjectConfig{
			Name: "test-proj",
			Hooks: map[string][]*ext.HookConfig{
				"postprovision": {
					{
						Shell: ext.ShellTypeBash,
						Run:   "scripts/postprovision1.sh",
					},
					{
						Shell: ext.ShellTypeBash,
						Run:   "scripts/postprovision2.sh",
					},
				},
			},
			Services: map[string]*ServiceConfig{
				"api": {
					Host:         ContainerAppTarget,
					Language:     ServiceLanguageTypeScript,
					RelativePath: "src/api",
					Hooks: HooksConfig{
						"postprovision": {
							{
								Shell: ext.ShellTypeBash,
								Run:   "scripts/postprovision1.sh",
							},
							{
								Shell: ext.ShellTypeBash,
								Run:   "scripts/postprovision2.sh",
							},
						},
					},
				},
			},
		}

		yamlBytes, err := yaml.Marshal(expected)
		require.NoError(t, err)
		snapshot.SnapshotT(t, string(yamlBytes))

		actual, err := Parse(context.Background(), string(yamlBytes))
		require.NoError(t, err)
		require.Equal(t, expected.Hooks, actual.Hooks)
		require.Equal(t, expected.Services["api"].Hooks, actual.Services["api"].Hooks)
	})
}
