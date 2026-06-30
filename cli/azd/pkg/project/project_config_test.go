// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
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
			ctx := t.Context()
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

	mockContext := mocks.NewMockContext(t.Context())
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

	mockContext := mocks.NewMockContext(t.Context())
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
	mockContext := mocks.NewMockContext(t.Context())
	project := getProjectConfig()
	handlerCalled := false

	handler := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		handlerCalled = true
		return nil
	}

	err := project.AddHandler(*mockContext.Context, ServiceEventDeploy, handler)
	require.Nil(t, err)

	err = project.RaiseEvent(*mockContext.Context, ServiceEventDeploy, ProjectLifecycleEventArgs{Project: project})
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestProjectConfigRemoveHandler(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	project := getProjectConfig()
	handler1Called := false

	handler1 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		handler1Called = true
		return nil
	}

	// Register handler with a cancellable context
	ctx, cancel := context.WithCancel(*mockContext.Context)
	err := project.AddHandler(ctx, ServiceEventDeploy, handler1)
	require.Nil(t, err)

	// Cancel context to trigger removal
	cancel()

	require.Eventually(t, func() bool {
		// Handler should not be called after context cancellation
		handler1Called = false
		err = project.RaiseEvent(*mockContext.Context, ServiceEventDeploy, ProjectLifecycleEventArgs{Project: project})
		return err == nil && !handler1Called
	}, time.Second, 10*time.Millisecond, "Handler should not fire after context cancellation")
}

func TestProjectConfigWithMultipleEventHandlers(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
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

	err := project.AddHandler(*mockContext.Context, ServiceEventDeploy, handler1)
	require.Nil(t, err)
	err = project.AddHandler(*mockContext.Context, ServiceEventDeploy, handler2)
	require.Nil(t, err)

	err = project.RaiseEvent(*mockContext.Context, ServiceEventDeploy, ProjectLifecycleEventArgs{Project: project})
	require.Nil(t, err)
	require.True(t, handlerCalled1)
	require.True(t, handlerCalled2)
}

func TestProjectConfigWithMultipleEvents(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
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

	err := project.AddHandler(*mockContext.Context, ProjectEventProvision, provisionHandler)
	require.Nil(t, err)
	err = project.AddHandler(*mockContext.Context, ProjectEventDeploy, deployHandler)
	require.Nil(t, err)

	err = project.RaiseEvent(*mockContext.Context, ProjectEventProvision, ProjectLifecycleEventArgs{Project: project})
	require.Nil(t, err)

	require.True(t, provisionHandlerCalled)
	require.False(t, deployHandlerCalled)
}

