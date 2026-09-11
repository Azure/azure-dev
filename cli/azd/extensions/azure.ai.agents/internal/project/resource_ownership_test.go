// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestAgentResourceOwnershipConfigAndSchema(t *testing.T) {
	t.Parallel()
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	tests := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{
			name: "connection definitions",
			props: map[string]any{"connections": []any{map[string]any{
				"name": "search", "category": "CognitiveSearch", "authType": "ApiKey", "target": "https://search.test",
			}}},
			wantErr: "azure.ai.connection services",
		},
		{
			name:  "empty connections still require migration",
			props: map[string]any{"connections": []any{}}, wantErr: "azure.ai.connection services",
		},
		{
			name: "full toolbox definition",
			props: map[string]any{"toolboxes": []any{map[string]any{
				"name": "tools", "tools": []any{map[string]any{"type": "mcp"}},
			}}},
			wantErr: "azure.ai.toolbox services",
		},
		{
			name: "empty toolbox definition is not a reference",
			props: map[string]any{"toolboxes": []any{map[string]any{
				"name": "tools", "tools": []any{},
			}}},
			wantErr: "azure.ai.toolbox services",
		},
		{
			name: "external endpoint belongs on split toolbox",
			props: map[string]any{"toolboxes": []any{map[string]any{
				"name": "tools", "endpoint": "https://existing.test/mcp",
			}}},
			wantErr: "set endpoint on the toolbox service",
		},
		{
			name:  "string and name-only references",
			props: map[string]any{"toolboxes": []any{"tools", map[string]any{"name": "more-tools"}}},
		},
		{
			name: "runtime tool connections remain agent-owned",
			props: map[string]any{"toolConnections": []any{map[string]any{
				"name": "runtime", "category": "RemoteTool", "target": "${RUNTIME_ENDPOINT}", "authType": "None",
			}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			props, err := structpb.NewStruct(tt.props)
			require.NoError(t, err)
			for _, nested := range []bool{false, true} {
				svc := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, AdditionalProperties: props}
				if nested {
					svc.Config, svc.AdditionalProperties = props, nil
				}
				cfg, err := LoadServiceTargetAgentConfig(svc)
				if tt.wantErr != "" {
					require.ErrorContains(t, err, tt.wantErr)
					require.ErrorContains(t, err, "azd deploy --all")
					require.Error(t, schema.validate(tt.props))
					continue
				}
				require.NoError(t, err)
				require.NoError(t, schema.validate(tt.props))
				if len(cfg.Toolboxes) > 0 {
					require.Equal(t, []Toolbox{{Name: "tools"}, {Name: "more-tools"}}, cfg.Toolboxes)
				}
				if len(cfg.ToolConnections) > 0 {
					require.Equal(t, "${RUNTIME_ENDPOINT}", cfg.ToolConnections[0].Target)
				}
			}
		})
	}
}

func TestAgentResourceOwnershipAfterFileRefsAndConfigSelection(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"connections", "toolboxes"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "resource.yaml"),
				[]byte("name: resource\n"+"tools: []\ncategory: ApiKey\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"),
				[]byte("kind: hosted\n"+field+":\n  - $ref: ./resource.yaml\n"), 0o600))
			ref, err := structpb.NewStruct(map[string]any{"$ref": "./agent.yaml"})
			require.NoError(t, err)
			clean, err := structpb.NewStruct(map[string]any{"kind": "hosted"})
			require.NoError(t, err)
			svc := &azdext.ServiceConfig{
				Name: "agent", Host: foundryAgentHost, AdditionalProperties: ref, Config: clean,
			}
			require.NoError(t, ResolveServiceConfigInPlace(svc, root))
			_, err = LoadServiceTargetAgentConfig(svc)
			require.ErrorContains(t, err, "bundled")

			// Validation follows the effective config, not an ignored older shape.
			svc.Config, svc.AdditionalProperties = svc.AdditionalProperties, clean
			_, err = LoadServiceTargetAgentConfig(svc)
			require.NoError(t, err)

			// Inline metadata without a kind defers to the config-nested definition.
			svc.AdditionalProperties, err = structpb.NewStruct(map[string]any{"startupCommand": "python main.py"})
			require.NoError(t, err)
			_, err = LoadServiceTargetAgentConfig(svc)
			require.ErrorContains(t, err, "bundled")
		})
	}
}

func TestAgentToolboxNameOnlyFileRef(t *testing.T) {
	t.Parallel()
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	for _, tt := range []struct {
		name, body, override, wantName, wantErr string
	}{
		{name: "name-only YAML", body: "name: tools\n", wantName: "tools"},
		{name: "name-only JSON", body: `{"name":"tools"}`, wantName: "tools"},
		{name: "name overlay", body: "name: original\n", override: "tools", wantName: "tools"},
		{name: "bundled tools", body: "name: tools\ntools: []\n", wantErr: "bundled toolbox definitions"},
		{name: "bundled endpoint", body: "name: tools\nendpoint: https://tools.test/mcp\n",
			wantErr: "set endpoint on the toolbox service"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "reference.yaml"), []byte(tt.body), 0o600))
			ref := map[string]any{"$ref": "./reference.yaml"}
			if tt.override != "" {
				ref["name"] = tt.override
			}
			values := map[string]any{"kind": "hosted", "toolboxes": []any{ref}}
			// Schema tooling sees the reference before runtime file expansion.
			require.NoError(t, schema.validate(values))
			props, err := structpb.NewStruct(values)
			require.NoError(t, err)
			svc := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, AdditionalProperties: props}
			require.NoError(t, ResolveServiceConfigInPlace(svc, root))
			cfg, err := LoadServiceTargetAgentConfig(svc)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Error(t, schema.validate(svc.AdditionalProperties.AsMap()))
				return
			}
			require.NoError(t, err)
			require.NoError(t, schema.validate(svc.AdditionalProperties.AsMap()))
			require.Equal(t, []Toolbox{{Name: tt.wantName}}, cfg.Toolboxes)
		})
	}
}

func TestAgentToolboxFileRefSchemaRejectsInvalidEntries(t *testing.T) {
	t.Parallel()
	schema := loadDocSchema(t, filepath.Join("..", ".."))
	for _, tt := range []struct {
		name  string
		entry map[string]any
	}{
		{"empty ref", map[string]any{"$ref": ""}},
		{"blank ref", map[string]any{"$ref": " \t"}},
		{"non-string ref", map[string]any{"$ref": 42}},
		{"empty name overlay", map[string]any{"$ref": "./reference.yaml", "name": ""}},
		{"bundled tools overlay", map[string]any{"$ref": "./reference.yaml", "tools": []any{}}},
		{"bundled endpoint overlay", map[string]any{"$ref": "./reference.yaml", "endpoint": "https://tools.test/mcp"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Error(t, schema.validate(map[string]any{"toolboxes": []any{tt.entry}}))
		})
	}
}
