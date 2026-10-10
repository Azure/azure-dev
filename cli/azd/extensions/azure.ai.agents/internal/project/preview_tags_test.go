// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestPreviewAuthoredMetadataTags(t *testing.T) {
	for _, tc := range []struct {
		name       string
		metadata   map[string]any
		before     map[string]string
		create     bool
		status     string
		operation  string
		path       string
		wantBefore any
		wantAfter  any
	}{
		{name: "create list", create: true, metadata: map[string]any{"tags": []any{"customer-support", "responses"}},
			status: "create", operation: "add", path: "metadata.tags", wantAfter: []any{"customer-support", "responses"}},
		{name: "add list", metadata: map[string]any{"tags": []any{"customer-support", "responses"}},
			status: "update", operation: "add", path: "metadata.tags", wantAfter: []any{"customer-support", "responses"}},
		{name: "update list", metadata: map[string]any{"tags": []any{"retained", "added"}},
			before: map[string]string{"tags": `["retained","removed"]`},
			status: "update", operation: "update", path: "metadata.tags",
			wantBefore: []any{"retained", "removed"}, wantAfter: []any{"retained", "added"}},
		{name: "remove last tag", before: map[string]string{"tags": `["removed"]`},
			status: "update", operation: "remove", path: "metadata.tags", wantBefore: []any{"removed"}},
		{name: "remove all metadata", metadata: map[string]any{}, before: map[string]string{"tags": `["removed"]`},
			status: "update", operation: "remove", path: "metadata.tags", wantBefore: []any{"removed"}},
		{name: "empty list is present", metadata: map[string]any{"tags": []any{}},
			before: map[string]string{"tags": `["removed"]`},
			status: "update", operation: "update", path: "metadata.tags", wantBefore: []any{"removed"}, wantAfter: []any{}},
		{name: "empty string is present", metadata: map[string]any{"tags": ""},
			status: "update", operation: "add", path: "metadata.tags", wantAfter: ""},
		{name: "scalar string", metadata: map[string]any{"tags": "customer-support"},
			before: map[string]string{"tags": "general"}, status: "update", operation: "update", path: "metadata.tags",
			wantBefore: "general", wantAfter: "customer-support"},
		{name: "remove scalar string", before: map[string]string{"tags": "customer-support"},
			status: "update", operation: "remove", path: "metadata.tags", wantBefore: "customer-support"},
		{name: "remove empty list", before: map[string]string{"tags": "[]"},
			status: "update", operation: "remove", path: "metadata.tags", wantBefore: []any{}},
		{name: "remove empty string", before: map[string]string{"tags": ""},
			status: "update", operation: "remove", path: "metadata.tags", wantBefore: ""},
		{name: "unchanged list", metadata: map[string]any{"tags": []any{"same"}},
			before: map[string]string{"tags": `["same"]`}, status: "noChange"},
		{name: "unchanged string", metadata: map[string]any{"tags": "same"},
			before: map[string]string{"tags": "same"}, status: "noChange"},
		{name: "unchanged empty list", metadata: map[string]any{"tags": []any{}},
			before: map[string]string{"tags": `[]`}, status: "noChange"},
		{name: "unchanged empty string", metadata: map[string]any{"tags": ""},
			before: map[string]string{"tags": ""}, status: "noChange"},
		{name: "credential-bearing list", metadata: map[string]any{
			"tags": []any{
				"support", "https://private-user:private-password@example.com?sig=private-signature#private-fragment",
			},
		}, status: "update", operation: "add", path: "metadata.tags", wantAfter: []any{"support", "https://example.com"}},
		{name: "absent on both sides", status: "noChange"},
		{name: "add metadata key", metadata: map[string]any{"owner": "private-team"},
			status: "update", operation: "add", path: "metadata.owner", wantAfter: "[redacted]"},
		{name: "update metadata key", metadata: map[string]any{"owner": "private-new"},
			before: map[string]string{"owner": "private-old"},
			status: "update", operation: "update", path: "metadata.owner",
			wantBefore: "[redacted]", wantAfter: "[redacted]"},
		{name: "remove metadata key", before: map[string]string{"owner": "private-old"},
			status: "update", operation: "remove", path: "metadata.owner", wantBefore: "[redacted]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := previewService(t)
			service.Image, service.Docker = "", nil
			service.AdditionalProperties.Fields["codeConfiguration"], _ = structpb.NewValue(map[string]any{
				"runtime": "python_3_13", "entryPoint": "app.py",
			})
			if tc.metadata != nil {
				service.AdditionalProperties.Fields["metadata"], _ = structpb.NewValue(tc.metadata)
			}
			original := proto.CloneOf(service)
			root := t.TempDir()
			resolved, definition, err := resolvePreviewDefinition(service, root)
			require.NoError(t, err)
			deploy, err := prepareDeployRequest(resolved, definition, nil, nil)
			require.NoError(t, err)
			request, inputs, err := preparePreviewRequest(resolved, definition, nil, nil)
			require.NoError(t, err)
			require.Equal(t, deploy.request.Metadata, request.Metadata, "preview must compare the real deploy request")
			remote := remotePreviewAgent(request)
			remote.Versions.Latest.Metadata = maps.Clone(tc.before)
			if remote.Versions.Latest.Metadata == nil {
				remote.Versions.Latest.Metadata = map[string]string{}
			}
			remote.Versions.Latest.Metadata["enableVnextExperience"] = "true"
			// Exercise the actual remote GET model, not just the request's in-memory shape.
			wire, err := json.Marshal(remote)
			require.NoError(t, err)
			remote = &agent_api.AgentObject{}
			require.NoError(t, json.Unmarshal(wire, remote))
			if tc.create {
				remote = nil
			}
			result, err := comparePreviewRequest(service.Name, request, remote, inputs)
			require.NoError(t, err)
			data := result.Data.AsMap()
			require.Equal(t, tc.status, data["status"])
			changes, ok := data["changes"].([]any)
			require.True(t, ok)
			if tc.operation == "" {
				require.Empty(t, changes)
				require.NotContains(t, result.Message, "  Metadata:")
			} else {
				count := 1
				if tc.create {
					count++ // The agent name is also added on creation.
				}
				require.Len(t, changes, count)
				found := false
				for _, item := range changes {
					change, ok := item.(map[string]any)
					require.True(t, ok)
					if change["path"] != tc.path {
						continue
					}
					found = true
					require.Equal(t, "metadata", change["group"])
					require.Equal(t, tc.operation, change["operation"])
					if tc.operation != "add" {
						require.Equal(t, tc.wantBefore, change["before"])
					} else {
						require.NotContains(t, change, "before")
					}
					if tc.operation == "remove" {
						require.NotContains(t, change, "after")
					} else {
						require.Equal(t, tc.wantAfter, change["after"])
					}
					beforeJSON, err := json.Marshal(tc.wantBefore)
					require.NoError(t, err)
					afterJSON, err := json.Marshal(tc.wantAfter)
					require.NoError(t, err)
					line := fmt.Sprintf("%s: %s: ", tc.operation, tc.path)
					switch tc.operation {
					case "add":
						line += string(afterJSON)
					case "remove":
						line += string(beforeJSON) + " -> (removed)"
					default:
						line += string(beforeJSON) + " -> " + string(afterJSON)
					}
					require.Contains(t, result.Message, line)
				}
				require.True(t, found)
			}
			encoded, err := json.Marshal(data)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "private-")
			require.NotContains(t, result.Message, "private-")
			require.NotContains(t, data, "containerImage")
			for _, excluded := range []string{
				"Container image:", "codeArtifact", "Code:", "Session:", "enableVnextExperience",
			} {
				require.NotContains(t, result.Message, excluded)
			}
			require.True(t, proto.Equal(original, service))
		})
	}
}