func TestProjectConfigWithEventHandlerErrors(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	project := getProjectConfig()

	handler1 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		return errors.New("sample error 1")
	}

	handler2 := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		return errors.New("sample error 2")
	}

	err := project.AddHandler(*mockContext.Context, ProjectEventProvision, handler1)
	require.Nil(t, err)
	err = project.AddHandler(*mockContext.Context, ProjectEventProvision, handler2)
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
	ctx := t.Context()
	project := getProjectConfig()
	handlerCalled := false

	handler := func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		handlerCalled = true
		require.Empty(t, args.Args)
		return nil
	}

	err := project.AddHandler(ctx, ProjectEventDeploy, handler)
	require.Nil(t, err)

	err = project.RaiseEvent(ctx, ProjectEventDeploy, ProjectLifecycleEventArgs{Project: project})
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestProjectConfigRaiseEventWithArgs(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
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

	err := project.AddHandler(*mockContext.Context, ProjectEventDeploy, handler)
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

	mockContext := mocks.NewMockContext(t.Context())
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

		_, err := Parse(t.Context(), testProjWithoutVersion)
		require.NoError(t, err)
	})

	t.Run("supportedVersion", func(t *testing.T) {
		// Exact match of minimum version.
		internal.Version = "0.6.0-beta.3 (commit 0000000000000000000000000000000000000000)"

		_, err := Parse(t.Context(), testProjWithMinVersion)
		require.NoError(t, err)

		// Newer version than minimum.
		internal.Version = "0.6.0 (commit 0000000000000000000000000000000000000000)"

		_, err = Parse(t.Context(), testProjWithMinVersion)
		require.NoError(t, err)
	})

	t.Run("unsupportedVersion", func(t *testing.T) {
		internal.Version = "0.6.0-beta.2 (commit 0000000000000000000000000000000000000000)"

		_, err := Parse(t.Context(), testProjWithMinVersion)
		require.Error(t, err)

		_, err = Parse(t.Context(), testProjWithMaxVersion)
		require.Error(t, err)
	})

	t.Run("devVersionAllowsAll", func(t *testing.T) {
		internal.Version = "0.0.0-dev.0 (commit 0000000000000000000000000000000000000000)"

		_, err := Parse(t.Context(), testProjWithMinVersion)
		require.NoError(t, err)

		_, err = Parse(t.Context(), testProjWithoutVersion)
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

		actual, err := Parse(t.Context(), string(yamlBytes))
		require.NoError(t, err)
		require.Equal(t, expected.Hooks, actual.Hooks)
	})

	t.Run("Single hooks per event", func(t *testing.T) {
		expected := &ProjectConfig{
			Name: "test-proj",
			Hooks: HooksConfig{
				"postprovision": {
					{
						Shell: string(language.HookKindBash),
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
								Shell: string(language.HookKindBash),
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

		actual, err := Parse(t.Context(), string(yamlBytes))
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
						Shell: string(language.HookKindBash),
						Run:   "scripts/postprovision1.sh",
					},
					{
						Shell: string(language.HookKindBash),
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
								Shell: string(language.HookKindBash),
								Run:   "scripts/postprovision1.sh",
							},
							{
								Shell: string(language.HookKindBash),
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

		actual, err := Parse(t.Context(), string(yamlBytes))
		require.NoError(t, err)
		require.Equal(t, expected.Hooks, actual.Hooks)
		require.Equal(t, expected.Services["api"].Hooks, actual.Services["api"].Hooks)
	})
}

func Test_Resources_Marshal_Unmarshal(t *testing.T) {
	const doc = `
name: test-proj
resources:
  api:
    type: host.containerapp
    port: 8080
    env:
    - name: FOO
      value: BAR
`

	prj := ProjectConfig{}
	err := yaml.Unmarshal([]byte(doc), &prj)
	require.NoError(t, err)

	marshaled, err := yaml.Marshal(prj)
	require.NoError(t, err)
	assert.YAMLEq(t, doc, string(marshaled))

	roundTripped := ProjectConfig{}
	err = yaml.Unmarshal(marshaled, &roundTripped)
	require.NoError(t, err)

	cap, ok := roundTripped.Resources["api"].Props.(ContainerAppProps)
	require.True(t, ok)
	require.Equal(t, 8080, cap.Port)
	require.Equal(t, "FOO", cap.Env[0].Name)
	require.Equal(t, "BAR", cap.Env[0].Value)
}

func TestProjectConfigLayerProviderInheritance(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	t.Run("root provider propagates to layers without explicit provider", func(t *testing.T) {
		const proj = `
name: test-proj
infra:
  provider: terraform
  layers:
    - name: backend
      path: ./infra/backend
    - name: resources
      path: ./infra/resources
`
		cfg, err := Parse(*mockContext.Context, proj)
		require.NoError(t, err)
		for _, layer := range cfg.Infra.Layers {
			require.Equal(t, "terraform", string(layer.Provider),
				"layer %q should inherit root provider", layer.Name)
		}
	})

	t.Run("per-layer provider overrides root", func(t *testing.T) {
		const proj = `
name: test-proj
infra:
  provider: terraform
  layers:
    - name: backend
      path: ./infra/backend
      provider: bicep
    - name: resources
      path: ./infra/resources
`
		cfg, err := Parse(*mockContext.Context, proj)
		require.NoError(t, err)
		require.Equal(t, "bicep", string(cfg.Infra.Layers[0].Provider),
			"layer with explicit provider should keep it")
		require.Equal(t, "terraform", string(cfg.Infra.Layers[1].Provider),
			"layer without explicit provider should inherit root")
	})
}

func Test_HooksConfig_UnmarshalYAML_LegacySingle(t *testing.T) {
	yamlData := `
preprovision:
  run: echo hello
  shell: sh
postprovision:
  run: echo bye
  shell: sh
`
	var hooks HooksConfig
	err := yaml.Unmarshal([]byte(yamlData), &hooks)
	require.NoError(t, err)

	require.Contains(t, hooks, "preprovision")
	require.Len(t, hooks["preprovision"], 1)
	assert.Equal(t, "echo hello", hooks["preprovision"][0].Run)

	require.Contains(t, hooks, "postprovision")
	require.Len(t, hooks["postprovision"], 1)
	assert.Equal(t, "echo bye", hooks["postprovision"][0].Run)
}

func Test_HooksConfig_UnmarshalYAML_NewMultiple(t *testing.T) {
	yamlData := `
preprovision:
  - run: echo step1
    shell: sh
  - run: echo step2
    shell: sh
`
	var hooks HooksConfig
	err := yaml.Unmarshal([]byte(yamlData), &hooks)
	require.NoError(t, err)

	require.Contains(t, hooks, "preprovision")
	require.Len(t, hooks["preprovision"], 2)
	assert.Equal(t, "echo step1", hooks["preprovision"][0].Run)
	assert.Equal(t, "echo step2", hooks["preprovision"][1].Run)
}

func Test_HooksConfig_MarshalYAML_Empty(t *testing.T) {
	hooks := HooksConfig{}
	result, err := hooks.MarshalYAML()
	require.NoError(t, err)
	assert.Nil(t, result)
}

func Test_HooksConfig_MarshalYAML_SingleHook(t *testing.T) {
	hooks := HooksConfig{
		"preprovision": {
			{Run: "echo hello", Shell: string(language.HookKindBash)},
		},
	}
	result, err := hooks.MarshalYAML()
	require.NoError(t, err)
	require.NotNil(t, result)

	// Single hook should be marshaled directly (not as array)
	m := result.(map[string]any)
	_, isHookConfig := m["preprovision"].(*ext.HookConfig)
	assert.True(t, isHookConfig, "single hook should be marshaled as HookConfig, not slice")
}

func Test_HooksConfig_MarshalYAML_MultipleHooks(t *testing.T) {
	hooks := HooksConfig{
		"preprovision": {
			{Run: "echo step1", Shell: string(language.HookKindBash)},
			{Run: "echo step2", Shell: string(language.HookKindBash)},
		},
	}
	result, err := hooks.MarshalYAML()
	require.NoError(t, err)
	require.NotNil(t, result)

	// Multiple hooks should be marshaled as slice
	m := result.(map[string]any)
	_, isSlice := m["preprovision"].([]*ext.HookConfig)
	assert.True(t, isSlice, "multiple hooks should be marshaled as slice")
}

func Test_HooksConfig_RoundTrip(t *testing.T) {
	// Test round trip with all single hooks (marshals as map[string]*HookConfig, legacy unmarshal works)
	original := HooksConfig{
		"preprovision": {
			{Run: "echo hello", Shell: string(language.HookKindBash)},
		},
		"postprovision": {
			{Run: "echo bye", Shell: string(language.HookKindBash)},
		},
	}

	data, err := yaml.Marshal(original)
	require.NoError(t, err)

	var restored HooksConfig
	err = yaml.Unmarshal(data, &restored)
	require.NoError(t, err)

	require.Contains(t, restored, "preprovision")
	require.Len(t, restored["preprovision"], 1)
	assert.Equal(t, "echo hello", restored["preprovision"][0].Run)

	require.Contains(t, restored, "postprovision")
	require.Len(t, restored["postprovision"], 1)
	assert.Equal(t, "echo bye", restored["postprovision"][0].Run)
}

func TestServiceConfig_MarshalYAML_OmitsEmptyOptionalFields(t *testing.T) {
	svc := &ServiceConfig{
		Host: ContainerAppTarget,
	}

	data, err := yaml.Marshal(svc)
	require.NoError(t, err)
	output := string(data)

	// host is required and must always be present
	assert.Contains(t, output, "host: containerapp")

	// optional fields with zero values should be omitted
	assert.NotContains(t, output, "project:")
	assert.NotContains(t, output, "language:")
}

func TestPipelineOptions_MarshalYAML_OmitsEmptyFields(t *testing.T) {
	tests := []struct {
		name    string
		options PipelineOptions
	}{
		{"all zero values", PipelineOptions{}},
		{"empty slices", PipelineOptions{Variables: []string{}, Secrets: []string{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := yaml.Marshal(tt.options)
			require.NoError(t, err)
			output := string(data)

			assert.NotContains(t, output, "provider:")
			assert.NotContains(t, output, "variables:")
			assert.NotContains(t, output, "secrets:")
		})
	}
}
