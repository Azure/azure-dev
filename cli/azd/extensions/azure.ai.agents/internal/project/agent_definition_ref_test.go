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

// TestDeclaredAgentDefinitionRef covers where the `$ref:` include may live on a
// service entry. Service-level properties win over the nested config block so
// the unified azure.yaml shape reads the same way the inline agent definition
// does.
func TestDeclaredAgentDefinitionRef(t *testing.T) {
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
			name: "no ref declared falls back to the convention",
			svc:  &azdext.ServiceConfig{Name: "agent"},
		},
		{
			name: "service-level ref",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"$ref": "./agent.yaml"}),
			},
			want: "./agent.yaml",
		},
		{
			name: "config-level ref",
			svc: &azdext.ServiceConfig{
				Config: mustStruct(t, map[string]any{"$ref": "./nested.yaml"}),
			},
			want: "./nested.yaml",
		},
		{
			name: "service-level wins over config-level",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"$ref": "./outer.yaml"}),
				Config:               mustStruct(t, map[string]any{"$ref": "./inner.yaml"}),
			},
			want: "./outer.yaml",
		},
		{
			name: "blank value is treated as undeclared",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"$ref": "   "}),
			},
		},
		{
			name: "non-string value is ignored",
			svc: &azdext.ServiceConfig{
				AdditionalProperties: mustStruct(t, map[string]any{"$ref": 42}),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, declaredAgentDefinitionRef(tc.svc))
		})
	}
}

// TestResolveDeclaredRefPath pins the confinement rules. A `$ref` resolves
// against the directory holding azure.yaml — the same anchor the shared include
// machinery uses — and may not escape it.
func TestResolveDeclaredRefPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	tests := []struct {
		name     string
		declared string
		wantRel  string
		wantErr  bool
	}{
		{
			name:     "sibling file",
			declared: "./agent.yaml",
			wantRel:  "agent.yaml",
		},
		{
			name:     "bare name",
			declared: "agent.yaml",
			wantRel:  "agent.yaml",
		},
		{
			name:     "nested file",
			declared: "./src/triage/agent.yml",
			wantRel:  filepath.Join("src", "triage", "agent.yml"),
		},
		{
			name:     "escaping the project root is rejected",
			declared: "../agent.yaml",
			wantErr:  true,
		},
		{
			name:     "absolute paths are rejected",
			declared: "/etc/agent.yaml",
			wantErr:  true,
		},
		{
			name:     "non-YAML extensions are rejected",
			declared: "./agent.json",
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolveDeclaredRefPath(root, tc.declared, "triage-agent")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, filepath.Join(root, tc.wantRel), got)
		})
	}
}
