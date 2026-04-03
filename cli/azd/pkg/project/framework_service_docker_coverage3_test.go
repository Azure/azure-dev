// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_NewDockerProjectAsFrameworkService_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	result := NewDockerProjectAsFrameworkService(env, nil, &ContainerHelper{}, nil, nil, nil)
	require.NotNil(t, result)
}

func Test_dockerProject_Requirements_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	p := NewDockerProject(env, nil, &ContainerHelper{}, nil, nil, nil)
	reqs := p.(FrameworkService).Requirements()
	assert.True(t, reqs.Package.RequireBuild)
	assert.False(t, reqs.Package.RequireRestore)
}

func Test_dockerProject_RequiredExternalTools_RemoteBuild_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	ch := &ContainerHelper{}
	p := NewDockerProject(env, nil, ch, nil, nil, nil)

	svcConfig := &ServiceConfig{
		Docker: DockerProjectOptions{RemoteBuild: true},
	}

	ctx := t.Context()
	tools := p.(FrameworkService).RequiredExternalTools(ctx, svcConfig)
	// Remote build => no external tools
	assert.Empty(t, tools)
}

func Test_dockerProject_Initialize_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	p := NewDockerProject(env, nil, &ContainerHelper{}, nil, nil, nil)
	// Initialize delegates to the inner NoOp framework, which returns nil
	err := p.(FrameworkService).Initialize(t.Context(), &ServiceConfig{})
	require.NoError(t, err)
}

func Test_dockerProject_SetSource_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	p := NewDockerProject(env, nil, &ContainerHelper{}, nil, nil, nil)

	// Set a custom inner framework
	innerEnv := environment.NewWithValues("inner-env", nil)
	inner := NewNoOpProject(innerEnv)
	p.SetSource(inner)

	// Verify Initialize now uses the new inner framework
	err := p.(FrameworkService).Initialize(t.Context(), &ServiceConfig{})
	require.NoError(t, err)
}

func Test_dockerProject_Restore_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	p := NewDockerProject(env, nil, &ContainerHelper{}, nil, nil, nil)

	result, err := p.(FrameworkService).Restore(
		t.Context(),
		&ServiceConfig{},
		NewServiceContext(),
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_ignoreAspireMultiStageDeployment_Coverage3(t *testing.T) {
	tests := []struct {
		name     string
		config   *ServiceConfig
		expected bool
	}{
		{
			name:     "BuildOnly",
			config:   &ServiceConfig{BuildOnly: true},
			expected: true,
		},
		{
			name: "HasContainerFiles",
			config: &ServiceConfig{
				DotNetContainerApp: &DotNetContainerAppOptions{
					ContainerFiles: map[string]ContainerFile{
						"svc": {Sources: []string{"Dockerfile"}},
					},
				},
			},
			expected: true,
		},
		{
			name: "EmptyContainerFiles",
			config: &ServiceConfig{
				DotNetContainerApp: &DotNetContainerAppOptions{
					ContainerFiles: map[string]ContainerFile{},
				},
			},
			expected: false,
		},
		{
			name:     "NoDotNetContainerApp",
			config:   &ServiceConfig{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ignoreAspireMultiStageDeployment(tt.config)
			assert.Equal(t, tt.expected, result)
		})
	}
}
