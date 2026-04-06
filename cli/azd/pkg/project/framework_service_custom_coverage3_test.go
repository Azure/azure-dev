// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_CustomProject_NewCustomProject(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	fs := NewCustomProject(env)
	require.NotNil(t, fs)
}

func Test_CustomProject_Requirements(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	cp := NewCustomProject(env)
	reqs := cp.Requirements()

	assert.True(t, reqs.Package.RequireRestore)
	assert.True(t, reqs.Package.RequireBuild)
}

func Test_CustomProject_RequiredExternalTools(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	cp := NewCustomProject(env)
	tools := cp.RequiredExternalTools(t.Context(), nil)

	require.NotNil(t, tools)
	assert.Empty(t, tools)
}

func Test_CustomProject_Initialize(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	cp := NewCustomProject(env)
	err := cp.Initialize(t.Context(), nil)
	require.NoError(t, err)
}

func Test_CustomProject_Restore(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	cp := NewCustomProject(env)

	svcConfig := &ServiceConfig{
		RelativePath: "src/myapp",
		Project:      &ProjectConfig{Path: "/project"},
	}

	result, err := cp.Restore(t.Context(), svcConfig, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Artifacts, 1)

	art := result.Artifacts[0]
	assert.Equal(t, ArtifactKindDirectory, art.Kind)
	assert.Equal(t, LocationKindLocal, art.LocationKind)
	assert.Equal(t, svcConfig.Path(), art.Location)
	assert.Equal(t, "custom", art.Metadata["framework"])
	assert.Equal(t, svcConfig.Path(), art.Metadata["projectPath"])
}

func Test_CustomProject_Build(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	cp := NewCustomProject(env)

	svcConfig := &ServiceConfig{
		RelativePath: "src/myapp",
		Project:      &ProjectConfig{Path: "/project"},
	}

	result, err := cp.Build(t.Context(), svcConfig, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Artifacts, 1)

	art := result.Artifacts[0]
	assert.Equal(t, ArtifactKindDirectory, art.Kind)
	assert.Equal(t, LocationKindLocal, art.LocationKind)
	assert.Equal(t, svcConfig.Path(), art.Location)
	assert.Equal(t, "custom", art.Metadata["framework"])
	assert.Equal(t, svcConfig.Path(), art.Metadata["buildPath"])
}

func Test_CustomProject_Package(t *testing.T) {
	t.Run("with output path", func(t *testing.T) {
		env := environment.NewWithValues("test-env", nil)
		cp := NewCustomProject(env)

		svcConfig := &ServiceConfig{
			OutputPath: "dist",
			Project:    &ProjectConfig{Path: "/project"},
		}

		result, err := cp.Package(t.Context(), svcConfig, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Artifacts, 1)

		art := result.Artifacts[0]
		assert.Equal(t, ArtifactKindDirectory, art.Kind)
		assert.Equal(t, "dist", art.Location)
		assert.Equal(t, LocationKindLocal, art.LocationKind)
		assert.Equal(t, "custom", art.Metadata["language"])
	})

	t.Run("without output path returns error", func(t *testing.T) {
		env := environment.NewWithValues("test-env", nil)
		cp := NewCustomProject(env)

		svcConfig := &ServiceConfig{
			OutputPath: "",
			Project:    &ProjectConfig{Path: "/project"},
		}

		result, err := cp.Package(t.Context(), svcConfig, nil, nil)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "'dist' required for custom language")
	})
}
