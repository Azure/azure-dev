// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/featureflags"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestConfigListAlphaText(t *testing.T) {
	var out bytes.Buffer
	err := runConfigListAlpha(t.Context(), &out, "text",
		func(_ context.Context, feature featureflags.Feature) (bool, error) {
			return feature == featureflags.Voice, nil
		})
	require.NoError(t, err)
	require.Equal(t, strings.Join([]string{
		"Name: agents.managed_harness",
		"Description: Prompt agents that run on a Copilot harness",
		"Status: Off",
		"Environment variable: AZD_FOUNDRY_EXT_ENABLE_AGENTS_MANAGED_HARNESS",
		"",
		"Name: agents.voice",
		"Description: Voice, prompt-voice, and hosted voice agents",
		"Status: On",
		"Environment variable: AZD_FOUNDRY_EXT_ENABLE_AGENTS_VOICE",
		"",
		"Name: agents.long_running",
		"Description: Long-running Responses invocations",
		"Status: Off",
		"Environment variable: AZD_FOUNDRY_EXT_ENABLE_AGENTS_LONG_RUNNING",
		"",
	}, "\n"), out.String())
}

func TestConfigListAlphaJSON(t *testing.T) {
	var out bytes.Buffer
	err := runConfigListAlpha(t.Context(), &out, "json",
		func(_ context.Context, feature featureflags.Feature) (bool, error) {
			return feature == featureflags.LongRunning, nil
		})
	require.NoError(t, err)
	var response alphaFeatureListResponse
	require.NoError(t, json.Unmarshal(out.Bytes(), &response))
	require.Len(t, response.Features, len(featureflags.All()))
	for i, feature := range featureflags.All() {
		item := response.Features[i]
		require.Equal(t, feature.ID, item.Name)
		require.Equal(t, feature.EnvVar, item.EnvVar)
		require.Equal(t, feature.Description, item.Description)
		want := "Off"
		if feature == featureflags.LongRunning {
			want = "On"
		}
		require.Equal(t, want, item.Status)
		require.Empty(t, item.Error)
	}
	require.NotContains(t, out.String(), "error")
}

func TestConfigListAlphaReportsEachReadError(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			err := runConfigListAlpha(t.Context(), &out, format,
				func(_ context.Context, feature featureflags.Feature) (bool, error) {
					if feature == featureflags.Voice {
						return false, errors.New("cannot read selected environment")
					}
					return true, nil
				})
			require.ErrorContains(t, err, "1 of 3")
			text := out.String()
			for _, feature := range featureflags.All() {
				require.Contains(t, text, feature.ID, "every feature is listed despite the error")
			}
			require.Contains(t, text, "cannot read selected environment")
			if format == "json" {
				var response alphaFeatureListResponse
				require.NoError(t, json.Unmarshal(out.Bytes(), &response))
				for _, item := range response.Features {
					if item.Name == featureflags.Voice.ID {
						require.Equal(t, "Unknown", item.Status)
						require.Equal(t, "cannot read selected environment", item.Error)
					} else {
						require.Equal(t, "On", item.Status)
						require.Empty(t, item.Error)
					}
				}
			}
			if format == "text" {
				require.Contains(t, text, "Name: agents.voice\n"+
					"Description: Voice, prompt-voice, and hosted voice agents\n"+
					"Status: Unknown\n"+
					"Environment variable: AZD_FOUNDRY_EXT_ENABLE_AGENTS_VOICE\n"+
					"Error: cannot read selected environment")
				require.Contains(t, text, "Name: agents.long_running\n"+
					"Description: Long-running Responses invocations\nStatus: On")
			}
		})
	}
}