func TestPreviewTagReferences(t *testing.T) {
	for _, fragment := range []bool{false, true} {
		t.Run(map[bool]string{false: "tags field reference", true: "root fragment"}[fragment], func(t *testing.T) {
			root := t.TempDir()
			service := previewService(t)
			var content string
			if fragment {
				content = "metadata:\n  tags: [support, responses]\n"
				service.AdditionalProperties.Fields["$ref"] = structpb.NewStringValue("fragment.yaml")
			} else {
				content = "tags: [support, responses]\n"
				service.AdditionalProperties.Fields["metadata"], _ = structpb.NewValue(map[string]any{
					"$ref": "fragment.yaml",
				})
			}

			require.NoError(t, os.WriteFile(filepath.Join(root, "fragment.yaml"), []byte(content), 0600))
			request, inputs := preparePresencePreview(t, service, root)
			require.Equal(t, `["support","responses"]`, request.Metadata["tags"])
			require.True(t, inputs.Declared["metadata.tags"])
			result, err := comparePreviewRequest(service.Name, request, remotePreviewAgent(previewRequest(t)), inputs)
			require.NoError(t, err)
			require.Equal(t, "update", result.Data.AsMap()["status"])
			require.Contains(t, result.Message, `add: metadata.tags: ["support","responses"]`)
			require.NotContains(t, result.Message, "private-")
		})
	}
}

func TestPreviewMetadataTagSchemaAndValidation(t *testing.T) {
	schema := loadDocSchema(t, extensionRoot(t))
	for _, tc := range []struct {
		name  string
		tags  any
		valid bool
	}{
		{name: "string", tags: "one", valid: true},
		{name: "empty string", tags: "", valid: true},
		{name: "list", tags: []any{"one", "two"}, valid: true},
		{name: "empty list", tags: []any{}, valid: true},
		{name: "null"},
		{name: "boolean", tags: false},
		{name: "number", tags: float64(0)},
		{name: "map", tags: map[string]any{"private-key": "private-value"}},
		{name: "mixed list", tags: []any{"private-value", false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := map[string]any{"tags": tc.tags}
			err := schema.validate(map[string]any{
				"host": foundryAgentHost, "kind": "hosted", "name": "agent", "metadata": metadata,
			})
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			service := previewService(t)
			service.AdditionalProperties.Fields["metadata"], _ = structpb.NewValue(metadata)
			resolved, definition, err := resolvePreviewDefinition(service, t.TempDir())
			require.NoError(t, err)
			request, _, err := preparePreviewRequest(resolved, definition, nil, nil)
			if tc.valid {
				require.NoError(t, err)
				require.Contains(t, request.Metadata, "tags")
			} else {
				require.Error(t, err)
				require.Nil(t, request)
				require.NotContains(t, err.Error(), "private-")
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Contains(t, local.Suggestion, "metadata.tags")
			}
		})
	}
}

