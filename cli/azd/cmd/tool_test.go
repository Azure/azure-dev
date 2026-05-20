// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
