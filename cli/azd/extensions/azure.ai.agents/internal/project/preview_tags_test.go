// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"errors"
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
		name      string
		metadata  map[string]any
		before    map[string]string
		create    bool
		status    string
		operation string
		path      string
	}{
		{name: "create list", create: true, metadata: map[string]any{"tags": []any{"private-new"}},
			status: "create", operation: "add", path: "metadata.tags"},
		{name: "add list", metadata: map[string]any{"tags": []any{"private-new"}},
			status: "update", operation: "add", path: "metadata.tags"},
		{name: "update list", metadata: map[string]any{"tags": []any{"private-retained", "private-added"}},
			before: map[string]string{"tags": `["private-retained","private-removed"]`},
			status: "update", operation: "update", path: "metadata.tags"},
		{name: "remove last tag", before: map[string]string{"tags": `["private-removed"]`},
			status: "update", operation: "remove", path: "metadata.tags"},
		{name: "remove all metadata", metadata: map[string]any{}, before: map[string]string{"tags": `["private-removed"]`},
			status: "update", operation: "remove", path: "metadata.tags"},
		{name: "empty list is present", metadata: map[string]any{"tags": []any{}},
			before: map[string]string{"tags": `["private-removed"]`},
			status: "update", operation: "update", path: "metadata.tags"},
		{name: "empty string is present", metadata: map[string]any{"tags": ""},
			status: "update", operation: "add", path: "metadata.tags"},
		{name: "unchanged list", metadata: map[string]any{"tags": []any{"private-same"}},
			before: map[string]string{"tags": `["private-same"]`}, status: "noChange"},
		{name: "unchanged string", metadata: map[string]any{"tags": "private-same"},
			before: map[string]string{"tags": "private-same"}, status: "noChange"},
		{name: "credential-bearing list", metadata: map[string]any{
			"tags": []any{"https://private-user:private-password@example.com?sig=private-signature#private-fragment"},
		}, status: "update", operation: "add", path: "metadata.tags"},
		{name: "absent on both sides", status: "noChange"},
		{name: "add metadata key", metadata: map[string]any{"owner": "private-team"},
			status: "update", operation: "add", path: "metadata.owner"},
		{name: "update metadata key", metadata: map[string]any{"owner": "private-new"},
			before: map[string]string{"owner": "private-old"},
			status: "update", operation: "update", path: "metadata.owner"},
		{name: "remove metadata key", before: map[string]string{"owner": "private-old"},
			status: "update", operation: "remove", path: "metadata.owner"},
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
						require.Equal(t, "[redacted]", change["before"])
					} else {
						require.NotContains(t, change, "before")
					}
					if tc.operation == "remove" {
						require.NotContains(t, change, "after")
					} else if tc.name == "empty string is present" {
						require.Equal(t, "", change["after"])
					} else {
						require.Equal(t, "[redacted]", change["after"])
					}
					require.Contains(t, result.Message, tc.operation+": "+tc.path+":")
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
				content = "metadata:\n  tags: [private-one, private-two]\n"
				service.AdditionalProperties.Fields["$ref"] = structpb.NewStringValue("fragment.yaml")
			} else {
				content = "tags: [private-one, private-two]\n"
				service.AdditionalProperties.Fields["metadata"], _ = structpb.NewValue(map[string]any{
					"$ref": "fragment.yaml",
				})
			}

			require.NoError(t, os.WriteFile(filepath.Join(root, "fragment.yaml"), []byte(content), 0600))
			request, inputs := preparePresencePreview(t, service, root)
			require.Equal(t, `["private-one","private-two"]`, request.Metadata["tags"])
			require.True(t, inputs.Declared["metadata.tags"])
			result, err := comparePreviewRequest(service.Name, request, remotePreviewAgent(previewRequest(t)), inputs)
			require.NoError(t, err)
			require.Equal(t, "update", result.Data.AsMap()["status"])
			require.Contains(t, result.Message, `add: metadata.tags: "[redacted]"`)
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
