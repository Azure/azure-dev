// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"maps"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestPreviewAndDeployResolveSameProjectEndpoint(t *testing.T) {
	const shellEndpoint = "https://shell.services.ai.azure.com/api/projects/shell"
	const storedEndpoint = "https://stored.services.ai.azure.com/api/projects/stored"
	const dependencyEndpoint = "https://dependency.services.ai.azure.com/api/projects/dependency"
	for _, tt := range []struct {
		name       string
		values     map[string]string
		process    string
		dependency bool
		expected   string
	}{
		{name: "process-only", process: shellEndpoint, expected: shellEndpoint},
		{name: "persisted-wins", values: map[string]string{"FOUNDRY_PROJECT_ENDPOINT": storedEndpoint},
			process: shellEndpoint, expected: storedEndpoint},
		{name: "persisted-empty-wins", values: map[string]string{"FOUNDRY_PROJECT_ENDPOINT": ""},
			process: shellEndpoint},
		{name: "missing"},
		{name: "dependency-wins", values: map[string]string{"FOUNDRY_PROJECT_ENDPOINT": storedEndpoint},
			process: shellEndpoint, dependency: true, expected: dependencyEndpoint},
		{name: "normalization", process: "https://SHELL.services.ai.azure.com/api/projects/shell/", expected: shellEndpoint},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("FOUNDRY_PROJECT_ENDPOINT", tt.process)
			options := agentServicePreviewFixture(t)
			config := &azdext.ProjectConfig{
				Path: options.ProjectRoot, Services: map[string]*azdext.ServiceConfig{options.Service.Name: options.Service},
			}
			if tt.dependency {
				options.Service.Uses = []string{"project"}
				config.Services["project"] = &azdext.ServiceConfig{
					Name: "project", Host: "azure.ai.project",
					AdditionalProperties: mustStruct(t, map[string]any{"endpoint": dependencyEndpoint}),
				}
			}
			originalConfig := proto.CloneOf(config)
			environment := &previewContextEnvironmentServer{values: maps.Clone(tt.values)}
			client := newPreviewContextClient(t, config, environment)
			provider := &AgentServiceTargetProvider{
				azdClient: client, env: &azdext.Environment{Name: "test"},
				projectPath: config.Path, projectServices: config.Services,
			}
			values, deployErr := provider.loadDeploymentEnvironment(t.Context(), options.Service)
			preview, previewErr := loadServicePreviewOptions(t.Context(), client, options.Service)
			if tt.expected == "" {
				require.ErrorContains(t, deployErr, "FOUNDRY_PROJECT_ENDPOINT is required")
				require.ErrorContains(t, previewErr, "FOUNDRY_PROJECT_ENDPOINT is required")
			} else {
				require.NoError(t, deployErr)
				require.NoError(t, previewErr)
				require.Equal(t, tt.expected, values["FOUNDRY_PROJECT_ENDPOINT"])
				require.Equal(t, tt.expected, preview.ProjectEndpoint)
				definition, hosted, _, err := LoadAgentDefinition(options.Service, options.ProjectRoot)
				require.NoError(t, err)
				require.True(t, hosted)
				_, err = prepareDeployRequest(options.Service, definition, values, nil)
				require.NoError(t, err, "the actual deployment request must accept the previewed endpoint")
			}
			require.Equal(t, tt.values, environment.values)
			require.Zero(t, environment.writes, "endpoint fallback must not be persisted")
			require.True(t, proto.Equal(originalConfig, config))
		})
	}
}

func TestLoadDeploymentEnvironmentPropagatesReadFailure(t *testing.T) {
	options := agentServicePreviewFixture(t)
	environment := &previewContextEnvironmentServer{valuesErr: status.Error(codes.PermissionDenied, "denied")}
	provider := &AgentServiceTargetProvider{
		azdClient: newPreviewContextClient(t, &azdext.ProjectConfig{Path: options.ProjectRoot}, environment),
		env:       &azdext.Environment{Name: "test"},
	}
	_, err := provider.loadDeploymentEnvironment(t.Context(), options.Service)
	require.ErrorContains(t, err, "denied")
	require.Zero(t, environment.writes)
}
