// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"context"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/tool"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func TestToolCommandGating(t *testing.T) {
	t.Run("hidden when alpha feature disabled", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("AZD_CONFIG_DIR", configDir)
		// Ensure the tool alpha feature is NOT enabled.
		t.Setenv("AZD_ALPHA_ENABLE_TOOL", "false")

		root := NewRootCmd(true, nil, nil)
		found := false
		for _, c := range root.Commands() {
			if c.Name() == "tool" {
				found = true
				break
			}
		}
		require.False(t, found, "expected 'tool' subcommand to be absent when alpha feature is disabled")
	})

	t.Run("present when alpha feature enabled", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("AZD_CONFIG_DIR", configDir)
		t.Setenv("AZD_ALPHA_ENABLE_TOOL", "true")

		root := NewRootCmd(true, nil, nil)
		found := false
		for _, c := range root.Commands() {
			if c.Name() == "tool" {
				found = true
				break
			}
		}
		require.True(t, found, "expected 'tool' subcommand to be present when alpha feature is enabled")
	})
}

func TestRunToolOperationUnsuccessfulResultReturnsError(t *testing.T) {
	toolDef := &tool.ToolDefinition{
		Id:   "az-cli",
		Name: "Azure CLI",
	}
	console := mockinput.NewMockConsole()

	results, err := runToolOperation(
		t.Context(),
		[]*tool.ToolDefinition{toolDef},
		func(ctx context.Context, ids []string) ([]*tool.InstallResult, error) {
			return []*tool.InstallResult{
				{
					Tool:    toolDef,
					Success: false,
				},
			}, nil
		},
		"Installing",
		"install",
		console,
	)

	require.Error(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Success)
	require.NotEmpty(t, console.Output())
	assert.Contains(t, console.Output()[0], "Some tools could not be")
}

func TestRunToolOperationSuccessfulResultReturnsNoError(t *testing.T) {
	toolDef := &tool.ToolDefinition{
		Id:   "az-cli",
		Name: "Azure CLI",
	}
	console := mockinput.NewMockConsole()

	results, err := runToolOperation(
		t.Context(),
		[]*tool.ToolDefinition{toolDef},
		func(ctx context.Context, ids []string) ([]*tool.InstallResult, error) {
			assert.Equal(t, []string{"az-cli"}, ids)

			return []*tool.InstallResult{
				{
					Tool:             toolDef,
					Success:          true,
					InstalledVersion: "2.73.0",
				},
			}, nil
		},
		"Installing",
		"install",
		console,
	)

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Success)
	assert.Equal(t, "az-cli", results[0].Id)
	assert.Equal(t, "Azure CLI", results[0].Name)
	assert.Equal(t, "install", results[0].Action)
	assert.Equal(t, "2.73.0", results[0].InstalledVersion)
	assert.Empty(t, console.Output())
}

// lookupToolStrUsage returns the most recent string-valued usage attribute
// for the given key (assumes the key was set via .String()).
func lookupToolStrUsage(key string) (string, bool) {
	for _, a := range tracing.GetUsageAttributes() {
		if string(a.Key) == key {
			return a.Value.AsString(), true
		}
	}
	return "", false
}

// lookupToolIntUsage returns the most recent int64-valued usage attribute
// for the given key (assumes the key was set via .Int() / .Int64()).
func lookupToolIntUsage(key string) (int64, bool) {
	for _, a := range tracing.GetUsageAttributes() {
		if string(a.Key) == key {
			return a.Value.AsInt64(), true
		}
	}
	return 0, false
}

func TestEmitToolInstallTelemetry_AllSucceeded(t *testing.T) {
	tracing.SetUsageAttributes(
		fields.ToolInstallSuccessCountKey.Int(-1),
		fields.ToolInstallFailureCountKey.Int(-1),
		fields.ToolInstallDurationMsKey.Int64(-1),
		fields.ToolInstallFailedIdsKey.String("__sentinel__"),
	)

	results := []*tool.InstallResult{
		{Tool: &tool.ToolDefinition{Id: "kubectl"}, Success: true},
		{Tool: &tool.ToolDefinition{Id: "helm"}, Success: true},
	}
	emitToolInstallTelemetry(results, 250*time.Millisecond)

	got, ok := lookupToolIntUsage(string(fields.ToolInstallSuccessCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(2), got)

	got, ok = lookupToolIntUsage(string(fields.ToolInstallFailureCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(0), got)

	got, ok = lookupToolIntUsage(string(fields.ToolInstallDurationMsKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(250), got)

	gotStr, ok := lookupToolStrUsage(string(fields.ToolInstallFailedIdsKey.Key))
	require.True(t, ok)
	assert.Equal(t, "__sentinel__", gotStr,
		"failed_ids should not be overwritten when there are no failures")
}

func TestEmitToolInstallTelemetry_MixedSuccessAndFailure(t *testing.T) {
	tracing.SetUsageAttributes(
		fields.ToolInstallFailedIdsKey.String("__sentinel__"),
	)

	results := []*tool.InstallResult{
		{Tool: &tool.ToolDefinition{Id: "kubectl"}, Success: true},
		{Tool: &tool.ToolDefinition{Id: "helm"}, Success: false, Error: errors.New("network failure")},
		{Tool: &tool.ToolDefinition{Id: "terraform"}, Success: false, Error: errors.New("not found")},
	}
	emitToolInstallTelemetry(results, 1*time.Second)

	got, ok := lookupToolIntUsage(string(fields.ToolInstallSuccessCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(1), got)

	got, ok = lookupToolIntUsage(string(fields.ToolInstallFailureCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(2), got)

	got, ok = lookupToolIntUsage(string(fields.ToolInstallDurationMsKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(1000), got)

	gotStr, ok := lookupToolStrUsage(string(fields.ToolInstallFailedIdsKey.Key))
	require.True(t, ok)
	assert.Equal(t, "helm,terraform", gotStr)
}

func TestEmitToolInstallTelemetry_FailureWithNilTool(t *testing.T) {
	tracing.SetUsageAttributes(
		fields.ToolInstallFailedIdsKey.String("__sentinel2__"),
	)

	results := []*tool.InstallResult{
		{Tool: nil, Success: false, Error: errors.New("orphan failure")},
	}
	emitToolInstallTelemetry(results, 10*time.Millisecond)

	got, ok := lookupToolIntUsage(string(fields.ToolInstallFailureCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(1), got)

	gotStr, ok := lookupToolStrUsage(string(fields.ToolInstallFailedIdsKey.Key))
	require.True(t, ok)
	assert.Equal(t, "__sentinel2__", gotStr,
		"failed_ids must not be emitted when the failure has no Tool reference")
}

func TestEmitToolInstallTelemetry_EmptyResults(t *testing.T) {
	emitToolInstallTelemetry(nil, 0)

	got, ok := lookupToolIntUsage(string(fields.ToolInstallSuccessCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(0), got)

	got, ok = lookupToolIntUsage(string(fields.ToolInstallFailureCountKey.Key))
	require.True(t, ok)
	assert.Equal(t, int64(0), got)
}
