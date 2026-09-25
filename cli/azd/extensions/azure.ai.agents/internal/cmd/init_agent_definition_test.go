// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestProbeAgentDefinitionForInitIgnoresRuntimeDefinitionPath(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, root string) *azdext.ServiceConfig
	}{
		{
			name: "direct definition",
			setup: func(t *testing.T, _ string) *azdext.ServiceConfig {
				t.Helper()
				return &azdext.ServiceConfig{
					Name: "agent",
					Host: AiAgentHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"kind": "hosted",
						"name": "direct-agent",
						"environmentVariables": []any{
							map[string]any{"name": "ENABLED", "value": "true"},
						},
					}),
				}
			},
		},
		{
			name: "nested config definition",
			setup: func(t *testing.T, _ string) *azdext.ServiceConfig {
				t.Helper()
				return &azdext.ServiceConfig{
					Name: "agent",
					Host: AiAgentHost,
					Config: mustStruct(t, map[string]any{
						"kind": "hosted",
						"name": "nested-agent",
					}),
				}
			},
		},
		{
			name: "implicit agent yaml",
			setup: func(t *testing.T, root string) *azdext.ServiceConfig {
				t.Helper()
				require.NoError(t, os.WriteFile(
					filepath.Join(root, "agent.yaml"),
					[]byte("kind: hosted\nname: disk-agent\n"),
					0o600,
				))
				return &azdext.ServiceConfig{
					Name:         "agent",
					Host:         AiAgentHost,
					RelativePath: ".",
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			override := filepath.Join(root, "override.yaml")
			require.NoError(t, os.WriteFile(override, []byte("not: [valid"), 0o600))
			t.Setenv("AGENT_DEFINITION_PATH", override)
			svc := tt.setup(t, root)
			before := proto.Clone(svc).(*azdext.ServiceConfig)

			probe, err := probeAgentDefinitionForInit(svc, root)
			require.NoError(t, err)
			require.True(t, probe.found)
			require.True(t, probe.isHosted)
			require.Equal(t, "hosted", string(probe.kind))
			require.True(t, proto.Equal(before, svc), "init probe mutated the original service")

			kind, err := probeAgentKindForInit(svc, root)
			require.NoError(t, err)
			require.Equal(t, "hosted", string(kind))
			require.True(t, proto.Equal(before, svc), "init kind probe mutated the original service")

			_, _, _, err = projectpkg.LoadAgentDefinition(svc, root)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeUnsupportedAgentDefinitionPath, localErr.Code)
		})
	}
}
