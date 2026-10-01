// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestValidateAgentEndpointOperationMatrix(t *testing.T) {
	tests := []struct {
		kind      string
		values    map[string]any
		operation AgentEndpointOperation
		supported bool
	}{
		{"hosted", map[string]any{"kind": "hosted", "name": "hosted"}, AgentEndpointOperationShow, true},
		{"hosted", map[string]any{"kind": "hosted", "name": "hosted"}, AgentEndpointOperationUpdate, true},
		{"hosted", map[string]any{"kind": "hosted", "name": "hosted"}, AgentEndpointOperationReport, true},
		{
			"prompt",
			map[string]any{"kind": "prompt", "name": "prompt", "model": "gpt-5-mini", "instructions": "Help."},
			AgentEndpointOperationShow,
			true,
		},
		{
			"prompt",
			map[string]any{"kind": "prompt", "name": "prompt", "model": "gpt-5-mini", "instructions": "Help."},
			AgentEndpointOperationUpdate,
			false,
		},
		{
			"voice",
			map[string]any{"kind": "voice", "name": "voice", "model": map[string]any{"id": "gpt-realtime"}},
			AgentEndpointOperationShow,
			true,
		},
		{
			"prompt-voice",
			map[string]any{"kind": "prompt-voice", "name": "voice", "model": map[string]any{"id": "gpt-realtime"}},
			AgentEndpointOperationReport,
			true,
		},
		{
			"voice",
			map[string]any{"kind": "voice", "name": "voice", "model": map[string]any{"id": "gpt-realtime"}},
			AgentEndpointOperationUpdate,
			false,
		},
		{
			"workflow",
			map[string]any{"kind": "workflow", "name": "workflow"},
			AgentEndpointOperationShow,
			false,
		},
		{
			"workflow",
			map[string]any{"kind": "workflow", "name": "workflow"},
			AgentEndpointOperationReport,
			false,
		},
	}

	for _, tt := range tests {
		for _, source := range []string{"inline", "root-ref"} {
			t.Run(tt.kind+"/"+string(tt.operation)+"/"+source, func(t *testing.T) {
				root := t.TempDir()
				svc := endpointPolicyTestService(t, root, source, tt.values)

				got, err := ValidateAgentEndpointOperation(svc, root, tt.operation)

				if tt.supported {
					require.NoError(t, err)
					require.Equal(t, tt.kind, string(got.Kind))
					return
				}
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, exterrors.CodeUnsupportedAgentKind, localErr.Code)
				require.NotContains(t, localErr.Suggestion, "set kind")
			})
		}
	}
}

func TestValidateAgentEndpointOperationPreservesDefinitionError(t *testing.T) {
	svc := endpointPolicyTestService(t, t.TempDir(), "inline", map[string]any{
		"kind": "prompt", "name": "prompt", "instructions": "Help.",
	})

	_, err := ValidateAgentEndpointOperation(svc, t.TempDir(), AgentEndpointOperationShow)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(t, exterrors.CodeInvalidAgentManifest, localErr.Code)
	require.ErrorContains(t, err, "non-empty model")
}

func endpointPolicyTestService(
	t *testing.T,
	root string,
	source string,
	values map[string]any,
) *azdext.ServiceConfig {
	t.Helper()

	propsValues := values
	if source == "root-ref" {
		definitionDir := filepath.Join(root, "definitions")
		require.NoError(t, os.MkdirAll(definitionDir, 0o700))
		data, err := yaml.Marshal(values)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(definitionDir, "agent.yaml"), data, 0o600))
		propsValues = map[string]any{"$ref": "./definitions/agent.yaml"}
	}

	props, err := structpb.NewStruct(propsValues)
	require.NoError(t, err)
	return &azdext.ServiceConfig{
		Name:                 "agent-service",
		Host:                 "azure.ai.agent",
		RelativePath:         ".",
		AdditionalProperties: props,
	}
}
