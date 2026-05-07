// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_NoOpProject_NewNoOpProject(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	fs := NewNoOpProject(env)
	require.NotNil(t, fs)
}

func Test_NoOpProject_Requirements(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	p := NewNoOpProject(env)
	reqs := p.Requirements()

	assert.False(t, reqs.Package.RequireRestore)
	assert.False(t, reqs.Package.RequireBuild)
}

func Test_NoOpProject_RequiredExternalTools(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	p := NewNoOpProject(env)
	tools := p.RequiredExternalTools(t.Context(), nil)

	require.NotNil(t, tools)
	assert.Empty(t, tools)
}

func Test_NoOpProject_Initialize(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	p := NewNoOpProject(env)
	err := p.Initialize(t.Context(), nil)
	require.NoError(t, err)
}

func Test_NoOpProject_Restore(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	p := NewNoOpProject(env)
	result, err := p.Restore(t.Context(), nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Artifacts)
}

func Test_NoOpProject_Build(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	p := NewNoOpProject(env)
	result, err := p.Build(t.Context(), nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Artifacts)
}

func Test_NoOpProject_Package(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	p := NewNoOpProject(env)
	result, err := p.Package(t.Context(), nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Artifacts)
}

func Test_ValidatePackageOutput(t *testing.T) {
	t.Run("non-existent directory", func(t *testing.T) {
		err := validatePackageOutput(filepath.Join(t.TempDir(), "does-not-exist"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not exist")
	})

	t.Run("empty directory", func(t *testing.T) {
		dir := t.TempDir()
		err := validatePackageOutput(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is empty")
	})

	t.Run("directory with files", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content"), 0600))
		err := validatePackageOutput(dir)
		require.NoError(t, err)
	})
}

func Test_IsDotNet(t *testing.T) {
	tests := []struct {
		lang   ServiceLanguageKind
		expect bool
	}{
		{ServiceLanguageDotNet, true},
		{ServiceLanguageCsharp, true},
		{ServiceLanguageFsharp, true},
		{ServiceLanguagePython, false},
		{ServiceLanguageJavaScript, false},
		{ServiceLanguageTypeScript, false},
		{ServiceLanguageJava, false},
		{ServiceLanguageDocker, false},
		{ServiceLanguageCustom, false},
		{ServiceLanguageNone, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.lang), func(t *testing.T) {
			assert.Equal(t, tt.expect, tt.lang.IsDotNet())
		})
	}
}
