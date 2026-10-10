// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestPreviewVisibleDescriptionChanges(t *testing.T) {
	for _, operation := range []string{"create", "add", "update", "remove", "empty", "unchanged"} {
		t.Run(operation, func(t *testing.T) {
			service := previewService(t)
			service.AdditionalProperties.Fields["description"] = structpb.NewStringValue("A helpful responses agent.")
			request, inputs := preparePresencePreview(t, service, t.TempDir())
			remote := remotePreviewAgent(request)
			remote.Versions.Latest.Description = new("A basic responses agent.")
			status := "update"
			line := `update: description: "A basic responses agent." -> "A helpful responses agent."`
			switch operation {
			case "create":
				remote = nil
				status = "create"
				line = `add: description: "A helpful responses agent."`
			case "add":
				remote.Versions.Latest.Description = nil
				line = `add: description: "A helpful responses agent."`
			case "remove":
				request.Description, inputs.Description = nil, nil
				line = `remove: description: "A basic responses agent." -> (removed)`
			case "empty":
				inputs.Description = new("")
				line = `update: description: "A basic responses agent." -> ""`
			case "unchanged":
				remote.Versions.Latest.Description = request.Description
				status = "noChange"
			}
			result, err := comparePreviewRequest(service.Name, request, remote, inputs)
			require.NoError(t, err)
			require.Equal(t, status, result.Data.AsMap()["status"])
			if operation == "unchanged" {
				require.Empty(t, result.Data.AsMap()["changes"])
				require.NotContains(t, result.Message, "Metadata:")
			} else {
				require.Contains(t, result.Message, line)
				encoded, err := json.Marshal(result.Data.AsMap())
				require.NoError(t, err)
				require.NotContains(t, string(encoded), "[redacted]")
				require.Contains(t, string(encoded), `"path":"description"`)
			}
		})
	}
}

func TestPreviewVisibleConfigurationDiff(t *testing.T) {
	service := previewService(t)
	service.AdditionalProperties.Fields["description"] = structpb.NewStringValue("A helpful responses agent.")
	service.AdditionalProperties.Fields["metadata"], _ = structpb.NewValue(map[string]any{
		"tags":  []any{"Agent Framework", "AI Agent Hosting", "Responses Protocol", "xxxx"},
		"owner": "support-team",
	})
	service.AdditionalProperties.Fields["protocols"], _ = structpb.NewValue([]any{
		map[string]any{"protocol": "responses", "version": "2.0.1"},
	})
	service.AdditionalProperties.Fields["container"], _ = structpb.NewValue(map[string]any{
		"resources": map[string]any{"memory": "2Gi"},
	})
	service.Environment = map[string]string{"MODE": "development", "FALSE": "false", "ZERO": "0", "EMPTY": ""}
	request, inputs := preparePresencePreview(t, service, t.TempDir())
	remote := remotePreviewAgent(request)
	remote.Versions.Latest.Description = new("A basic responses agent.")
	remote.Versions.Latest.Metadata = maps.Clone(request.Metadata)
	remote.Versions.Latest.Metadata["tags"] =
		`["Agent Framework","AI Agent Hosting","Responses Protocol","Streaming"]`
	hosted, ok := remote.Versions.Latest.Definition.(agent_api.HostedAgentDefinition)
	require.True(t, ok)
	hosted.Memory = "1Gi"
	hosted.ProtocolVersions = []agent_api.ProtocolVersionRecord{{Protocol: "responses", Version: "2.0.0"}}
	hosted.EnvironmentVariables = map[string]string{"MODE": "production"}
	remote.Versions.Latest.Definition = hosted
	result, err := previewAgentRequest(t.Context(), &recordingPreviewReader{agent: remote}, service.Name, request, inputs)
	require.NoError(t, err)
	require.Equal(t, "update", result.Data.AsMap()["status"])
	for _, line := range []string{
		`update: description: "A basic responses agent." -> "A helpful responses agent."`,
		`update: metadata.tags: ["Agent Framework","AI Agent Hosting","Responses Protocol","Streaming"] -> ` +
			`["Agent Framework","AI Agent Hosting","Responses Protocol","xxxx"]`,
		`update: definition.protocol_versions: [{"protocol":"responses","version":"2.0.0"}] -> ` +
			`[{"protocol":"responses","version":"2.0.1"}]`,
		`update: definition.memory: "1Gi" -> "2Gi"`,
		`update: definition.environment_variables.MODE: "production" -> "development"`,
		`add: definition.environment_variables.FALSE: "false"`,
		`add: definition.environment_variables.ZERO: "0"`,
		`add: definition.environment_variables.EMPTY: ""`,
	} {
		require.Contains(t, result.Message, line)
	}
	encoded, err := json.Marshal(result.Data.AsMap())
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "[redacted]")
	require.NotContains(t, result.Message, "Container image:")
	require.NotContains(t, result.Data.AsMap(), "containerImage")
	t.Log(result.Message)
}

