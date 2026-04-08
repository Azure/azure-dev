// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_NewServiceContext_Coverage3(t *testing.T) {
	sc := NewServiceContext()
	require.NotNil(t, sc)
	assert.NotNil(t, sc.Restore)
	assert.NotNil(t, sc.Build)
	assert.NotNil(t, sc.Package)
	assert.NotNil(t, sc.Publish)
	assert.NotNil(t, sc.Deploy)
	assert.Empty(t, sc.Restore)
	assert.Empty(t, sc.Build)
	assert.Empty(t, sc.Package)
	assert.Empty(t, sc.Publish)
	assert.Empty(t, sc.Deploy)
}

func Test_NewServiceProgress_Coverage3(t *testing.T) {
	before := time.Now()
	sp := NewServiceProgress("building service")
	after := time.Now()

	assert.Equal(t, "building service", sp.Message)
	assert.True(t, sp.Timestamp.After(before) || sp.Timestamp.Equal(before))
	assert.True(t, sp.Timestamp.Before(after) || sp.Timestamp.Equal(after))
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
			{Run: "echo hello", Shell: ext.ShellTypeBash},
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
			{Run: "echo step1", Shell: ext.ShellTypeBash},
			{Run: "echo step2", Shell: ext.ShellTypeBash},
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
			{Run: "echo hello", Shell: ext.ShellTypeBash},
		},
		"postprovision": {
			{Run: "echo bye", Shell: ext.ShellTypeBash},
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
