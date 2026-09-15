// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestPreviewMatchesProviderLegacyDefinition(t *testing.T) {
	t.Setenv("AGENT_DEFINITION_PATH", "")
	for _, tt := range []struct {
		name         string
		suffix       string
		manifestOnly bool
		clearEnv     bool
		alternate    bool
	}{
		{name: "split-yaml", suffix: "yaml"},
		{name: "split-yml", suffix: "yml"},
		{name: "manifest-only-yaml", suffix: "yaml", manifestOnly: true},
		{name: "manifest-only-yml", suffix: "yml", manifestOnly: true},
		{name: "explicit-environment-clear", suffix: "yaml", clearEnv: true},
		{name: "yaml-wins-over-yml", suffix: "yaml", alternate: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "src")
			require.NoError(t, os.Mkdir(source, 0o700))
			manifest := writePreviewYAML(t, source, "agent.manifest."+tt.suffix, map[string]any{
				"name": "research-agent", "metadata": map[string]any{"tags": []any{"Streaming", "Test"}},
				"template": map[string]any{
					"kind": "hosted",
					"code_configuration": map[string]any{
						"runtime": "python_3_13", "entry_point": "main.py",
					},
					"environment_variables": []any{
						map[string]any{"name": "MANIFEST_ONLY", "value": "inherited"},
						map[string]any{"name": "SHARED", "value": "manifest"},
					},
				},
			})
			selected := manifest
			expectedEnv := map[string]string{"MANIFEST_ONLY": "inherited", "SHARED": "manifest"}
			if !tt.manifestOnly {
				variables := []any{map[string]any{"name": "SHARED", "value": "selected"}}
				expectedEnv["SHARED"] = "selected"
				if tt.clearEnv {
					variables = []any{}
					expectedEnv = map[string]string{}
				}
				selected = writePreviewYAML(t, source, "agent."+tt.suffix, map[string]any{
					"kind": "hosted", "name": "research-agent", "environment_variables": variables,
				})
			}
			if tt.alternate {
				writePreviewYAML(t, source, "agent.yml", map[string]any{
					"kind": "hosted", "name": "lower-priority-agent",
					"environment_variables": []any{map[string]any{"name": "SHARED", "value": "lower-priority"}},
				})
				writePreviewYAML(t, source, "agent.manifest.yml", map[string]any{
					"metadata": map[string]any{"tags": []any{"LowerPriority"}},
				})
			}
			before, err := os.ReadFile(selected)
			require.NoError(t, err)
			service := &azdext.ServiceConfig{Name: "research-agent", Host: foundryAgentHost, RelativePath: "src"}
			provider := &AgentServiceTargetProvider{azdClient: newInitializeTestClient(t, root)}
			require.NoError(t, provider.Initialize(t.Context(), proto.CloneOf(service)))
			require.NoError(t, provider.ensureDeployContext(t.Context()))
			require.Equal(t, selected, provider.agentDefinitionPath)
			definition, hosted, err := provider.loadContainerAgentDefinition()
			require.NoError(t, err)
			require.True(t, hosted)
			prepared, err := prepareDeployRequest(service, definition,
				map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://account.services.ai.azure.com/api/projects/project"}, nil)
			require.NoError(t, err)
			require.Equal(t, `["Streaming","Test"]`, prepared.request.Metadata["tags"])
			require.Equal(t, expectedEnv, prepared.resolvedEnvVars)

			reader := &previewAgentReader{result: deployedPreviewAgent(t, prepared.request)}
			result, err := previewAgentService(t.Context(), AgentServicePreviewOptions{
				Service: service, ProjectRoot: root,
				ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project",
			}, func(string) (standaloneAgentReader, error) { return reader, nil })
			require.NoError(t, err)
			require.Empty(t, result.Changes)
			require.False(t, result.HasChanges)
			require.Equal(t, source, result.SourcePath)
			require.Equal(t, 1, reader.calls)
			after, err := os.ReadFile(selected)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestPreviewMatchesProviderDefinitionOverride(t *testing.T) {
	options := agentServicePreviewFixture(t)
	source := t.TempDir()
	path := writePreviewYAML(t, source, "custom-agent.yaml", map[string]any{
		"kind": "hosted", "name": "override-agent", "description": "Explicit definition",
		"metadata": map[string]any{"tags": []any{"Override"}},
		"code_configuration": map[string]any{
			"runtime": "python_3_13", "entry_point": "main.py",
		},
		"environment_variables": []any{map[string]any{"name": "OVERRIDE_ONLY", "value": "value"}},
	})
	t.Setenv("AGENT_DEFINITION_PATH", path)
	// An explicit override is a single definition, not a convention-based merge.
	require.NoError(t, os.WriteFile(filepath.Join(source, "agent.manifest.yaml"), []byte("invalid: ["), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(source, "override-only.py"), []byte("pass\n"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(options.ProjectRoot, "src", "inline-only.py"), []byte("pass\n"), 0o600,
	))
	original := proto.CloneOf(options.Service)
	provider := &AgentServiceTargetProvider{azdClient: newInitializeTestClient(t, options.ProjectRoot)}
	require.NoError(t, provider.Initialize(t.Context(), proto.CloneOf(options.Service)))
	require.NoError(t, provider.ensureDeployContext(t.Context()))
	definition, hosted, err := provider.loadContainerAgentDefinition()
	require.NoError(t, err)
	require.True(t, hosted)
	require.Equal(t, "override-agent", definition.Name)
	prepared, err := prepareDeployRequest(options.Service, definition,
		map[string]string{"FOUNDRY_PROJECT_ENDPOINT": options.ProjectEndpoint}, nil)
	require.NoError(t, err)
	reader := &previewAgentReader{result: deployedPreviewAgent(t, prepared.request)}

	result, err := previewAgentService(t.Context(), options,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	require.Equal(t, "override-agent", reader.name)
	require.Equal(t, "override-agent", result.Name)
	require.Empty(t, result.Changes)
	require.Equal(t, source, result.SourcePath)
	require.Equal(t, []string{path}, result.Sources)
	require.True(t, proto.Equal(original, options.Service))

	archivePath, _, err := provider.packageCodeDeploy(t.Context(), provider.serviceConfig)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Remove(archivePath)) })
	archive, err := zip.OpenReader(archivePath)
	require.NoError(t, err)
	defer func(archive *zip.ReadCloser) {
		_ = archive.Close()
	}(archive)
	var names []string
	for _, file := range archive.File {
		names = append(names, file.Name)
	}
	require.Contains(t, names, "override-only.py")
	require.NotContains(t, names, "inline-only.py")
}

func TestPreviewRejectsInvalidDefinitionOverrideBeforeRemoteRead(t *testing.T) {
	for _, name := range []string{"missing.yaml", "not-yaml.json", "directory.yaml"} {
		t.Run(name, func(t *testing.T) {
			options := agentServicePreviewFixture(t)
			path := filepath.Join(t.TempDir(), name)
			switch name {
			case "not-yaml.json":
				require.NoError(t, os.WriteFile(path, []byte(`{"kind":"hosted","name":"override"}`), 0o600))
			case "directory.yaml":
				require.NoError(t, os.Mkdir(path, 0o700))
			}
			t.Setenv("AGENT_DEFINITION_PATH", path)
			_, err := previewAgentService(t.Context(), options, func(string) (standaloneAgentReader, error) {
				t.Fatal("invalid explicit overrides must fail before reading a remote agent")
				return nil, nil
			})
			require.Error(t, err)
		})
	}
}

func TestDefinitionOverrideErrorRedactsURLCredentials(t *testing.T) {
	// #nosec G101 -- Synthetic URL credentials test non-disclosure on an invalid file override.
	t.Setenv("AGENT_DEFINITION_PATH",
		"https://override-user:override-password@example.com/agent.yaml?sig=override-token#fragment")
	_, err := agentDefinitionOverridePath()
	require.Error(t, err)
	for _, secret := range []string{"override-user", "override-password", "override-token", "fragment"} {
		require.NotContains(t, err.Error(), secret)
	}
}

func TestPreviewLegacyImageMarkerMatchesProvider(t *testing.T) {
	for _, tt := range []struct {
		name      string
		persisted map[string]string
		process   string
		prebuilt  bool
	}{
		{name: "process-only", process: "true", prebuilt: true},
		{name: "persisted-wins", persisted: map[string]string{"AZD_AGENT_SKIP_ACR": "false"}, process: "true"},
		{name: "persisted-empty-wins", persisted: map[string]string{"AZD_AGENT_SKIP_ACR": ""}, process: "true"},
		{name: "numeric-is-not-true", persisted: map[string]string{"AZD_AGENT_SKIP_ACR": "1"}},
		{name: "letter-is-not-true", persisted: map[string]string{"AZD_AGENT_SKIP_ACR": "t"}},
		{name: "mixed-case", persisted: map[string]string{"AZD_AGENT_SKIP_ACR": "tRuE"}, prebuilt: true},
		{name: "whitespace", persisted: map[string]string{"AZD_AGENT_SKIP_ACR": " true "}, prebuilt: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_AGENT_SKIP_ACR", tt.process)
			service := &azdext.ServiceConfig{Name: "agent", Image: "registry.example.com/agent:v1"}
			environment := &previewContextEnvironmentServer{values: tt.persisted}
			client := newPreviewContextClient(t, &azdext.ProjectConfig{Path: t.TempDir()}, environment)
			provider := &AgentServiceTargetProvider{
				azdClient: client, serviceConfig: service, env: &azdext.Environment{Name: "test"},
			}
			require.Equal(t, tt.prebuilt, provider.shouldSkipACRForEnvironment(t.Context()))
			plan, err := planPreviewImage(agent_yaml.ContainerAgent{Image: service.Image}, service, "project", tt.persisted)
			require.NoError(t, err)
			require.Equal(t, tt.prebuilt, plan.Mode == "prebuilt")
			require.Equal(t, !tt.prebuilt, plan.Build)
			require.Equal(t, !tt.prebuilt, plan.Push)
			require.Zero(t, environment.writes)
		})
	}
}

func (s *previewContextEnvironmentServer) GetValue(
	_ context.Context, req *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	value, found := s.values[req.Key]
	if !found {
		value = os.Getenv(req.Key)
	}
	return &azdext.KeyValueResponse{Value: value}, nil
}