func TestConfigListAlphaKeepsReadErrorChain(t *testing.T) {
	notFound := status.Error(codes.NotFound, "environment not found")
	err := runConfigListAlpha(t.Context(), io.Discard, "text",
		func(_ context.Context, feature featureflags.Feature) (bool, error) {
			switch feature {
			case featureflags.Voice:
				return false, notFound
			case featureflags.LongRunning:
				return false, context.Canceled
			}
			return false, nil
		})
	require.ErrorContains(t, err, "2 of 3")
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, notFound)
	require.Equal(t, "could not read the status of 2 of 3 alpha features", err.Error())
	readErr, ok := errors.AsType[*alphaReadError](err)
	require.True(t, ok)
	require.ErrorIs(t, readErr.Unwrap()[0], context.Canceled, "cancellation takes precedence")
}

func TestConfigListAlphaCommandReadsSelectedEnvironment(t *testing.T) {
	clearPreviewProcess(t)
	t.Chdir(t.TempDir())
	_, env := startPreviewCommandServer(t, map[string]any{
		"all":    "on",
		"agents": map[string]any{"voice": "on"},
	}, &azdext.ProjectConfig{Path: t.TempDir()}, nil)
	env.mu.Lock()
	env.values = []*azdext.KeyValue{{Key: featureflags.LongRunning.EnvVar, Value: "true"}}
	env.mu.Unlock()

	text, err := executeRoot(t, "config", "list-alpha", "-e", "selected-env", "--output", "json")
	require.NoError(t, err)
	var response alphaFeatureListResponse
	require.NoError(t, json.Unmarshal([]byte(text), &response))
	states := map[string]string{}
	for _, item := range response.Features {
		states[item.Name] = item.Status
	}
	require.Equal(t, map[string]string{
		"agents.managed_harness": "Off", "agents.voice": "On", "agents.long_running": "On",
	}, states)
	env.mu.Lock()
	require.Equal(t, "selected-env", env.selected)
	env.mu.Unlock()

	t.Setenv(featureflags.ManagedHarness.EnvVar, "true")
	text, err = executeRoot(t, "config", "list-alpha", "-e", "selected-env")
	require.NoError(t, err)
	require.Contains(t, text, "Name: agents.managed_harness\n"+
		"Description: Prompt agents that run on a Copilot harness\nStatus: On")
	require.NotContains(t, strings.ToLower(text), "azure.yaml")
	require.NotContains(t, strings.ToLower(text), "source")
}

func TestConfigListAlphaCommandUsesProcessValue(t *testing.T) {
	clearPreviewProcess(t)
	t.Setenv(featureflags.Voice.EnvVar, "1")
	startPreviewCommandServer(t, nil, nil, nil)
	text, err := executeRoot(t, "config", "list-alpha")
	require.NoError(t, err)
	require.Contains(t, text, "Name: agents.voice\n"+
		"Description: Voice, prompt-voice, and hosted voice agents\nStatus: On")
	require.Contains(t, text, "Name: agents.long_running\n"+
		"Description: Long-running Responses invocations\nStatus: Off")
}

func TestConfigListAlphaRejectsUnsupportedOutput(t *testing.T) {
	clearPreviewProcess(t)
	_, err := executeRoot(t, "config", "list-alpha", "--output", "raw")
	require.ErrorContains(t, err, "raw")
}

func TestConfigListAlphaRejectsEmptyEnvironment(t *testing.T) {
	for _, value := range []string{"", " \t "} {
		for _, flag := range []string{"--environment", "-e"} {
			t.Run(flag+"/"+value, func(t *testing.T) {
				clearPreviewProcess(t)
				t.Setenv("AZD_ENVIRONMENT", "default-env")

				text, err := executeRoot(t, "config", "list-alpha", flag, value)
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok, "%v", err)
				require.Equal(t, exterrors.CodeInvalidParameter, localErr.Code)
				require.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
				require.Contains(t, localErr.Message, "--environment")
				require.Empty(t, text)
			})
		}
	}
}

func TestFeatureCommandRemoved(t *testing.T) {
	clearPreviewProcess(t)
	for _, child := range NewRootCommand().Commands() {
		require.NotEqual(t, "feature", child.Name())
	}
}
