// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestProjectMutationsPreserveUntouchedRawConfiguration(t *testing.T) {
	for _, operation := range []string{
		"add", "replace", "project value", "project section", "project unset",
		"service value", "service section", "service unset", "service replacement",
	} {
		t.Run(operation, func(t *testing.T) {
			_, service, path := newBetaProjectServiceOverrideFixture(t)
			manifest := "name: test\ninfra: {}\ncustom:\n  number: 9007199254740993\n  empty: {}\n" +
				"services:\n  isolated.api:\n    host: azure.ai.connection\n    env: {}\n    infra: {}\n" +
				"    config:\n      number: 9007199254740993\n  managed.api:\n    host: azure.ai.connection\n" +
				"    target: old\n    env:\n      KEY: ${API_KEY}\n    config:\n      number: 9007199254740993\n"
			require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600))
			typed, err := project.Load(t.Context(), path)
			require.NoError(t, err)
			service.lazyProjectConfig.SetValue(typed)
			dispatcher := typed.Services["managed.api"].EventDispatcher
			expected, err := project.LoadConfig(t.Context(), path)
			require.NoError(t, err)
			services, ok := expected.Raw()["services"].(map[string]any)
			require.True(t, ok)
			managed, ok := services["managed.api"].(map[string]any)
			require.True(t, ok)
			section := &structpb.Struct{Fields: map[string]*structpb.Value{}}
			switch operation {
			case "add", "replace":
				name := "new.api"
				if operation == "replace" {
					name = "managed.api"
				}
				_, err = service.AddService(t.Context(), &azdext.AddServiceRequest{
					Service: &azdext.ServiceConfig{
						Name: name, Host: "azure.ai.connection",
						RelativePath: filepath.Join("src", "api"),
						OutputPath:   filepath.Join("dist", "api"),
						Environment:  map[string]string{"KEY": "${NEW_KEY}"},
					},
				})
				services[name] = map[string]any{
					"host": "azure.ai.connection", "env": map[string]any{"KEY": "$${NEW_KEY}"},
					"project": "src/api", "dist": "dist/api",
				}
			case "project value":
				_, err = service.SetConfigValue(t.Context(), &azdext.SetProjectConfigValueRequest{
					Path: "metadata.template", Value: structpb.NewStringValue("test"),
				})
				expected.Raw()["metadata"] = map[string]any{"template": "test"}
			case "project section":
				_, err = service.SetConfigSection(t.Context(), &azdext.SetProjectConfigSectionRequest{
					Path: "metadata", Section: section,
				})
				expected.Raw()["metadata"] = map[string]any{}
			case "project unset":
				_, err = service.UnsetConfig(t.Context(), &azdext.UnsetProjectConfigRequest{Path: "custom.empty"})
				require.NoError(t, expected.Unset("custom.empty"))
			case "service value":
				_, err = service.SetServiceConfigValue(t.Context(), &azdext.SetServiceConfigValueRequest{
					ServiceName: "managed.api", Path: "env.KEY", Value: structpb.NewStringValue("${NEW_KEY}"),
				})
				managed["env"] = map[string]any{"KEY": "${NEW_KEY}"}
			case "service section":
				_, err = service.SetServiceConfigSection(t.Context(), &azdext.SetServiceConfigSectionRequest{
					ServiceName: "managed.api", Path: "env", Section: section,
				})
				managed["env"] = map[string]any{}
			case "service unset":
				_, err = service.UnsetServiceConfig(t.Context(), &azdext.UnsetServiceConfigRequest{
					ServiceName: "managed.api", Path: "target",
				})
				delete(managed, "target")
			case "service replacement":
				section, err = structpb.NewStruct(map[string]any{"host": "azure.ai.connection", "env": map[string]any{}})
				require.NoError(t, err)
				_, err = service.SetServiceConfigSection(t.Context(), &azdext.SetServiceConfigSectionRequest{
					ServiceName: "managed.api", Section: section,
				})
				services["managed.api"] = map[string]any{"host": "azure.ai.connection", "env": map[string]any{}}
			}
			require.NoError(t, err)
			actual, err := project.LoadConfig(t.Context(), path)
			require.NoError(t, err)
			assert.Equal(t, expected.Raw(), actual.Raw())
			cached, err := service.lazyProjectConfig.GetValue()
			require.NoError(t, err)
			assert.Same(t, dispatcher, cached.Services["managed.api"].EventDispatcher)
			assert.Same(t, typed.EventDispatcher, cached.EventDispatcher)
			assert.Equal(t, cached, cached.Services["managed.api"].Project)
			saved, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(saved), "9007199254740993")
			assert.NotContains(t, string(saved), "9007199254740992")
			assert.Contains(t, string(saved), "# yaml-language-server:")
		})
	}
}

