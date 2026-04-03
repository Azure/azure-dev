// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_NewSwaProject_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	inner := NewNoOpProject(env)
	result := NewSwaProject(env, nil, nil, nil, inner)
	require.NotNil(t, result)
}

func Test_NewSwaProjectAsFrameworkService_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	inner := NewNoOpProject(env)
	result := NewSwaProjectAsFrameworkService(env, nil, nil, nil, inner)
	require.NotNil(t, result)
}

func Test_swaProject_Requirements_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	inner := NewNoOpProject(env)
	p := NewSwaProject(env, nil, nil, nil, inner)
	reqs := p.(FrameworkService).Requirements()
	assert.True(t, reqs.Package.RequireRestore)
	assert.True(t, reqs.Package.RequireBuild)
}

func Test_swaProject_RequiredExternalTools_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	inner := NewNoOpProject(env)
	p := NewSwaProject(env, nil, nil, nil, inner)
	tools := p.(FrameworkService).RequiredExternalTools(t.Context(), nil)
	// Returns the swa CLI (nil in this case)
	require.Len(t, tools, 1)
	assert.Nil(t, tools[0])
}

func Test_swaProject_Initialize_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	inner := NewNoOpProject(env)
	p := NewSwaProject(env, nil, nil, nil, inner)
	err := p.(FrameworkService).Initialize(t.Context(), &ServiceConfig{})
	require.NoError(t, err)
}

func Test_swaProject_SetSource_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	inner := NewNoOpProject(env)
	p := NewSwaProject(env, nil, nil, nil, inner)

	newInner := NewNoOpProject(environment.NewWithValues("new-env", nil))
	p.(CompositeFrameworkService).SetSource(newInner)

	err := p.(FrameworkService).Initialize(t.Context(), &ServiceConfig{})
	require.NoError(t, err)
}

func Test_swaProject_Restore_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	inner := NewNoOpProject(env)
	p := NewSwaProject(env, nil, nil, nil, inner)

	result, err := p.(FrameworkService).Restore(
		t.Context(),
		&ServiceConfig{},
		NewServiceContext(),
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_swaProject_Package_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	inner := NewNoOpProject(env)
	p := NewSwaProject(env, nil, nil, nil, inner)

	svcConfig := &ServiceConfig{
		Project:      &ProjectConfig{Path: t.TempDir()},
		RelativePath: ".",
	}

	result, err := p.(FrameworkService).Package(
		t.Context(),
		svcConfig,
		NewServiceContext(),
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Artifacts)
	assert.Equal(t, ArtifactKindConfig, result.Artifacts[0].Kind)
	assert.Equal(t, LocationKindLocal, result.Artifacts[0].LocationKind)
}
