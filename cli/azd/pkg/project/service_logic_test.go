// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/stretchr/testify/require"
)

func Test_ServiceConfig_Path_Relative(t *testing.T) {
	sc := &ServiceConfig{
		RelativePath: "src/api",
		Project: &ProjectConfig{
			Path: "/home/user/myproject",
		},
	}

	expected := filepath.Join(
		"/home/user/myproject", "src/api",
	)
	require.Equal(t, expected, sc.Path())
}

func Test_ServiceConfig_Path_Absolute(t *testing.T) {
	// t.TempDir() returns a guaranteed absolute path
	// on any OS.
	absPath := t.TempDir()

	sc := &ServiceConfig{
		RelativePath: absPath,
		Project: &ProjectConfig{
			Path: "/home/user/myproject",
		},
	}

	// When RelativePath is absolute, it should be returned
	// as-is without joining with Project.Path.
	require.True(t, filepath.IsAbs(absPath))
	require.Equal(t, absPath, sc.Path())
}

func Test_isConditionTrue(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected bool
	}{
		{"1", "1", true},
		{"true", "true", true},
		{"TRUE", "TRUE", true},
		{"True", "True", true},
		{"yes", "yes", true},
		{"YES", "YES", true},
		{"Yes", "Yes", true},
		{"0", "0", false},
		{"false", "false", false},
		{"no", "no", false},
		{"empty", "", false},
		{"random", "random", false},
		{"tRuE mixed case", "tRuE", false},
		{"yEs mixed case", "yEs", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(
				t, tt.expected, isConditionTrue(tt.value),
			)
		})
	}
}

func Test_NewServiceContext(t *testing.T) {
	ctx := NewServiceContext()

	require.NotNil(t, ctx)
	require.NotNil(t, ctx.Restore)
	require.NotNil(t, ctx.Build)
	require.NotNil(t, ctx.Package)
	require.NotNil(t, ctx.Publish)
	require.NotNil(t, ctx.Deploy)

	// All collections should be empty (not nil)
	require.Len(t, ctx.Restore, 0)
	require.Len(t, ctx.Build, 0)
	require.Len(t, ctx.Package, 0)
	require.Len(t, ctx.Publish, 0)
	require.Len(t, ctx.Deploy, 0)
}

func Test_NewServiceProgress(t *testing.T) {
	msg := "deploying to Azure"
	progress := NewServiceProgress(msg)

	require.Equal(t, msg, progress.Message)
	require.False(t, progress.Timestamp.IsZero())
}

func Test_envResolver_NilEnv(t *testing.T) {
	resolver := envResolver(nil)
	result := resolver("ANY_KEY")
	require.Equal(t, "", result)
}

func Test_createProgressFunc_NilProgress(t *testing.T) {
	fn := createProgressFunc(nil)
	// Should not panic
	fn("some message")
}

func Test_createProgressFunc_WithProgress(t *testing.T) {
	// NewNoopProgress drains the channel in a background
	// goroutine, preventing SetProgress from blocking.
	progress := async.NewNoopProgress[ServiceProgress]()
	fn := createProgressFunc(progress)

	// Should not panic; sets progress internally
	fn("deploying step 1")
	progress.Done()
}

func Test_externalTool_Name(t *testing.T) {
	tool := &externalTool{
		name:       "my-tool",
		installUrl: "https://example.com/install",
	}

	require.Equal(t, "my-tool", tool.Name())
}

func Test_externalTool_InstallUrl(t *testing.T) {
	tool := &externalTool{
		name:       "my-tool",
		installUrl: "https://example.com/install",
	}

	require.Equal(
		t,
		"https://example.com/install",
		tool.InstallUrl(),
	)
}

func Test_stripUTF8BOM(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
	}{
		{
			name:     "no BOM",
			input:    []byte("hello world"),
			expected: []byte("hello world"),
		},
		{
			name:     "with BOM",
			input:    append(utf8BOM, []byte("hello")...),
			expected: []byte("hello"),
		},
		{
			name:     "only BOM",
			input:    utf8BOM,
			expected: []byte{},
		},
		{
			name:     "empty input",
			input:    []byte{},
			expected: []byte{},
		},
		{
			name:     "partial BOM prefix",
			input:    []byte{0xEF, 0xBB},
			expected: []byte{0xEF, 0xBB},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(
				t, tt.expected, stripUTF8BOM(tt.input),
			)
		})
	}
}