func TestProjectAddServiceInvalidCandidateRestoresRawAndCache(t *testing.T) {
	_, service, path := newBetaProjectServiceOverrideFixture(t)
	service.mapService = func(*azdext.ServiceConfig) (*project.ServiceConfig, error) {
		return &project.ServiceConfig{
			Host:  "azure.ai.connection",
			Hooks: project.HooksConfig{"predeploy": {nil}},
		}, nil
	}
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	_, err = service.AddService(t.Context(), &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{Name: "invalid.api", Host: "azure.ai.connection"},
	})
	require.Error(t, err)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	cached, err := service.lazyProjectConfig.GetValue()
	require.NoError(t, err)
	assert.NotContains(t, cached.Services, "invalid.api")
	require.True(t, service.configMutationMu.TryLock())
	service.configMutationMu.Unlock()
}

func TestProjectAddServiceRejectsInterveningInvalidFile(t *testing.T) {
	_, service, path := newBetaProjectServiceOverrideFixture(t)
	manifest := "name: test\nlayers:\n  - name: application\n    services:\n" +
		"      existing.api:\n        host: azure.ai.connection\n"
	mapService := service.mapService
	service.mapService = func(input *azdext.ServiceConfig) (*project.ServiceConfig, error) {
		require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600))
		return mapService(input)
	}
	_, err := service.AddService(t.Context(), &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{Name: "new.api", Host: "azure.ai.connection"},
	})
	assert.ErrorContains(t, err, "'layers' cannot be combined")
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, manifest, string(saved))
	cached, err := service.lazyProjectConfig.GetValue()
	require.NoError(t, err)
	assert.NotContains(t, cached.Services, "new.api")
	require.True(t, service.configMutationMu.TryLock())
	service.configMutationMu.Unlock()
}

func TestServerE2E_ExistingServiceRPCsPreserveRawConfiguration(t *testing.T) {
	server, service := newProjectE2EServer(t)
	client, ctx := dialProjectSDK(t, server)
	projectClient := client.Project()
	azdContext, err := service.lazyAzdContext.GetValue()
	require.NoError(t, err)
	path := azdContext.ProjectPath()
	manifest := "name: test\ninfra: {}\nservices:\n  isolated.api:\n" +
		"    host: azure.ai.connection\n    env: {}\n    config:\n      number: 9007199254740993\n"
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600))
	results := make(chan error, 2)
	for _, name := range []string{"first.api", "second.api"} {
		go func() {
			_, err := projectClient.AddService(ctx, &azdext.AddServiceRequest{
				Service: &azdext.ServiceConfig{Name: name, Host: "azure.ai.connection"},
			})
			results <- err
		}()
	}
	for range 2 {
		require.NoError(t, <-results)
	}
	for _, fields := range []map[string]any{{"KEY": "${API_KEY}"}, {}} {
		section, err := structpb.NewStruct(fields)
		require.NoError(t, err)
		_, err = projectClient.SetServiceConfigSection(ctx, &azdext.SetServiceConfigSectionRequest{
			ServiceName: "first.api", Path: "env", Section: section,
		})
		require.NoError(t, err)
		raw, err := project.LoadConfig(t.Context(), path)
		require.NoError(t, err)
		services, ok := raw.Raw()["services"].(map[string]any)
		require.True(t, ok)
		first, ok := services["first.api"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, fields, first["env"])
	}
	raw, err := project.LoadConfig(t.Context(), path)
	require.NoError(t, err)
	services, ok := raw.Raw()["services"].(map[string]any)
	require.True(t, ok)
	assert.Len(t, services, 3)
	isolated, ok := services["isolated.api"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{}, isolated["env"])
	config, ok := isolated["config"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, int(9007199254740993), config["number"])
	assert.Equal(t, map[string]any{}, raw.Raw()["infra"])
	cached, err := service.lazyProjectConfig.GetValue()
	require.NoError(t, err)
	assert.Len(t, cached.Services, 3)
}