func TestPreviewAuthoredEnvironmentProtection(t *testing.T) {
	root := t.TempDir()
	body := []byte(`name: preview
services:
  agent:
    host: azure.ai.agent
    metadata:
      $ref: metadata.yaml
    env:
      INLINE: development
      API_KEY: ${CREDENTIAL}
      EMBEDDED: prefix-${CREDENTIAL}
      PROCESS_KEY: ${PREVIEW_PROCESS_CREDENTIAL}
      URL_KEY: ${CREDENTIAL_URL}
      DEFAULT: ${MISSING:-fallback}
      TEMPLATE: ${{connections.example.credentials.key}}
      "FALSE": false
      ZERO: 0
      EMPTY: ""
      AZURE_AI_MODEL_DEPLOYMENT_NAME: ${MODEL}
    environmentVariables:
      - name: LEGACY_INLINE
        value: development
      - name: LEGACY_KEY
        value: ${CREDENTIAL}
`)
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), body, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "metadata.yaml"),
		[]byte("note: ${CREDENTIAL}\ntags: [support, '${CREDENTIAL}']\n"), 0600))
	t.Setenv("PREVIEW_PROCESS_CREDENTIAL", "private-process-secret")
	environment := map[string]string{"CREDENTIAL": "private-current-secret", "MODEL": "gpt-4.1"}
	//nolint:gosec // Fake credential-bearing URL verifies resolved-value non-disclosure.
	environment["CREDENTIAL_URL"] = "https://user:private-password@example.com?sig=private-signature"
	service := previewService(t)
	service.AdditionalProperties.Fields["description"] =
		structpb.NewStringValue("New instructions using private-current-secret.")
	service.AdditionalProperties.Fields["metadata"], _ = structpb.NewValue(map[string]any{
		"note": "private-process-secret", "tags": []any{"support", "private-current-secret"},
	})
	service.Environment = map[string]string{
		"INLINE": "development", "API_KEY": "private-current-secret", "EMBEDDED": "prefix-private-current-secret",
		"PROCESS_KEY": "private-process-secret", "DEFAULT": "fallback",
		"URL_KEY":  environment["CREDENTIAL_URL"],
		"TEMPLATE": "${{connections.example.credentials.key}}", "FALSE": "false", "ZERO": "0", "EMPTY": "",
		"AZURE_AI_MODEL_DEPLOYMENT_NAME": "gpt-4.1",
	}
	source, err := previewSourceInputs(root, service, environment)
	require.NoError(t, err)
	for _, name := range []string{"INLINE", "TEMPLATE", "FALSE", "ZERO", "EMPTY", "LEGACY_INLINE"} {
		require.True(t, source.PublicEnvironment[name], name)
	}
	for _, name := range []string{"API_KEY", "EMBEDDED", "PROCESS_KEY", "URL_KEY", "DEFAULT", "LEGACY_KEY"} {
		require.False(t, source.PublicEnvironment[name], name)
	}
	require.Contains(t, source.Secrets, "private-current-secret")
	require.Contains(t, source.Secrets, "private-process-secret")
	require.NotContains(t, source.Secrets, "development")
	require.NotContains(t, source.Secrets, "gpt-4.1")
	request, inputs := preparePresencePreview(t, service, root)
	inputs.PublicEnvironment, inputs.Secrets = source.PublicEnvironment, source.Secrets
	remote := remotePreviewAgent(request)
	remote.Versions.Latest.Description = new("Old instructions using private-previous-secret.")
	remote.Versions.Latest.Metadata = maps.Clone(request.Metadata)
	remote.Versions.Latest.Metadata["note"] = "private-removed-secret"
	remote.Versions.Latest.Metadata["tags"] = `["support","private-previous-secret"]`
	hosted, ok := remote.Versions.Latest.Definition.(agent_api.HostedAgentDefinition)
	require.True(t, ok)
	hosted.EnvironmentVariables = maps.Clone(hosted.EnvironmentVariables)
	hosted.EnvironmentVariables["API_KEY"] = "private-previous-secret"
	hosted.EnvironmentVariables["REMOVED_KEY"] = "private-removed-secret"
	hosted.EnvironmentVariables["URL_KEY"] = "https://example.com/previous-private-path"
	remote.Versions.Latest.Definition = hosted
	result, err := comparePreviewRequest(service.Name, request, remote, inputs)
	require.NoError(t, err)
	require.Equal(t, "update", result.Data.AsMap()["status"])
	require.Contains(t, result.Message,
		`update: description: "Old instructions using [redacted]." -> "New instructions using [redacted]."`)
	require.Contains(t, result.Message, `update: definition.environment_variables.API_KEY: "[redacted]" -> "[redacted]"`)
	require.Contains(t, result.Message, `remove: definition.environment_variables.REMOVED_KEY: "[redacted]" -> (removed)`)
	require.Contains(t, result.Message, `update: metadata.note: "[redacted]" -> "[redacted]"`)
	require.Contains(t, result.Message, `update: metadata.tags: ["support","[redacted]"] -> ["support","[redacted]"]`)
	require.Contains(t, result.Message,
		`update: definition.environment_variables.URL_KEY: "[redacted]" -> "[redacted]"`)
	encoded, err := json.Marshal(result.Data.AsMap())
	require.NoError(t, err)
	require.NotContains(t, result.Message, "private-")
	require.NotContains(t, string(encoded), "private-")
	require.NotContains(t, string(encoded), "previous-private-path")
	persisted, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, body, persisted)
}

func TestPreviewSanitizesTypedInlineValues(t *testing.T) {
	for _, value := range []any{false, float64(0), "", []any{}, []any{"support", "responses"}} {
		require.Equal(t, value, previewDisplayValue("metadata.public", value, redactPreviewURLs))
	}
	//nolint:gosec // Fake URL credentials verify targeted non-disclosure.
	value := "Docs at https://user:private-password@example.com?sig=private-signature#private-fragment"
	actual := previewDisplayValue("description", value, redactPreviewURLs)
	require.Equal(t, "Docs at https://example.com", actual)
	require.NotContains(t, actual, "private-")
	value = strings.ReplaceAll(value, "https", "HTTPS")
	require.NotContains(t, previewDisplayValue("metadata.url", value, redactPreviewURLs), "private-")
}