func TestPreviewTagValuesRemainSafeAndCompareBeforeSanitizing(t *testing.T) {
	for _, tc := range []struct {
		name       string
		tags       any
		before     string
		wantBefore any
		wantAfter  any
	}{
		{name: "known environment secrets", tags: []any{"support", "private-desired-secret"},
			before:     `["support","private-remote-secret"]`,
			wantBefore: []any{"support", "[redacted]"}, wantAfter: []any{"support", "[redacted]"}},
		{name: "escaped environment secret", tags: []any{"support", "private-escaped\nsecret"},
			before: `["support"]`, wantBefore: []any{"support"}, wantAfter: []any{"support", "[redacted]"}},
		//nolint:gosec // Fake credential-bearing URLs verify non-disclosure.
		{name: "URL credentials", tags: []any{
			"support", "https://user:new-password@example.com/tags?sig=new-signature#new-fragment",
		}, before: `["support","https://user:old-password@example.com/tags?sig=old-signature#old-fragment"]`,
			wantBefore: []any{"support", "https://example.com/tags"},
			wantAfter:  []any{"support", "https://example.com/tags"}},
		//nolint:gosec // Fake credential-bearing URLs verify non-disclosure.
		{name: "scalar URL credentials",
			tags:       "https://user:new-password@example.com/tags?sig=new-signature#new-fragment",
			before:     "https://user:old-password@example.com/tags?sig=old-signature#old-fragment",
			wantBefore: "https://example.com/tags", wantAfter: "https://example.com/tags"},
		{name: "terminal controls", tags: []any{"support\nresponses", "support\x1b[31m"},
			before: `["support"]`, wantBefore: []any{"support"}, wantAfter: []any{"support?responses", "support?[31m"}},
		{name: "scalar compatibility", tags: "[not a list]", before: "general",
			wantBefore: "general", wantAfter: "[not a list]"},
		{name: "scalar null compatibility", tags: "null", before: "general",
			wantBefore: "general", wantAfter: "null"},
		{name: "scalar mixed JSON compatibility", tags: `["support",null,1]`, before: "general",
			wantBefore: "general", wantAfter: `["support",null,1]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := previewService(t)
			service.Environment = map[string]string{
				"API_KEY": "private-desired-secret", "ESCAPED_KEY": "private-escaped\nsecret",
			}
			service.AdditionalProperties.Fields["metadata"], _ = structpb.NewValue(map[string]any{"tags": tc.tags})
			request, inputs := preparePresencePreview(t, service, t.TempDir())
			remote := remotePreviewAgent(request)
			remote.Versions.Latest.Metadata = maps.Clone(request.Metadata)
			remote.Versions.Latest.Metadata["tags"] = tc.before
			hosted, ok := remote.Versions.Latest.Definition.(agent_api.HostedAgentDefinition)
			require.True(t, ok)
			hosted.EnvironmentVariables = maps.Clone(hosted.EnvironmentVariables)
			hosted.EnvironmentVariables["REMOTE_KEY"] = "private-remote-secret"
			remote.Versions.Latest.Definition = hosted
			result, err := comparePreviewRequest(service.Name, request, remote, inputs)
			require.NoError(t, err)
			data := result.Data.AsMap()
			require.Equal(t, "update", data["status"], "raw tags must differ even if sanitized tags match")
			require.Equal(t, []any{map[string]any{
				"group": "environmentVariables", "path": "definition.environment_variables.REMOTE_KEY",
				"operation": "remove", "before": "[redacted]",
			}, map[string]any{
				"group": "metadata", "path": "metadata.tags", "operation": "update",
				"before": tc.wantBefore, "after": tc.wantAfter,
			}}, data["changes"])
			before, err := json.Marshal(tc.wantBefore)
			require.NoError(t, err)
			after, err := json.Marshal(tc.wantAfter)
			require.NoError(t, err)
			require.Contains(t, result.Message, fmt.Sprintf("update: metadata.tags: %s -> %s", before, after))
			encoded, err := json.Marshal(data)
			require.NoError(t, err)
			for _, sensitive := range []string{
				"private-", "new-password", "old-password", "new-signature", "old-signature", "new-fragment", "old-fragment",
			} {
				require.NotContains(t, result.Message, sensitive)
				require.NotContains(t, string(encoded), sensitive)
			}
			require.NotContains(t, data, "containerImage")
		})
	}
}
