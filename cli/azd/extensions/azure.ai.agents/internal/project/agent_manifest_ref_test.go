// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func mustStruct(t *testing.T, fields map[string]any) *structpb.Struct {
	t.Helper()

	s, err := structpb.NewStruct(fields)
	require.NoError(t, err)
	return s
}

// TestDeclaredAgentManifest covers where the `manifest:` key may live on a
// service entry. Service-level properties win over the nested config block so
// the unified azure.yaml shape reads the same way the inline agent definition
// does.
func TestDeclaredAgentManifest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		svc  *azdext.ServiceConfig
		want string
	}{
		{
			name: "nil service",
		},
		{
			name: "no manifest declared falls back to the convention",
			svc:  &azdext.ServiceConfig{Name: "agent"},
		},
		{
			name: "service-level manifest",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"manifest": "agent.yaml"}),
			},
			want: "agent.yaml",
		},
		{
			name: "config-level manifest",
			svc: &azdext.ServiceConfig{
				Config: mustStruct(t, map[string]any{"manifest": "nested.yaml"}),
			},
			want: "nested.yaml",
		},
		{
			name: "service-level wins over config-level",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"manifest": "outer.yaml"}),
				Config:               mustStruct(t, map[string]any{"manifest": "inner.yaml"}),
			},
			want: "outer.yaml",
		},
		{
			name: "blank value is treated as undeclared",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"manifest": "   "}),
			},
		},
		{
			name: "non-string value is ignored",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"manifest": 42}),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, declaredAgentManifest(tc.svc))
		})
	}
}

// TestResolveDeclaredManifestPath pins the confinement rules. A manifest is
// part of one service's source, so it must stay inside that service's project
// directory even when the path would still land inside the azd project.
func TestResolveDeclaredManifestPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	tests := []struct {
		name        string
		servicePath string
		declared    string
		wantRel     string
		wantErr     bool
	}{
		{
			name:        "sibling file",
			servicePath: "src/triage",
			declared:    "agent.yaml",
			wantRel:     filepath.Join("src", "triage", "agent.yaml"),
		},
		{
			name:        "nested file",
			servicePath: "src/triage",
			declared:    "agents/primary.yml",
			wantRel:     filepath.Join("src", "triage", "agents", "primary.yml"),
		},
		{
			name:        "service at project root",
			servicePath: ".",
			declared:    "agent.yaml",
			wantRel:     "agent.yaml",
		},
		{
			name:        "escaping the service directory is rejected",
			servicePath: "src/triage",
			declared:    "../other/agent.yaml",
			wantErr:     true,
		},
		{
			name:        "escaping the project root is rejected",
			servicePath: "src/triage",
			declared:    "../../../agent.yaml",
			wantErr:     true,
		},
		{
			name:        "absolute paths are rejected",
			servicePath: "src/triage",
			declared:    "/etc/agent.yaml",
			wantErr:     true,
		},
		{
			name:        "non-YAML extensions are rejected",
			servicePath: "src/triage",
			declared:    "agent.json",
			wantErr:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolveDeclaredManifestPath(root, tc.servicePath, tc.declared, "triage-agent")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, filepath.Join(root, tc.wantRel), got)
		})
	}
}
