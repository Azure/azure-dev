// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_NewAppServiceTarget_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	target := NewAppServiceTarget(env, nil, nil)
	require.NotNil(t, target)
}

func Test_appServiceTarget_RequiredExternalTools_Coverage3(t *testing.T) {
	target := NewAppServiceTarget(nil, nil, nil)
	result := target.RequiredExternalTools(t.Context(), nil)
	assert.Empty(t, result)
}

func Test_appServiceTarget_Initialize_Coverage3(t *testing.T) {
	target := NewAppServiceTarget(nil, nil, nil)
	err := target.Initialize(t.Context(), nil)
	require.NoError(t, err)
}

func Test_slotEnvVarNameForService_Coverage3(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple", "web", "AZD_DEPLOY_WEB_SLOT_NAME"},
		{"withHyphens", "my-web-app", "AZD_DEPLOY_MY_WEB_APP_SLOT_NAME"},
		{"uppercase", "MyApp", "AZD_DEPLOY_MYAPP_SLOT_NAME"},
		{"mixed", "my-App-2", "AZD_DEPLOY_MY_APP_2_SLOT_NAME"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := slotEnvVarNameForService(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func Test_NewStaticWebAppTarget_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	target := NewStaticWebAppTarget(env, nil, nil)
	require.NotNil(t, target)
}

func Test_staticWebAppTarget_RequiredExternalTools_Coverage3(t *testing.T) {
	target := NewStaticWebAppTarget(nil, nil, nil)
	result := target.RequiredExternalTools(t.Context(), nil)
	require.Len(t, result, 1)
	// Contains the swa CLI (nil since we passed nil)
	assert.Nil(t, result[0])
}

func Test_staticWebAppTarget_Initialize_Coverage3(t *testing.T) {
	target := NewStaticWebAppTarget(nil, nil, nil)
	err := target.Initialize(t.Context(), nil)
	require.NoError(t, err)
}

func Test_staticWebAppTarget_Publish_Coverage3(t *testing.T) {
	target := NewStaticWebAppTarget(nil, nil, nil)
	result, err := target.Publish(t.Context(), nil, nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_usingSwaConfig_Coverage3(t *testing.T) {
	tests := []struct {
		name      string
		artifacts ArtifactCollection
		expected  bool
	}{
		{
			name:      "empty",
			artifacts: ArtifactCollection{},
			expected:  false,
		},
		{
			name: "hasConfig",
			artifacts: ArtifactCollection{
				{Kind: ArtifactKindConfig, Location: "swa-cli.config.json"},
			},
			expected: true,
		},
		{
			name: "noConfig",
			artifacts: ArtifactCollection{
				{Kind: ArtifactKindDirectory, Location: "/build"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := usingSwaConfig(tt.artifacts)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func Test_staticWebAppTarget_Package_Coverage3(t *testing.T) {
	t.Run("WithSwaConfig", func(t *testing.T) {
		target := NewStaticWebAppTarget(nil, nil, nil)
		svcCtx := NewServiceContext()
		require.NoError(t, svcCtx.Package.Add(&Artifact{
			Kind:         ArtifactKindConfig,
			Location:     "swa-cli.config.json",
			LocationKind: LocationKindLocal,
		}))

		result, err := target.Package(t.Context(), &ServiceConfig{}, svcCtx, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, ArtifactKindConfig, result.Artifacts[0].Kind)
	})

	t.Run("WithBuildOutput", func(t *testing.T) {
		target := NewStaticWebAppTarget(nil, nil, nil)
		svcCtx := NewServiceContext()
		require.NoError(t, svcCtx.Package.Add(&Artifact{
			Kind:         ArtifactKindDirectory,
			Location:     "/build/output",
			LocationKind: LocationKindLocal,
		}))

		result, err := target.Package(t.Context(), &ServiceConfig{}, svcCtx, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, ArtifactKindDirectory, result.Artifacts[0].Kind)
		assert.Equal(t, "/build/output", result.Artifacts[0].Location)
	})

	t.Run("WithOutputPath", func(t *testing.T) {
		target := NewStaticWebAppTarget(nil, nil, nil)
		svcCtx := NewServiceContext()
		require.NoError(t, svcCtx.Package.Add(&Artifact{
			Kind:         ArtifactKindDirectory,
			Location:     "/build/output",
			LocationKind: LocationKindLocal,
		}))

		result, err := target.Package(t.Context(), &ServiceConfig{OutputPath: "dist"}, svcCtx, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "dist", result.Artifacts[0].Location)
	})
}

func Test_NewFunctionAppTarget_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	target := NewFunctionAppTarget(env, nil, nil)
	require.NotNil(t, target)
}

func Test_functionAppTarget_RequiredExternalTools_Coverage3(t *testing.T) {
	target := NewFunctionAppTarget(nil, nil, nil)
	result := target.RequiredExternalTools(t.Context(), nil)
	assert.Empty(t, result)
}

func Test_functionAppTarget_Initialize_Coverage3(t *testing.T) {
	target := NewFunctionAppTarget(nil, nil, nil)
	err := target.Initialize(t.Context(), nil)
	require.NoError(t, err)
}

func Test_functionAppTarget_Publish_Coverage3(t *testing.T) {
	target := NewFunctionAppTarget(nil, nil, nil)
	result, err := target.Publish(t.Context(), nil, nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_suggestRemoteBuild_Coverage3(t *testing.T) {
	tests := []struct {
		name     string
		svcTools []svcToolInfo
		toolErr  *tools.MissingToolErrors
		wantNil  bool
		wantMsg  string
	}{
		{
			name:     "NoDocker",
			svcTools: nil,
			toolErr: &tools.MissingToolErrors{
				ToolNames: []string{"npm"},
				Errs:      []error{fmt.Errorf("npm not found")},
			},
			wantNil: true,
		},
		{
			name:     "DockerMissing_NoneNeedIt",
			svcTools: []svcToolInfo{{svc: &ServiceConfig{Name: "web"}, needsDocker: false}},
			toolErr: &tools.MissingToolErrors{
				ToolNames: []string{"Docker"},
				Errs:      []error{fmt.Errorf("Docker not found")},
			},
			wantNil: true,
		},
		{
			name:     "DockerMissing_SomeNeedIt",
			svcTools: []svcToolInfo{{svc: &ServiceConfig{Name: "api"}, needsDocker: true}},
			toolErr: &tools.MissingToolErrors{
				ToolNames: []string{"Docker"},
				Errs:      []error{fmt.Errorf("Docker not found")},
			},
			wantNil: false,
			wantMsg: "install Docker",
		},
		{
			name:     "DockerNotRunning_SomeNeedIt",
			svcTools: []svcToolInfo{{svc: &ServiceConfig{Name: "api"}, needsDocker: true}},
			toolErr: &tools.MissingToolErrors{
				ToolNames: []string{"Docker"},
				Errs:      []error{fmt.Errorf("Docker is not running")},
			},
			wantNil: false,
			wantMsg: "start your container runtime",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := suggestRemoteBuild(tt.svcTools, tt.toolErr)
			if tt.wantNil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Contains(t, result.Suggestion, tt.wantMsg)
				assert.Contains(t, result.Suggestion, "api")
			}
		})
	}
}
